//go:build !vastlint_cgo

package vastlint

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// The engine compiled to wasm32 with the plain C-ABI exports of vastlint-abi.
// Built from the vastlint repository with:
//
//	RUSTFLAGS="-C target-feature=+simd128" cargo build -p vastlint-abi \
//	  --profile release-wasm --target wasm32-unknown-unknown
//
// The checksum file pins the exact module so a stale copy is caught at startup.
//
//go:embed internal/abi/vastlint_abi.wasm
var abiModule []byte

//go:embed internal/abi/vastlint_abi.wasm.sha256
var abiModuleSHA256 string

type wasmEngine struct {
	cfg      Config
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	pool     chan *wasmInstance
	rules    []Rule
	known    map[string]bool
	version  string
	closed   chan struct{}
	mu       sync.Mutex
}

type wasmInstance struct {
	mod                                                   api.Module
	alloc, dealloc, free, validate, validateOpts, rulesFn api.Function
	versionFn                                             api.Function
}

func newEngine(ctx context.Context, cfg Config) (Engine, error) {
	sum := sha256.Sum256(abiModule)
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(strings.Fields(abiModuleSHA256)[0]); got != want {
		return nil, fmt.Errorf("%w: embedded module sha256 %s does not match pinned %s", ErrEngine, got, want)
	}
	// Compiler mode is required: the interpreter is about 40x slower. Fail
	// rather than silently degrade on a platform without a compiler backend.
	// WithCloseOnContextDone is deliberately off: it makes wazero insert
	// interruption checks into generated code and cost about 5x on large
	// documents when measured. The engine bounds its own work (4 MiB input, 128
	// levels, 100,000 elements), so a call always returns; callers that need a
	// deadline check it before the call, as Validate does.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
	compiled, err := rt.CompileModule(ctx, abiModule)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("%w: compile: %v", ErrEngine, err)
	}
	if n := len(compiled.ImportedFunctions()); n != 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("%w: module imports %d host functions; expected none", ErrEngine, n)
	}
	e := &wasmEngine{cfg: cfg, rt: rt, compiled: compiled, pool: make(chan *wasmInstance, cfg.PoolSize), closed: make(chan struct{})}
	for i := 0; i < cfg.PoolSize; i++ {
		inst, err := e.instantiate(ctx)
		if err != nil {
			_ = e.Close()
			return nil, err
		}
		e.pool <- inst
	}
	inst := <-e.pool
	defer func() { e.pool <- inst }()
	v, err := inst.callNoArgs(ctx, inst.versionFn)
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	e.version = string(v)
	if err := checkVersion(e.version); err != nil {
		_ = e.Close()
		return nil, err
	}
	raw, err := inst.callNoArgs(ctx, inst.rulesFn)
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	e.rules, err = parseRules(raw)
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	e.known = ruleSet(e.rules)
	return e, nil
}

func (e *wasmEngine) instantiate(ctx context.Context) (*wasmInstance, error) {
	mod, err := e.rt.InstantiateModule(ctx, e.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("%w: instantiate: %v", ErrEngine, err)
	}
	in := &wasmInstance{
		mod:          mod,
		alloc:        mod.ExportedFunction("vl_alloc"),
		dealloc:      mod.ExportedFunction("vl_dealloc"),
		free:         mod.ExportedFunction("vl_result_free"),
		validate:     mod.ExportedFunction("vl_validate"),
		validateOpts: mod.ExportedFunction("vl_validate_with_options"),
		rulesFn:      mod.ExportedFunction("vl_rules"),
		versionFn:    mod.ExportedFunction("vl_version"),
	}
	for name, f := range map[string]api.Function{"vl_alloc": in.alloc, "vl_dealloc": in.dealloc, "vl_result_free": in.free, "vl_validate": in.validate, "vl_validate_with_options": in.validateOpts, "vl_rules": in.rulesFn, "vl_version": in.versionFn} {
		if f == nil {
			_ = mod.Close(ctx)
			return nil, fmt.Errorf("%w: module lacks export %s", ErrEngine, name)
		}
	}
	return in, nil
}

