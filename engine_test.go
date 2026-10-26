package vastlint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// golden.json is produced from the repository fixtures by the vastlint CLI of
// the same core version: ids with severity, validity, document type, version.
type golden struct {
	Valid        bool     `json:"valid"`
	DocumentType string   `json:"document_type"`
	Version      string   `json:"version"`
	IDs          []string `json:"ids"`
}

func loadGolden(t *testing.T) map[string]golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func idsOf(r *Result) []string {
	out := make([]string, 0, len(r.Issues))
	for _, i := range r.Issues {
		out = append(out, i.ID+"/"+i.Severity)
	}
	sort.Strings(out)
	return out
}

func newTestEngine(t *testing.T) Engine {
	t.Helper()
	e, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func TestEngineMatchesGoldenOnEveryFixture(t *testing.T) {
	e := newTestEngine(t)
	g := loadGolden(t)
	files, _ := filepath.Glob("testdata/fixtures/*.xml")
	if len(files) < 100 {
		t.Fatalf("expected the fixture set, found %d files", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		want, ok := g[name]
		if !ok {
			t.Errorf("%s: no golden", name)
			continue
		}
		xml, _ := os.ReadFile(f)
		got, err := e.Validate(context.Background(), string(xml), Options{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.Valid != want.Valid || got.DocumentType != want.DocumentType || got.Version != want.Version {
			t.Errorf("%s: valid/type/version = %v/%s/%q, want %v/%s/%q", name, got.Valid, got.DocumentType, got.Version, want.Valid, want.DocumentType, want.Version)
		}
		if ids := idsOf(got); strings.Join(ids, ",") != strings.Join(want.IDs, ",") {
			t.Errorf("%s: issue set differs\n got %v\nwant %v", name, ids, want.IDs)
		}
	}
	t.Logf("engine=%s version=%s fixtures=%d", e.Kind(), e.Version(), len(files))
}

func TestVersionAndCatalog(t *testing.T) {
	e := newTestEngine(t)
	if e.Version() != ExpectedVersion {
		t.Fatalf("version %q", e.Version())
	}
	rules := e.Rules()
	if len(rules) < 200 {
		t.Fatalf("catalog has %d rules", len(rules))
	}
	seen := map[string]bool{}
	impact := 0
	for _, r := range rules {
		if seen[r.ID] {
			t.Errorf("duplicate rule %s", r.ID)
		}
		seen[r.ID] = true
		if r.RevenueImpact {
			impact++
		}
	}
	if impact == 0 || !seen["VAST-2.0-inline-impression"] {
		t.Errorf("catalog content looks wrong: impact=%d", impact)
	}
}

const invalidVAST = `<VAST version="4.2"><Ad><InLine></InLine></Ad></VAST>`

func TestOverridesApplyAndUnknownOnesAreRefused(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	base, err := e.Validate(ctx, invalidVAST, Options{})
	if err != nil || base.Valid || base.InputStatus != "supported" {
		t.Fatalf("baseline: %v valid=%v status=%s", err, base.Valid, base.InputStatus)
	}
	off, err := e.Validate(ctx, invalidVAST, Options{RuleOverrides: map[string]string{"VAST-2.0-inline-impression": "off"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range off.Issues {
		if i.ID == "VAST-2.0-inline-impression" {
			t.Fatal("override did not apply")
		}
	}
	for _, bad := range []map[string]string{{"not-a-rule": "off"}, {"VAST-2.0-inline-impression": "loud"}} {
		if _, err := e.Validate(ctx, invalidVAST, Options{RuleOverrides: bad}); !errors.Is(err, ErrUnknownRule) {
			t.Errorf("overrides %v: got %v, want ErrUnknownRule", bad, err)
		}
	}
}

func TestInputStatusAndInputGuards(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	cases := map[string]string{
		`<VAST version="4.2"><Ad>`:          "malformed",
		`<html><body/></html>`:              "unsupported",
		`<VMAP version="1.0"/>`:             "supported",
		`<DAAST version="1.1"/>`:            "supported",
		`<!DOCTYPE VAST><VAST/>`:            "malformed",
		string(rune(0xFEFF)) + invalidVAST:  "supported",
		"<VAST version=\"4.2\">\x07</VAST>": "malformed",
	}
	for xml, want := range cases {
		r, err := e.Validate(ctx, xml, Options{})
		if err != nil {
			t.Errorf("%q: %v", xml[:min(20, len(xml))], err)
			continue
		}
		if r.InputStatus != want {
			t.Errorf("%q: input_status=%s want %s", xml[:min(20, len(xml))], r.InputStatus, want)
		}
	}
	if _, err := e.Validate(ctx, "", Options{}); !errors.Is(err, ErrEmptyInput) {
		t.Errorf("empty: %v", err)
	}
	small, err := New(ctx, Config{MaxInputBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	if _, err := small.Validate(ctx, invalidVAST, Options{}); !errors.Is(err, ErrInputTooLarge) {
		t.Errorf("too large: %v", err)
	}
	// Non-UTF-8 bytes are an engine-level refusal, never a crash.
	if _, err := e.Validate(ctx, "\xff\xfe<VAST/>", Options{}); !errors.Is(err, ErrEngine) {
		t.Errorf("non-utf8: %v", err)
	}
}

func TestConcurrentCallersGetIdenticalResults(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	xml, _ := os.ReadFile("testdata/fixtures/warn_publica_large.xml")
	ref, err := e.Validate(ctx, string(xml), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(idsOf(ref), ",")
	var wg sync.WaitGroup
	bad := make(chan string, 64)
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				r, err := e.Validate(ctx, string(xml), Options{})
				if err != nil || strings.Join(idsOf(r), ",") != want {
					bad <- "diverged"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	if len(bad) != 0 {
		t.Fatalf("%d workers diverged", len(bad))
	}
}

func TestDeadlineIsHonoredAndEngineRecovers(t *testing.T) {
	e := newTestEngine(t)
	xml, _ := os.ReadFile("testdata/fixtures/warn_publica_large.xml")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	_, err := e.Validate(ctx, string(xml), Options{})
	if err == nil && e.Kind() == "wasm" {
		t.Fatal("expected an error from an expired context on the wasm engine")
	}
	// The engine must still work afterwards.
	r, err := e.Validate(context.Background(), string(xml), Options{})
	if err != nil || r == nil {
		t.Fatalf("engine did not recover: %v", err)
	}
}

func TestOversizeDocumentRecyclesInstance(t *testing.T) {
	e, err := New(context.Background(), Config{MaxInputBytes: 5 << 20, MaxInstanceMemory: 8 << 20, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	big := "<VAST version=\"4.2\">" + strings.Repeat(" ", 4<<20+16) + "</VAST>"
	r, err := e.Validate(context.Background(), big, Options{})
	if err != nil || r.InputStatus != "malformed" {
		t.Fatalf("oversize: %v status=%v", err, r)
	}
	// Second call must work on the recycled instance.
	if _, err := e.Validate(context.Background(), invalidVAST, Options{}); err != nil {
		t.Fatal(err)
	}
}

// TestCloseRacesValidate closes the engine while callers are mid-flight. No
// call may panic; after Close every call reports an error.
func TestCloseRacesValidate(t *testing.T) {
	ctx := context.Background()
	doc, err := os.ReadFile(filepath.Join("testdata", "fixtures", "err_bad_duration.xml"))
	if err != nil {
		t.Fatal(err)
	}
	smallFixture := string(doc)
	for round := 0; round < 5; round++ {
		e, err := New(ctx, Config{PoolSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 50; i++ {
					_, _ = e.Validate(ctx, smallFixture, Options{})
				}
			}()
		}
		close(start)
		time.Sleep(time.Duration(round) * time.Millisecond)
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if _, err := e.Validate(ctx, smallFixture, Options{}); err == nil {
			t.Fatal("Validate succeeded after Close")
		}
		if err := e.Close(); err != nil {
			t.Fatal("second Close:", err)
		}
	}
}
