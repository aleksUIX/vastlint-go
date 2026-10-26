//go:build vastlint_cgo

package vastlint

// The native engine. Requires libvastlint and its header, found through
// pkg-config ("vastlint"), or CGO_CFLAGS / CGO_LDFLAGS set by the host. Build
// with: go build -tags vastlint_cgo

/*
#cgo pkg-config: vastlint
#include <stdlib.h>
#include <vastlint.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

type cgoEngine struct {
	cfg     Config
	rules   []Rule
	known   map[string]bool
	version string
	// Native calls run only on PoolSize goroutines pinned to their OS threads.
	// Go otherwise runs a cgo call on whichever thread the goroutine happens to
	// hold, and under load Go rotates through hundreds of threads; every thread
	// that enters the library's allocator leaves a per-thread heap behind.
	// Measured on a loaded machine: 900+ threads and 600 to 900 MB resident in
	// allocator arenas. Pinning confines that state to PoolSize threads.
	jobs      chan cgoJob
	done      chan struct{}
	closeOnce sync.Once
}

type cgoJob struct {
	xml   string
	opts  Options
	reply chan cgoReply
}

type cgoReply struct {
	res *Result
	err error
}

func newEngine(_ context.Context, cfg Config) (Engine, error) {
	e := &cgoEngine{cfg: cfg, version: C.GoString(C.vastlint_version()), jobs: make(chan cgoJob), done: make(chan struct{})}
	if err := checkVersion(e.version); err != nil {
		return nil, err
	}
	raw := C.vastlint_rules_json()
	if raw == nil {
		return nil, fmt.Errorf("%w: library returned no rule catalog", ErrEngine)
	}
	rules, err := parseRules([]byte(C.GoString(raw)))
	if err != nil {
		return nil, err
	}
	e.rules = rules
	e.known = ruleSet(rules)
	for i := 0; i < cfg.PoolSize; i++ {
		go e.worker()
	}
	return e, nil
}

func (e *cgoEngine) worker() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for {
		select {
		case <-e.done:
			return
		case job := <-e.jobs:
			res, err := e.call(job.xml, job.opts)
			job.reply <- cgoReply{res: res, err: err}
		}
	}
}

func (e *cgoEngine) Validate(ctx context.Context, xml string, opts Options) (*Result, error) {
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
	job := cgoJob{xml: xml, opts: opts, reply: make(chan cgoReply, 1)}
	// The jobs channel is never closed, so a call that races Close cannot
	// panic; it observes done and reports the engine as closed.
	select {
	case e.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, fmt.Errorf("%w: engine closed", ErrEngine)
	}
	// The reply channel is buffered, so a worker never blocks if the caller
	// gives up while the call is in flight.
	select {
	case r := <-job.reply:
		return r.res, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// call runs on a pinned worker thread.
func (e *cgoEngine) call(xml string, opts Options) (*Result, error) {
	// The library reads (pointer, length) and keeps nothing, so the Go string's
	// bytes can be passed without a copy. cgo pins them for the call.
	xmlPtr := (*C.char)(unsafe.Pointer(unsafe.StringData(xml)))
	var raw *C.VastlintResult
	if opts.WrapperDepth == 0 && opts.MaxWrapperDepth == 0 && len(opts.RuleOverrides) == 0 {
		raw = C.vastlint_validate(xmlPtr, C.size_t(len(xml)))
	} else {
		var overrides *C.char
		if len(opts.RuleOverrides) > 0 {
			b, err := json.Marshal(opts.RuleOverrides)
			if err != nil {
				return nil, err
			}
			overrides = C.CString(string(b))
			defer C.free(unsafe.Pointer(overrides))
		}
		raw = C.vastlint_validate_with_options(xmlPtr, C.size_t(len(xml)), C.uint(opts.WrapperDepth), C.uint(opts.MaxWrapperDepth), overrides)
	}
	if raw == nil {
		// NULL means the library refused or failed: not UTF-8, or a panic caught
		// at the boundary. Either way the document was not validated.
		return nil, fmt.Errorf("%w: library returned NULL", ErrEngine)
	}
	defer C.vastlint_result_free(raw)
	js := C.vastlint_result_json(raw)
	if js == nil {
		return nil, fmt.Errorf("%w: result has no JSON", ErrEngine)
	}
	return parseResult([]byte(C.GoString(js)))
}

func (e *cgoEngine) Rules() []Rule {
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

func (e *cgoEngine) Version() string { return e.version }
func (e *cgoEngine) Kind() string    { return "cgo" }

// Close stops the worker threads. Calls in flight finish; later calls return
// ErrEngine. Safe to call more than once and concurrently with Validate.
func (e *cgoEngine) Close() error {
	e.closeOnce.Do(func() { close(e.done) })
	return nil
}
