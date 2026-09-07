# zerojson

[![Go Reference](https://pkg.go.dev/badge/github.com/neal/zerojson.svg)](https://pkg.go.dev/github.com/neal/zerojson)

High-performance JSON for Go, generated for your types.

zerojson generates specialized encoders and decoders with reusable buffers,
optional zero-copy decoding, and zero runtime dependencies.

- **Direct encoding:** append into your own buffer without intermediate JSON.
- **Reusable decoding:** optional decoders reuse storage across messages.
- **Schema-aware tests:** generate differential fuzz tests alongside your codecs.

Requires Go 1.26 or later.

## Quick start

Add the runtime and pin the generator as a development tool:

```sh
go get github.com/neal/zerojson@latest
go get -tool github.com/neal/zerojson/cmd/zerojsongen@latest
```

Define your types in a Go package:

```go
package events

//go:generate go tool zerojsongen -types Event

type Event struct {
    ID     string `json:"id"`
    Symbol string `json:"symbol"`
    Count  int64  `json:"count"`
}
```

Run `go generate ./...`, then use the generated methods:

```go
event := events.Event{ID: "evt-1", Symbol: "AAPL", Count: 10}

data, err := event.AppendZJSON(nil)
if err != nil {
    return err
}

var decoded events.Event
if err := decoded.UnmarshalZJSON(data); err != nil {
    return err
}
```

Reuse a buffer with `event.AppendZJSON(buf[:0])`. Discard the partial encoding
if it returns an error. The generator supports nested structs, supported
slices and string-keyed maps, standard JSON tags, and custom codecs;
[see the supported shapes](docs/guide.md#supported-shapes).

## Faster decode loops

Generate with `-pool` to get an `EventDecoder`:

```go
//go:generate go tool zerojsongen -types Event -pool
```

```go
var decoder events.EventDecoder
for _, data := range messages {
    var event events.Event
    if err := decoder.DecodeBorrow(data, &event); err != nil {
        return err
    }
    consume(event) // finish before reusing the decoder or input buffer
}
```

Choose the mode that fits your ownership requirements:

| Mode | Input ownership | Decoder storage |
|---|---|---|
| `UnmarshalZJSON` | Strings and Raw values are copied | Ordinary target reuse |
| `UnmarshalZJSONBorrow` | Strings and Raw values may alias input | Ordinary target reuse |
| Pooled `Decode` / `DecodeBorrow` | Copy / borrow, respectively | Target reset; previous result invalidated on the next call |

**Borrowed input must remain unchanged while any result refers to it.**
A pooled decoder is single-goroutine; finish using or copy out its result
before the next call. Custom codecs must honor their own ownership contracts.
See [reusable decoders](docs/guide.md#reusable-decoders) for details.

## Compatibility and validation

zerojson targets classic `encoding/json` semantics, but is not a universal
drop-in replacement. Notable differences include case-sensitive field matching,
unsorted map output, float notation, and slice-element reuse. Unsupported
schemas are rejected during generation.

**Decode success does not guarantee strict JSON grammar.** Ordinary value
readers deliberately accept some malformed string and number spellings.
If your boundary requires strict grammar, validate first:

```go
if err := zerojson.Valid(data); err != nil {
    return err
}
if err := event.UnmarshalZJSON(data); err != nil {
    return err
}
```

Validation checks JSON syntax, not application rules or resource limits.
Decode errors can leave the target partially changed.

Read the [compatibility contract and caveats](docs/compatibility.md) before
adopting the library. An [easyjson migration guide](docs/compatibility.md#easyjson-migration)
is available for existing easyjson users.

## Performance

Field dispatch is resolved at generation time. Direct fields avoid reflection,
leaf encoders append into the caller's buffer, and eligible decoders match
compact keys in declaration order before falling back to general lookup.

Leaf-only shapes can encode into a sufficiently sized buffer and decode in
pooled borrow mode with zero allocations. Custom methods, buffer growth, and
other shapes may allocate; measure your actual types and workload.

[Benchmark commands](conformance/README.md#benchmarking) cover encoding,
decoding, allocation counts, and interning. Previous measurements are retained
as [historical results](conformance/BENCHMARKS.md), not current performance claims.

zerojson works on complete, in-memory messages with known schemas. For dynamic
JSON or streaming input, use a general-purpose JSON library.

## Documentation

- [Go reference](https://pkg.go.dev/github.com/neal/zerojson) — runtime APIs.
- [User guide](docs/guide.md) — tags, type support, generation, pooling, and custom codecs.
- [Compatibility](docs/compatibility.md) — semantics, toolchain differences, and caveats.
- [Contributing](CONTRIBUTING.md) — repository layout and development checks.
- [Testing](conformance/README.md) — differential tests, fuzzing, and reproducible campaigns.

## License

[Apache License 2.0](LICENSE). The vendored JSONTestSuite corpus retains its
MIT license; see [third-party licenses](THIRD_PARTY_LICENSES.md).

Copyright 2026 Neal Patel.
