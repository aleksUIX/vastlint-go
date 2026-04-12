// Package vastlint provides Go bindings for the vastlint VAST XML validator.
//
// vastlint validates IAB VAST XML tags against 108 rules covering VAST 2.0
// through 4.3. It validates production-size tags (17–44 KB) in under
// 2.3 milliseconds and requires no network calls or external services.
//
// Basic usage:
//
//	result, err := vastlint.Validate(xmlString)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if !result.Valid {
//	    for _, issue := range result.Issues {
//	        fmt.Printf("[%s] %s\n", issue.Severity, issue.Message)
//	    }
//	}
//
// With options:
//
//	result, err := vastlint.ValidateWithOptions(xmlString, vastlint.Options{
//	    WrapperDepth:    2,
//	    MaxWrapperDepth: 5,
//	    RuleOverrides: map[string]string{
//	        "VAST-2.0-mediafile-https":       "error",
//	        "VAST-4.1-mezzanine-recommended": "off",
//	    },
//	})
package vastlint

/*
#cgo darwin,arm64  LDFLAGS: -L${SRCDIR}/libs/darwin_arm64  -lvastlint -lm
#cgo darwin,amd64  LDFLAGS: -L${SRCDIR}/libs/darwin_amd64  -lvastlint -lm
#cgo linux,arm64   LDFLAGS: -L${SRCDIR}/libs/linux_arm64   -lvastlint -lm -ldl -lpthread
#cgo linux,amd64   LDFLAGS: -L${SRCDIR}/libs/linux_amd64   -lvastlint -lm -ldl -lpthread
#include "vastlint.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// Issue represents a single validation finding.
type Issue struct {
	// ID is the stable rule identifier, e.g. "VAST-4.2-3.4.1".
	ID string `json:"id"`

	// Severity is one of "error", "warning", or "info".
	Severity string `json:"severity"`

	// Message is a human-readable description of the finding.
	Message string `json:"message"`

	// Path is the XPath-like location in the document, e.g.
	// "VAST/Ad/InLine/Creatives". Empty string if not applicable.
	Path string `json:"path"`

	// SpecRef is the spec section that defines the rule, e.g.
	// "IAB VAST 4.2 §3.4.1".
	SpecRef string `json:"spec_ref"`
}

// Summary contains aggregate counts for a validation result.
type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

// Result is the output of a validation call.
type Result struct {
	// Version is the detected VAST version string, e.g. "4.2".
	// Empty string if the version could not be determined.
	Version string `json:"version"`

	// Issues is the list of all validation findings.
	Issues []Issue `json:"issues"`

	// Summary contains aggregate counts by severity.
	Summary Summary `json:"summary"`

	// Valid is true when Summary.Errors == 0.
	Valid bool `json:"valid"`
}

// Options configures an optional validation call.
type Options struct {
	// WrapperDepth is the current wrapper chain depth. Pass 0 for the
	// outermost tag in a chain (the default).
	WrapperDepth uint

	// MaxWrapperDepth is the maximum allowed wrapper chain depth. Pass 0
	// to use the default (5).
	MaxWrapperDepth uint

	// RuleOverrides maps rule IDs to severity strings. Valid severity
	// values are "error", "warning", "info", and "off". Unknown rule IDs
	// and unrecognised severity strings are silently ignored, matching the
	// CLI config-file behaviour.
	//
	// Example:
	//   map[string]string{
	//       "VAST-2.0-mediafile-https":       "error",
	//       "VAST-4.1-mezzanine-recommended": "off",
	//   }
	RuleOverrides map[string]string
}

// Validate validates a VAST XML string using the default rule configuration.
//
// Returns a non-nil error only if xml is empty or if the internal JSON
// deserialisation of the result fails (should never happen in practice).
func Validate(xml string) (*Result, error) {
	if xml == "" {
		return nil, fmt.Errorf("vastlint: xml must not be empty")
	}

	cxml := C.CString(xml)
	defer C.free(unsafe.Pointer(cxml))

	raw := C.vastlint_validate(cxml, C.size_t(len(xml)))
	if raw == nil {
		return nil, fmt.Errorf("vastlint: internal error — vastlint_validate returned NULL")
	}
	defer C.vastlint_result_free(raw)

	return parseResult(raw)
}

// ValidateWithOptions validates a VAST XML string with caller-supplied options.
//
// A zero-value Options struct is equivalent to calling Validate.
func ValidateWithOptions(xml string, opts Options) (*Result, error) {
	if xml == "" {
		return nil, fmt.Errorf("vastlint: xml must not be empty")
	}

	cxml := C.CString(xml)
	defer C.free(unsafe.Pointer(cxml))

	// Serialise rule overrides to JSON for the C layer.
	var coverrides *C.char
	if len(opts.RuleOverrides) > 0 {
		b, err := json.Marshal(opts.RuleOverrides)
		if err != nil {
			return nil, fmt.Errorf("vastlint: failed to serialise rule overrides: %w", err)
		}
		cs := C.CString(string(b))
		defer C.free(unsafe.Pointer(cs))
		coverrides = cs
	}

	raw := C.vastlint_validate_with_options(
		cxml,
		C.size_t(len(xml)),
		C.uint(opts.WrapperDepth),
		C.uint(opts.MaxWrapperDepth),
		coverrides,
	)
	if raw == nil {
		return nil, fmt.Errorf("vastlint: internal error — vastlint_validate_with_options returned NULL")
	}
	defer C.vastlint_result_free(raw)

	return parseResult(raw)
}

// Version returns the vastlint-core library version string, e.g. "0.1.0".
func Version() string {
	return C.GoString(C.vastlint_version())
}

// parseResult reads the JSON out of the opaque C result handle and
// deserialises it into a *Result. The caller is responsible for freeing raw.
func parseResult(raw *C.VastlintResult) (*Result, error) {
	jsonPtr := C.vastlint_result_json(raw)
	if jsonPtr == nil {
		return nil, fmt.Errorf("vastlint: vastlint_result_json returned NULL")
	}

	jsonStr := C.GoString(jsonPtr)

	// The JSON has a nullable "version" field and nullable "path" fields on
	// issues. We unmarshal into an intermediate struct to handle the nulls
	// cleanly, then convert to the public Result type.
	var wire struct {
		Version *string `json:"version"`
		Issues  []struct {
			ID       string  `json:"id"`
			Severity string  `json:"severity"`
			Message  string  `json:"message"`
			Path     *string `json:"path"`
			SpecRef  string  `json:"spec_ref"`
		} `json:"issues"`
		Summary struct {
			Errors   int  `json:"errors"`
			Warnings int  `json:"warnings"`
			Infos    int  `json:"infos"`
			Valid    bool `json:"valid"`
		} `json:"summary"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &wire); err != nil {
		return nil, fmt.Errorf("vastlint: failed to parse result JSON: %w", err)
	}

	result := &Result{
		Summary: Summary{
			Errors:   wire.Summary.Errors,
			Warnings: wire.Summary.Warnings,
			Infos:    wire.Summary.Infos,
		},
		Valid: wire.Summary.Valid,
	}

	if wire.Version != nil {
		result.Version = *wire.Version
	}

	result.Issues = make([]Issue, len(wire.Issues))
	for i, wi := range wire.Issues {
		result.Issues[i] = Issue{
			ID:       wi.ID,
			Severity: wi.Severity,
			Message:  wi.Message,
			SpecRef:  wi.SpecRef,
		}
		if wi.Path != nil {
			result.Issues[i].Path = *wi.Path
		}
	}

	return result, nil
}