// take reads a length-prefixed reply and frees it.
func (in *wasmInstance) take(ctx context.Context, rptr uint32) ([]byte, error) {
	if rptr == 0 {
		return nil, fmt.Errorf("%w: engine returned null", ErrEngine)
	}
	prefix, ok := in.mod.Memory().Read(rptr, 4)
	if !ok {
		return nil, fmt.Errorf("%w: reply prefix out of bounds", ErrEngine)
	}
	n := binary.LittleEndian.Uint32(prefix)
	view, ok := in.mod.Memory().Read(rptr+4, n)
	if !ok {
		return nil, fmt.Errorf("%w: reply payload out of bounds", ErrEngine)
	}
	out := make([]byte, n)
	copy(out, view)
	if _, err := in.free.Call(ctx, uint64(rptr)); err != nil {
		return nil, fmt.Errorf("%w: free: %v", ErrEngine, err)
	}
	return out, nil
}

func (in *wasmInstance) callNoArgs(ctx context.Context, f api.Function) ([]byte, error) {
	r, err := f.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEngine, err)
	}
	return in.take(ctx, uint32(r[0]))
}

// write copies s into engine memory; the caller frees with dealloc(ptr, len).
func (in *wasmInstance) write(ctx context.Context, s string) (uint32, error) {
	r, err := in.alloc.Call(ctx, uint64(len(s)))
	if err != nil {
		return 0, fmt.Errorf("%w: alloc: %v", ErrEngine, err)
	}
	ptr := uint32(r[0])
	if ptr == 0 || !in.mod.Memory().WriteString(ptr, s) {
		return 0, fmt.Errorf("%w: could not place %d bytes in engine memory", ErrEngine, len(s))
	}
	return ptr, nil
}

func (in *wasmInstance) run(ctx context.Context, xml string, opts Options) ([]byte, error) {
	xp, err := in.write(ctx, xml)
	if err != nil {
		return nil, err
	}
	defer in.dealloc.Call(ctx, uint64(xp), uint64(len(xml))) //nolint:errcheck
	var r []uint64
	if opts.WrapperDepth == 0 && opts.MaxWrapperDepth == 0 && len(opts.RuleOverrides) == 0 {
		r, err = in.validate.Call(ctx, uint64(xp), uint64(len(xml)))
	} else {
		lines := overrideLines(opts.RuleOverrides)
		var lp uint32
		if lines != "" {
			if lp, err = in.write(ctx, lines); err != nil {
				return nil, err
			}
			defer in.dealloc.Call(ctx, uint64(lp), uint64(len(lines))) //nolint:errcheck
		}
		r, err = in.validateOpts.Call(ctx, uint64(xp), uint64(len(xml)), uint64(opts.WrapperDepth), uint64(opts.MaxWrapperDepth), uint64(lp), uint64(len(lines)))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEngine, err)
	}
	return in.take(ctx, uint32(r[0]))
}

func (e *wasmEngine) Validate(ctx context.Context, xml string, opts Options) (*Result, error) {
	if len(xml) == 0 {
		return nil, ErrEmptyInput
	}
	if len(xml) > e.cfg.MaxInputBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrInputTooLarge, len(xml))
	}
	if opts.WrapperDepth > 255 || opts.MaxWrapperDepth > 255 {
		return nil, errors.New("vastlint: wrapper depth settings must be 0 to 255")
	}
	if err := checkOverrides(e.known, opts.RuleOverrides); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var inst *wasmInstance
	select {
	case inst = <-e.pool:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.closed:
		return nil, fmt.Errorf("%w: engine closed", ErrEngine)
	}
	payload, err := inst.run(ctx, xml, opts)
	// A trap or runaway memory retires this instance; the pool slot is refilled
	// with a fresh one.
	if err != nil || inst.mod.Memory().Size() > uint32(e.cfg.MaxInstanceMemory) {
		_ = inst.mod.Close(context.Background())
		fresh, ierr := e.instantiate(context.Background())
		if ierr != nil {
			// Shrink the pool rather than block callers forever; Close reports it.
			e.mu.Lock()
			e.pool <- nil
			e.mu.Unlock()
		} else {
			e.pool <- fresh
		}
		if err != nil {
			return nil, err
		}
	} else {
		e.pool <- inst
	}
	return parseResult(payload)
}

func (e *wasmEngine) Rules() []Rule {
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

func (e *wasmEngine) Version() string { return e.version }
func (e *wasmEngine) Kind() string    { return "wasm" }

func (e *wasmEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.closed:
		return nil
	default:
		close(e.closed)
	}
	return e.rt.Close(context.Background())
}
