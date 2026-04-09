package vastlint_test

import (
	"strings"
	"testing"

	vastlint "github.com/aleksUIX/vastlint-go"
)

// ── Fixtures ──────────────────────────────────────────────────────────────────

// A structurally valid VAST 4.2 wrapper tag. Produces no errors.
const validVAST42 = `<?xml version="1.0" encoding="UTF-8"?>
<VAST version="4.2">
  <Ad id="1">
    <Wrapper>
      <AdSystem>Test AdServer</AdSystem>
      <VASTAdTagURI><![CDATA[https://example.com/vast.xml]]></VASTAdTagURI>
      <Impression><![CDATA[https://example.com/impression]]></Impression>
      <Creatives>
        <Creative>
          <Linear>
            <VideoClicks>
              <ClickThrough><![CDATA[https://example.com/click]]></ClickThrough>
              <ClickTracking><![CDATA[https://example.com/clicktrack]]></ClickTracking>
            </VideoClicks>
          </Linear>
        </Creative>
      </Creatives>
    </Wrapper>
  </Ad>
</VAST>`

// Missing AdSystem and Impression — must produce errors.
const invalidVAST = `<VAST version="4.2"><Ad><InLine></InLine></Ad></VAST>`

// Deliberately malformed XML.
const malformedXML = `<VAST version="4.2"><Ad><unclosed>`

// ── Validate ──────────────────────────────────────────────────────────────────

func TestValidate_ValidTag_NoErrors(t *testing.T) {
	result, err := vastlint.Validate(validVAST42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid=true, got false; errors: %d", result.Summary.Errors)
		for _, issue := range result.Issues {
			if issue.Severity == "error" {
				t.Logf("  [error] %s: %s", issue.ID, issue.Message)
			}
		}
	}
	if result.Summary.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Summary.Errors)
	}
}

func TestValidate_InvalidTag_HasErrors(t *testing.T) {
	result, err := vastlint.Validate(invalidVAST)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected valid=false for tag missing required fields")
	}
	if result.Summary.Errors == 0 {
		t.Error("expected at least one error")
	}
}

func TestValidate_MalformedXML_HasErrors(t *testing.T) {
	result, err := vastlint.Validate(malformedXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected valid=false for malformed XML")
	}
	if result.Summary.Errors == 0 {
		t.Error("expected at least one error for malformed XML")
	}
}

func TestValidate_EmptyString_ReturnsError(t *testing.T) {
	_, err := vastlint.Validate("")
	if err == nil {
		t.Error("expected non-nil error for empty input")
	}
}

// ── Result shape ──────────────────────────────────────────────────────────────

func TestValidate_ResultVersion_Populated(t *testing.T) {
	result, err := vastlint.Validate(validVAST42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version for VAST 4.2 tag")
	}
	if !strings.HasPrefix(result.Version, "4") {
		t.Errorf("expected version starting with '4', got %q", result.Version)
	}
}

func TestValidate_IssueFields_Populated(t *testing.T) {
	result, err := vastlint.Validate(invalidVAST)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	for _, issue := range result.Issues {
		if issue.ID == "" {
			t.Error("issue.ID must not be empty")
		}
		if issue.Severity == "" {
			t.Error("issue.Severity must not be empty")
		}
		switch issue.Severity {
		case "error", "warning", "info":
			// valid
		default:
			t.Errorf("unexpected severity %q", issue.Severity)
		}
		if issue.Message == "" {
			t.Error("issue.Message must not be empty")
		}
		if issue.SpecRef == "" {
			t.Errorf("issue.SpecRef must not be empty (ID: %s)", issue.ID)
		}
	}
}

func TestValidate_Summary_ConsistentWithIssues(t *testing.T) {
	result, err := vastlint.Validate(invalidVAST)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var errors, warnings, infos int
	for _, issue := range result.Issues {
		switch issue.Severity {
		case "error":
			errors++
		case "warning":
			warnings++
		case "info":
			infos++
		}
	}
	if result.Summary.Errors != errors {
		t.Errorf("Summary.Errors=%d does not match counted errors=%d", result.Summary.Errors, errors)
	}
	if result.Summary.Warnings != warnings {
		t.Errorf("Summary.Warnings=%d does not match counted warnings=%d", result.Summary.Warnings, warnings)
	}
	if result.Summary.Infos != infos {
		t.Errorf("Summary.Infos=%d does not match counted infos=%d", result.Summary.Infos, infos)
	}
	wantValid := errors == 0
	if result.Valid != wantValid {
		t.Errorf("Valid=%v but errors=%d", result.Valid, errors)
	}
}

// ── ValidateWithOptions ───────────────────────────────────────────────────────

func TestValidateWithOptions_ZeroOptions_SameAsValidate(t *testing.T) {
	r1, err1 := vastlint.Validate(validVAST42)
	r2, err2 := vastlint.ValidateWithOptions(validVAST42, vastlint.Options{})
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v / %v", err1, err2)
	}
	if r1.Valid != r2.Valid {
		t.Errorf("Valid mismatch: %v vs %v", r1.Valid, r2.Valid)
	}
	if len(r1.Issues) != len(r2.Issues) {
		t.Errorf("Issues count mismatch: %d vs %d", len(r1.Issues), len(r2.Issues))
	}
}

func TestValidateWithOptions_RuleOff_SuppressesIssue(t *testing.T) {
	// First confirm the tag produces at least one warning or info with defaults.
	baseline, err := vastlint.Validate(validVAST42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find a non-error rule ID to turn off.
	var targetID string
	for _, issue := range baseline.Issues {
		if issue.Severity == "warning" || issue.Severity == "info" {
			targetID = issue.ID
			break
		}
	}
	if targetID == "" {
		t.Skip("no warning/info issues on valid tag to suppress — skipping")
	}

	overridden, err := vastlint.ValidateWithOptions(validVAST42, vastlint.Options{
		RuleOverrides: map[string]string{targetID: "off"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, issue := range overridden.Issues {
		if issue.ID == targetID {
			t.Errorf("rule %s was set to 'off' but still appears in output", targetID)
		}
	}
}

func TestValidateWithOptions_EmptyString_ReturnsError(t *testing.T) {
	_, err := vastlint.ValidateWithOptions("", vastlint.Options{})
	if err == nil {
		t.Error("expected non-nil error for empty input")
	}
}

// ── Version ───────────────────────────────────────────────────────────────────

func TestVersion_NonEmpty(t *testing.T) {
	v := vastlint.Version()
	if v == "" {
		t.Error("Version() must not return empty string")
	}
}

func TestVersion_SemverShape(t *testing.T) {
	v := vastlint.Version()
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		t.Errorf("expected semver X.Y.Z, got %q", v)
	}
}
