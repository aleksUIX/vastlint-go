// Package vastlint validates IAB VAST, VMAP and DAAST XML in-process from Go.
//
// The rules live in the Rust engine vastlint-core. This package runs that engine
// one of two ways behind the same interface:
//
//   - Default: the engine compiled to WebAssembly and executed by wazero, a pure
//     Go runtime. Nothing to install, CGO_ENABLED=0 works, every platform Go
//     supports. About three times the native cost per document.
//   - Build tag vastlint_cgo: the native library libvastlint linked through cgo
//     and found with pkg-config. Native speed. The host installs the library.
//
// Both engines produce identical findings; the test suite checks that on every
// fixture.
//
//	engine, err := vastlint.New(ctx, vastlint.Config{})
//	defer engine.Close()
//	result, err := engine.Validate(ctx, xml, vastlint.Options{})
//
// The package-level Validate and ValidateWithOptions keep the original API and
// use a lazily created default engine.
package vastlint

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ExpectedVersion is the engine version this package was built against. New
// refuses an engine that reports a different version so a stale library or
// module can never run under a binding that assumes a different catalog.
const ExpectedVersion = "0.16.2"

// Issue is a single validation finding.
type Issue struct {
	// ID is the stable rule identifier, for example "VAST-2.0-inline-impression".
	ID string `json:"id"`
	// Severity is "error", "warning" or "info", after any overrides.
	Severity string `json:"severity"`
	// Message is the human-readable description.
	Message string `json:"message"`
	// Path is the XPath-like location, for example "/VAST/Ad[0]/InLine". Empty
	// for document-level findings.
	Path string `json:"path"`
	// SpecRef names the specification section, for example "IAB VAST 4.2 §3.4.1".
	SpecRef string `json:"spec_ref"`
	// Line and Col are 1-based; zero when the finding has no position.
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Summary counts findings by severity.
type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

// Result is the outcome of validating one document.
type Result struct {
	// DocumentType is "VAST", "VMAP" or "DAAST".
	DocumentType string `json:"document_type"`
	// Version is the detected version, for example "4.2". Empty when unknown.
	Version string `json:"version"`
	// InputStatus is "supported", "unsupported" or "malformed". It is decided
	// independently of rule severity overrides, so a caller can tell non-VAST
	// input from VAST with findings even when rules are turned off.
	InputStatus string  `json:"input_status"`
	Issues      []Issue `json:"issues"`
	Summary     Summary `json:"summary"`
	// Valid is true when there are no error-severity findings.
	Valid bool `json:"valid"`
}

// Options configures one validation call.
type Options struct {
	// WrapperDepth is this document's position in a wrapper chain; 0 for the
	// outermost tag.
	WrapperDepth uint
	// MaxWrapperDepth is the allowed chain depth; 0 selects the default of 5.
	MaxWrapperDepth uint
	// RuleOverrides maps rule ids to "error", "warning", "info" or "off".
	// Unknown rule ids or levels are rejected with ErrUnknownRule.
	RuleOverrides map[string]string
}

// Rule describes one catalog entry.
type Rule struct {
	ID              string `json:"id"`
	DefaultSeverity string `json:"default_severity"`
	Description     string `json:"description"`
	Source          string `json:"source"`
	// RevenueImpact is true when violating the rule tends to cost the impression
	// or its measurement.
	RevenueImpact bool `json:"revenue_impact"`
}

// Config tunes an Engine. Zero values select the defaults named here.
type Config struct {
	// MaxInputBytes rejects larger documents before they reach the engine.
	// Default 1 MiB. The engine itself refuses anything above 4 MiB.
	MaxInputBytes int
	// PoolSize is the number of engine instances kept ready for concurrent
	// callers (wasm engine only; the native engine is stateless). Default
	// runtime.GOMAXPROCS(0).
	PoolSize int
	// MaxInstanceMemory recycles a wasm instance whose linear memory grew past
	// this many bytes, since wasm memory never shrinks. Default 16 MiB.
	MaxInstanceMemory int
}

// Engine validates documents. Implementations are safe for concurrent use.
type Engine interface {
	// Validate checks one document. ctx bounds the call; on the wasm engine a
	// deadline aborts the instance, which is then replaced.
	Validate(ctx context.Context, xml string, opts Options) (*Result, error)
	// Rules returns the engine's rule catalog.
	Rules() []Rule
	// Version returns the engine version, equal to ExpectedVersion.
	Version() string
	// Kind is "wasm" or "cgo".
	Kind() string
	Close() error
}

// Errors returned by engines. Wrap-compare with errors.Is.
var (
	ErrEmptyInput    = errors.New("vastlint: input is empty")
	ErrInputTooLarge = errors.New("vastlint: input exceeds MaxInputBytes")
	ErrUnknownRule   = errors.New("vastlint: unknown rule id or level in RuleOverrides")
	// ErrEngine means the engine failed to produce a verdict (a trap, a panic
	// caught at the boundary, or a malformed reply). The document was not
	// validated and should be counted as an engine failure, not as invalid.
	ErrEngine = errors.New("vastlint: engine failure")
	// ErrVersionMismatch is returned by New when the engine is not ExpectedVersion.
	ErrVersionMismatch = errors.New("vastlint: engine version does not match ExpectedVersion")
)

func (c Config) withDefaults() Config {
	if c.MaxInputBytes <= 0 {
		c.MaxInputBytes = 1 << 20
	}
	if c.PoolSize <= 0 {
		c.PoolSize = defaultPoolSize()
	}
	if c.MaxInstanceMemory <= 0 {
		c.MaxInstanceMemory = 16 << 20
	}
	return c
}

// New creates the engine selected at build time (wasm by default, native with
// -tags vastlint_cgo).
func New(ctx context.Context, cfg Config) (Engine, error) {
	return newEngine(ctx, cfg.withDefaults())
}

var (
	defaultOnce   sync.Once
	defaultEngine Engine
	defaultErr    error
)

func getDefault() (Engine, error) {
	defaultOnce.Do(func() {
		defaultEngine, defaultErr = New(context.Background(), Config{})
	})
	return defaultEngine, defaultErr
}

// Validate validates with default options using a shared engine created on
// first use. For servers, create an Engine with New instead so you control its
// lifetime and limits.
func Validate(xml string) (*Result, error) {
	e, err := getDefault()
	if err != nil {
		return nil, err
	}
	return e.Validate(context.Background(), xml, Options{})
}

// ValidateWithOptions validates with caller-supplied options using the shared
// engine.
func ValidateWithOptions(xml string, opts Options) (*Result, error) {
	e, err := getDefault()
	if err != nil {
		return nil, err
	}
	return e.Validate(context.Background(), xml, opts)
}

// Version returns the engine version of the shared engine, or ExpectedVersion
// if the engine could not be created.
func Version() string {
	if e, err := getDefault(); err == nil {
		return e.Version()
	}
	return ExpectedVersion
}

func checkVersion(got string) error {
	if got != ExpectedVersion {
		return fmt.Errorf("%w: engine reports %q, binding expects %q", ErrVersionMismatch, got, ExpectedVersion)
	}
	return nil
}
