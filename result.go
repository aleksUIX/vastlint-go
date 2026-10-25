package vastlint

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// wireResult is the engine's JSON result document (vastlint_core::json).
type wireResult struct {
	Error        string  `json:"error"`
	DocumentType string  `json:"document_type"`
	Version      *string `json:"version"`
	InputStatus  string  `json:"input_status"`
	Issues       []struct {
		ID       string  `json:"id"`
		Severity string  `json:"severity"`
		Message  string  `json:"message"`
		Path     *string `json:"path"`
		SpecRef  string  `json:"spec_ref"`
		Line     *int    `json:"line"`
		Col      *int    `json:"col"`
	} `json:"issues"`
	Summary struct {
		Errors   int  `json:"errors"`
		Warnings int  `json:"warnings"`
		Infos    int  `json:"infos"`
		Valid    bool `json:"valid"`
	} `json:"summary"`
}

// parseResult decodes an engine reply. An error document becomes ErrEngine
// unless it is one of the input errors this package reports itself.
func parseResult(payload []byte) (*Result, error) {
	var w wireResult
	if err := json.Unmarshal(payload, &w); err != nil {
		return nil, fmt.Errorf("%w: unreadable reply: %v", ErrEngine, err)
	}
	if w.Error != "" {
		switch {
		case strings.HasPrefix(w.Error, "unknown rule id"), strings.HasPrefix(w.Error, "unknown level"), strings.HasPrefix(w.Error, "rule override"):
			return nil, fmt.Errorf("%w: %s", ErrUnknownRule, w.Error)
		case w.Error == "input is empty":
			return nil, ErrEmptyInput
		default:
			return nil, fmt.Errorf("%w: %s", ErrEngine, w.Error)
		}
	}
	if w.DocumentType == "" || w.InputStatus == "" {
		return nil, fmt.Errorf("%w: reply is missing document_type or input_status", ErrEngine)
	}
	r := &Result{
		DocumentType: w.DocumentType,
		InputStatus:  w.InputStatus,
		Issues:       make([]Issue, len(w.Issues)),
		Summary:      Summary{Errors: w.Summary.Errors, Warnings: w.Summary.Warnings, Infos: w.Summary.Infos},
		Valid:        w.Summary.Valid,
	}
	if w.Version != nil {
		r.Version = *w.Version
	}
	for i, wi := range w.Issues {
		r.Issues[i] = Issue{ID: wi.ID, Severity: wi.Severity, Message: wi.Message, SpecRef: wi.SpecRef}
		if wi.Path != nil {
			r.Issues[i].Path = *wi.Path
		}
		if wi.Line != nil {
			r.Issues[i].Line = *wi.Line
		}
		if wi.Col != nil {
			r.Issues[i].Col = *wi.Col
		}
	}
	return r, nil
}

func parseRules(payload []byte) ([]Rule, error) {
	var rules []Rule
	if err := json.Unmarshal(payload, &rules); err != nil {
		return nil, fmt.Errorf("%w: unreadable rule catalog: %v", ErrEngine, err)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%w: empty rule catalog", ErrEngine)
	}
	return rules, nil
}

var levels = map[string]bool{"error": true, "warning": true, "info": true, "off": true}

// checkOverrides validates RuleOverrides against the catalog before any call,
// so a misconfigured caller fails loudly instead of validating with defaults.
func checkOverrides(known map[string]bool, overrides map[string]string) error {
	for id, level := range overrides {
		if !known[id] {
			return fmt.Errorf("%w: rule %q", ErrUnknownRule, id)
		}
		if !levels[level] {
			return fmt.Errorf("%w: level %q for rule %q", ErrUnknownRule, level, id)
		}
	}
	return nil
}

// overrideLines renders overrides as the text the wasm ABI accepts: one
// "id=level" per line, sorted for a stable byte sequence.
func overrideLines(overrides map[string]string) string {
	if len(overrides) == 0 {
		return ""
	}
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('=')
		b.WriteString(overrides[id])
		b.WriteByte('\n')
	}
	return b.String()
}

func ruleSet(rules []Rule) map[string]bool {
	m := make(map[string]bool, len(rules))
	for _, r := range rules {
		m[r.ID] = true
	}
	return m
}

func defaultPoolSize() int { return runtime.GOMAXPROCS(0) }
