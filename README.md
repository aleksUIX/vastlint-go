# vastlint-go

Created by [Aleksander Sekowski](https://github.com/aleksUIX).

In-process VAST, VMAP and DAAST validation for Go, backed by the [vastlint](https://github.com/aleksUIX/vastlint) rule engine. One `Engine` interface, two ways to run the engine, identical findings from both.

```sh
go get github.com/aleksUIX/vastlint-go
```

```go
engine, err := vastlint.New(ctx, vastlint.Config{})
defer engine.Close()

result, err := engine.Validate(ctx, xml, vastlint.Options{})
for _, issue := range result.Issues {
    fmt.Println(issue.ID, issue.Severity, issue.Message)
}
```

**Website and docs:** [vastlint.org](https://vastlint.org) · **Rule reference:** [vastlint.org/docs/rules](https://vastlint.org/docs/rules) · **Methodology:** [how rules are derived](https://vastlint.org/docs/methodology/)

## Two engines

| | Default build | `go build -tags vastlint_cgo` |
|---|---|---|
| What runs | The engine compiled to WebAssembly, executed by [wazero](https://github.com/tetratelabs/wazero), a pure Go runtime | The native library `libvastlint`, linked through cgo |
| Install | Nothing; `CGO_ENABLED=0` works | `libvastlint` and `vastlint.h` on the build host, found with `pkg-config vastlint` |
| Cost per document | About three times native | Native, plus about 10 µs per call |
| Concurrency | Pool of `PoolSize` instances | `PoolSize` worker threads pinned with `LockOSThread` |

Both engines pass the same fixture suite and produce byte-identical issue sets. Pick the default for portability and the native build when video bid volume makes the 3x matter.

The wasm module is embedded in this package and pinned by SHA-256; `New` refuses a module or library whose version is not `ExpectedVersion`.

## Why the native engine pins threads

Go runs a cgo call on whichever OS thread the calling goroutine holds, and a busy server rotates through hundreds of threads. Every thread that enters the library's allocator leaves a per-thread heap behind. Measured in Prebid Server under load: 900 threads and 600 to 900 MB resident in allocator arenas. Running all native calls on `PoolSize` pinned threads brought that to about 200 MB. The two channel hops cost about 10 µs per call.

## Configuration

```go
vastlint.Config{
    MaxInputBytes:     1 << 20,  // reject larger documents before the engine sees them
    PoolSize:          0,        // 0 = runtime.GOMAXPROCS(0)
    MaxInstanceMemory: 16 << 20, // recycle a wasm instance that grew past this
}
```

`Options.RuleOverrides` maps rule ids to `error`, `warning`, `info` or `off`. Unknown ids or levels return `ErrUnknownRule` before any call; the catalog is available through `Engine.Rules()`.

## Results

`Result` carries `DocumentType`, `Version`, `InputStatus` (`supported`, `unsupported` or `malformed`, decided independently of overrides), `Issues` with rule id, severity, message, path, spec reference, line and column, a `Summary` and `Valid`.

Errors worth handling: `ErrEmptyInput`, `ErrInputTooLarge`, `ErrUnknownRule`, and `ErrEngine` for a trap or library failure, which means the document was not validated and should be counted as an engine failure rather than as invalid.

## Building the native library

From the vastlint repository:

```sh
cargo build --release -p vastlint-ffi
# target/release/libvastlint_ffi.a, crates/vastlint-ffi/vastlint.h
```

Install the archive as `libvastlint.a` with a `vastlint.pc` that points at it, or set `CGO_CFLAGS` and `CGO_LDFLAGS`. Hosts loading the library from a Go process should consider `--no-default-features` on the FFI crate to use the system allocator instead of mimalloc.

## Development

```sh
go test ./...                                      # wasm engine
PKG_CONFIG_PATH=... go test -tags vastlint_cgo ./...  # native engine
go test -bench . -benchmem                         # either
```

`testdata/fixtures` holds the engine's fixture set and `testdata/golden.json` the expected findings produced by the vastlint CLI of the same version. Both engines must match it exactly.
