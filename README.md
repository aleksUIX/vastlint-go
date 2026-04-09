# vastlint-go

Go bindings for [vastlint](https://github.com/aleksUIX/vastlint) — a high-performance VAST XML linter implementing 108 rules across IAB VAST 2.0 through 4.3.

No Rust toolchain required. Prebuilt static libraries are included for all supported platforms.

---

## Install

```sh
go get github.com/aleksUIX/vastlint-go
```

Supported platforms:

| OS    | Architecture |
|-------|-------------|
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

---

## Usage

```go
import vastlint "github.com/aleksUIX/vastlint-go"

// Basic validation
result, err := vastlint.Validate(xmlString)
if err != nil {
    log.Fatal(err)
}

if result.Valid {
    fmt.Println("tag is clean")
} else {
    for _, issue := range result.Issues {
        fmt.Printf("[%s] %s (%s)\n", issue.Severity, issue.Message, issue.ID)
    }
}

// With options
result, err = vastlint.ValidateWithOptions(xmlString, vastlint.Options{
    WrapperDepth:    2,
    MaxWrapperDepth: 5,
    RuleOverrides: map[string]string{
        "VAST-2.0-mediafile-https":            "error",
        "VAST-4.1-mezzanine-recommended":      "off",
    },
})

// Library version
fmt.Println(vastlint.Version())
```

### Result shape

```go
type Result struct {
    Version string   // detected VAST version, e.g. "4.2", or "" if unknown
    Issues  []Issue
    Summary Summary
    Valid   bool     // true when Summary.Errors == 0
}

type Issue struct {
    ID       string // e.g. "VAST-4.2-3.4.1"
    Severity string // "error", "warning", or "info"
    Message  string
    Path     string // XPath-like location, e.g. "VAST/Ad/InLine/Creatives", or ""
    SpecRef  string // e.g. "IAB VAST 4.2 §3.4.1"
}

type Summary struct {
    Errors   int
    Warnings int
    Infos    int
}
```

---

## Updating the prebuilt libraries

The `.a` static libraries in `libs/` are built from
[vastlint-ffi](https://github.com/aleksUIX/vastlint/tree/main/crates/vastlint-ffi)
and committed to this repo so callers need no Rust toolchain.

To update them after a new `vastlint` release:

```sh
./scripts/fetch-libs.sh v0.2.0
```

This downloads the prebuilt tarballs from the GitHub Release and unpacks them
into the correct `libs/` subdirectories.

To build from source instead:

```sh
# In the vastlint monorepo:
cargo build --release -p vastlint-ffi

# Then copy the output into this repo:
cp path/to/vastlint/target/release/libvastlint_ffi.a \
   libs/darwin_arm64/libvastlint.a   # adjust platform dir as needed
cp path/to/vastlint/crates/vastlint-ffi/vastlint.h vastlint.h
```

---

## License

Apache 2.0 — same as [vastlint-core](https://github.com/aleksUIX/vastlint).
