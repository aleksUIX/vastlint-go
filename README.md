# vastlint-go

**In-process VAST XML validation for Go ad servers.** Drop it into your bidder, SSP, or ad quality pipeline and start catching broken tags before they cost you revenue.

Backed by [vastlint](https://github.com/aleksUIX/vastlint) — 108 rules across IAB VAST 2.0 through 4.3, written in Rust, called directly from Go via CGo. No sidecar, no network hop, no Rust toolchain required.

**Website & docs:** [vastlint.org](https://vastlint.org) · **Rule reference:** [vastlint.org/docs/rules](https://vastlint.org/docs/rules) · **Web validator:** [vastlint.org/validate](https://vastlint.org/validate)

```sh
go get github.com/aleksUIX/vastlint-go
```

---

## Why bother validating VAST?

A broken VAST tag doesn't just fail silently — it burns an impression. The player loads, the auction clears, the publisher gets charged, and the viewer sees nothing. Common causes:

- Missing required fields (`<Impression>`, `<Duration>`, `<MediaFile>`) → player error, no fill
- Malformed XML → parser crash, blank ad slot
- Wrong VAST version declared → player skips the creative
- Wrapper chains that exceed player depth limits → timeout, no ad
- HTTP media URLs in HTTPS contexts → mixed-content block, no playback

Every one of these is detectable in ~260µs before the impression fires.

---

## Use cases

### 1. Pre-bid creative rejection with waterfall fallback

Validate the winning bid's VAST before accepting it. If it's broken, fall to the next eligible bid — the auction already ran, so there's no added latency to the viewer.

```go
func selectWinningBid(rankedBids []Bid) (*Bid, error) {
    for _, bid := range rankedBids {
        result, _ := vastlint.Validate(bid.VastXML)
        if result.Valid {
            return &bid, nil
        }
        log.Warnf("bid %s rejected: %d errors — %s",
            bid.ID, result.Summary.Errors, result.Issues[0].Message)
        reportBadCreative(bid.DemandPartner, bid.CreativeID, result)
    }
    return nil, ErrNoValidBid // serve house ad
}
```

**Revenue impact:** Every bad creative that reaches the player is a wasted impression. At 5% invalid rate and $5 CPM, that's $0.25 wasted per 1,000 auctions — caught here, recovered by serving the next valid bid.

---

### 2. Async creative quality monitoring (zero latency impact)

Fire validation in a goroutine after the bid is sent. Collect quality signals per demand partner over time without touching your critical path.

```go
func handleBidResponse(bid Bid) {
    serveBid(bid) // fast path unaffected

    go func() {
        result, _ := vastlint.Validate(bid.VastXML)
        metrics.RecordCreativeQuality(bid.DemandPartner, result)
        if result.Summary.Errors > 0 {
            alertOps(bid.DemandPartner, result)
        }
    }()
}
```

**Revenue impact:** Identify which demand partners consistently send broken tags. Use the data in QBRs to enforce SLAs or drop chronic offenders.

---

### 3. Cache-backed validation (near-zero overhead at scale)

In programmatic, the same creative runs millions of times. Validate once, cache the result by creative ID. The hot path becomes a map lookup (~15ns vs ~260µs for validation).

```go
var creativeCache sync.Map // creativeID → *vastlint.Result

func validateCreative(creativeID, vastXML string) *vastlint.Result {
    if cached, ok := creativeCache.Load(creativeID); ok {
        return cached.(*vastlint.Result)
    }
    result, _ := vastlint.Validate(vastXML)
    creativeCache.Store(creativeID, result)
    return result
}
```

At steady state, cache hit rate converges to 99%+ within seconds. Validation cost effectively disappears.

---

### 4. Wrapper depth enforcement

VAST wrapper chains that are too deep time out in players, causing blank slots. Validate each hop as you resolve the chain and reject early.

```go
func resolveWrapper(vastXML string, depth int) error {
    result, _ := vastlint.ValidateWithOptions(vastXML, vastlint.Options{
        WrapperDepth:    depth,
        MaxWrapperDepth: 4, // reject chains deeper than 4 hops
    })
    if !result.Valid {
        return fmt.Errorf("wrapper depth %d rejected: %s", depth, result.Issues[0].Message)
    }
    return nil
}
```

---

### 5. Demand partner reporting and SLA enforcement

Aggregate validation results per partner and surface them to your ops team or feed them back to demand partners directly.

```go
result, _ := vastlint.Validate(vastXML)
for _, issue := range result.Issues {
    fmt.Printf("[%s] %s  rule:%s  at:%s  ref:%s\n",
        issue.Severity,
        issue.Message,
        issue.ID,
        issue.Path,
        issue.SpecRef, // e.g. "IAB VAST 4.2 §3.4.1"
    )
}
```

Every issue includes a spec reference — so when you tell a demand partner their tags are broken, you can cite the exact IAB clause.

---

## Performance

Measured on Apple M4 (10-core) with production-realistic VAST tags (17–44 KB),
using mimalloc as the global allocator in the underlying Rust library.

| Goroutines | 17 KB tag | 44 KB tag |
|---|---|---|
| 1 | 2,480 tags/sec · 403 µs | 440 tags/sec · 2,269 µs |
| 4 | 10,181 tags/sec | 1,505 tags/sec |
| 10 | 13,558 tags/sec | 1,993 tags/sec |

A single M4 node handles **2,480 validations/sec** per goroutine on typical 17 KB tags.
With 10 goroutines you reach **13,500+/sec** — near-linear scaling limited only by CPU
core count. A typical OpenRTB bid cycle takes 100–300 ms; validation adds under 2.3 ms
even on the heaviest 44 KB tags.

---

## Install

```sh
go get github.com/aleksUIX/vastlint-go
```

Supported platforms — prebuilt static libraries included, no Rust toolchain needed:

| OS    | Architecture |
|-------|-------------|
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

---

## API reference

```go
// Validate a VAST XML string with default settings.
func Validate(xml string) (*Result, error)

// Validate with wrapper depth tracking or rule overrides.
func ValidateWithOptions(xml string, opts Options) (*Result, error)

// Returns the vastlint-core library version.
func Version() string
```

```go
type Options struct {
    WrapperDepth    int               // current depth in wrapper chain (default 0)
    MaxWrapperDepth int               // reject chains deeper than this (default 5)
    RuleOverrides   map[string]string // rule ID → "error" | "warning" | "info" | "off"
}

type Result struct {
    Version string  // detected VAST version, e.g. "4.2"
    Issues  []Issue
    Summary Summary
    Valid   bool    // true when Summary.Errors == 0
}

type Issue struct {
    ID       string // stable rule ID, e.g. "VAST-4.2-3.4.1"
    Severity string // "error", "warning", or "info"
    Message  string
    Path     string // XPath-like location, e.g. "VAST/Ad/InLine/Creatives"
    SpecRef  string // IAB spec citation, e.g. "IAB VAST 4.2 §3.4.1"
}

type Summary struct {
    Errors   int
    Warnings int
    Infos    int
}
```

### Controlling severity

Not every issue should block a bid. Filter by severity to match your policy:

```go
result, _ := vastlint.Validate(vastXML)

// Block on hard errors only — let warnings through
hasErrors := result.Summary.Errors > 0

// Or inspect individual issues
for _, issue := range result.Issues {
    if issue.Severity == "error" {
        rejectBid(issue)
    }
}
```

---

## Updating the prebuilt libraries

The `.a` static libraries in `libs/` are built from
[vastlint-ffi](https://github.com/aleksUIX/vastlint/tree/main/crates/vastlint-ffi)
and committed to this repo so callers need no Rust toolchain.

To update after a new vastlint release:

```sh
./scripts/fetch-libs.sh v0.2.3
```

---

## License

Apache 2.0 — same as [vastlint-core](https://github.com/aleksUIX/vastlint).

Need to validate a tag without writing code? Try the free web validator at [vastlint.org/validate](https://vastlint.org/validate).
