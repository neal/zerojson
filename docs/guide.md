# User guide

[Overview](../README.md) · [Compatibility](compatibility.md) · [Go reference](https://pkg.go.dev/github.com/neal/zerojson)

This guide covers generation and day-to-day use. See [Compatibility](compatibility.md)
for behavioral differences and ownership rules.

- [Generation and regeneration](#generation-and-regeneration)
- [Fields and tags](#fields-and-tags) and [supported shapes](#supported-shapes)
- [Reusable decoders](#reusable-decoders) and [projection](#projection--passthrough-decoding)
- [Custom codecs](#appender-and-reader)
- [Generated tests](#generated-differential-tests--tests)

## Generation and regeneration

Start with the [quick start](../README.md#quick-start) to install the runtime
and pin the generator in your module. Add one directive to a source file in
the package containing your types:

```go
//go:generate go tool zerojsongen -types Event
```

`-types` is required: list the roots you want to encode and decode, separated
by commas (for example, `-types Event,Record`). Reachable same-package structs
are discovered automatically; unrelated types are not selected.

The defaults are `-dir .` and `-out zerojson_gen.go`. Go runs each generation
directive from its source file's directory. From your module root, run:

```sh
go generate ./...
go test ./...
```

Commit the generated files alongside your types, and regenerate after schema
or generator changes. Keep the runtime and generator on compatible versions;
upgrading the tool does not automatically regenerate existing codecs.

Optional flags add [reusable decoders](#reusable-decoders) (`-pool`),
[differential tests](#generated-differential-tests--tests) (`-tests`), or
[stdlib wrappers](#compatibility-wrappers) (`-compat`). Use
`go tool zerojsongen -help` for the full flag reference.

Use one codec output and one generated fuzz file per package. Add roots to
the same `-types` list instead of generating separate files that duplicate
shared helpers. `-out` and `-tests` paths are relative to `-dir` unless
absolute. The test output must be a distinct `_test.go` file, and `-tests`
cannot be combined with `-compat`. Once a package has a generated fuzz file,
keep passing `-tests` on every run (or delete the file): the generator
refuses to regenerate the codec while leaving that file to go stale.

Hand-written custom methods are preserved during auto-discovery. Only methods
from recognized zerojsongen/easyjson output are replaceable; an arbitrary
`Code generated` header is insufficient. Explicit roots with hand-written
JSON/Text methods are refused; remove those methods to generate a codec, or
leave the type out of the explicit roots to retain its custom behavior.

The loader overlays stale generated method bodies while loading the package,
preserving declarations needed by caller code where possible, and prunes the
declarations a renamed type leaves dangling. This lets field edits and type
renames regenerate even if the old output no longer compiles. Regeneration
does not repair hand-written code that names a type which no longer exists
(a `-pool` Decoder for a root you renamed, say): generation succeeds and the
following build reports that reference. Unrelated package errors still
require manual repair.

Generated declarations must not collide with hand-written ones: a type named
like a `-pool` Decoder (`EventDecoder`), or a hand-written
`MarshalEasyJSON` on a type `-compat-easyjson` would wrap, is refused with
its location rather than written into a file that cannot compile.

The generator refuses to overwrite a file without a recognized codec header
before the package clause; `-force` overrides that protection. It cannot detect
a field added without regeneration when the stale codec still compiles. Run
generation as part of your build and check for drift, as this repository's CI
does.

## Fields and tags

- `json` accepts a name, `-`, `omitempty`, `omitzero`, and `string`.
  Unknown options are generation errors. `zerojson:"intern"` opts string/enum
  fields or map keys into the bounded interner; unknown options there also error.
- `omitempty` uses underlying-kind emptiness: nil pointers/interfaces,
  zero scalars, and length-zero strings/slices/maps/arrays. Struct values
  are never empty, including struct-backed custom types.
- `omitzero` honors `IsZero() bool` where available; otherwise it uses scalar
  zero or nil for supported reference kinds. A non-nil empty container is
  empty but not zero. Custom struct/array values require `IsZero`, while the
  built-in UUID path compares against its zero array. Value-typed
  `zerojson.Raw` with `omitzero` is refused; use `omitempty` or a pointer.
  When both omission options are present, either condition can omit the field.
- `,string` is supported for int/uint fields, including named types and
  pointers. It requires a quoted number; quoted `"null"` leaves a scalar
  unchanged or clears a pointer. Other kinds are refused.
- Ordinary numeric fields require bare tokens. Quoted numbers, leading `+`,
  `.5`, `1.`, and range overflows are rejected; leading zeros remain a
  documented leniency. `json.Number` preserves its literal and checks number
  grammar on encode/decode, accepting quoted numeric input like classic stdlib.
- Invalid JSON tag names fall back to the Go field name under classic-v1 rules.
  The v2-backed stdlib differs; see [toolchain compatibility](compatibility.md#which-encodingjson-go-127-and-later).

## Supported shapes

- Direct leaves: strings, bools, integer and float types (including named
  scalars), `time.Time`, `github.com/google/uuid.UUID`, `json.Number`, and
  registered custom leaves. `uintptr` is refused.
- Named string enums decode known constants without copying. Unknown values
  are copied unless the field opts into interning. The enum path also works
  in slice elements and map values.
- A named int64 with JSON methods can opt into quoted-decimal encoding with
  an exact value-receiver `ZeroJSONQuotedInt64()` marker. Its direct reader
  accepts bare or quoted integers, including `+` only inside quotes.
  Without the marker, its JSON methods define the wire format.
- Same-package nested structs are auto-discovered; list only roots in `-types`.
  Supported containers include slices of leaves, `[]T`/`[]*T` structs,
  string-keyed maps of supported leaves/structs, and pointers to slices/maps.
  Map values cannot be pointers. Slice elements may be pointers only for
  struct, Marshaler, and TextMarshaler kinds.
- `[]byte` and named byte slices encode as base64 and decode from base64 or
  arrays of byte values. This includes defined uint8 elements without custom
  methods, fields, pointer fields, slice elements, and map values.
  `json.RawMessage` and `zerojson.Raw` retain their custom JSON representation.
- A root may be a struct, a named slice, or a named string-keyed map; bare
  byte-slice roots are refused. Nil container roots encode as `null`; non-nil
  empty roots encode as `[]`/`{}`. Top-level `null` leaves struct roots
  unchanged and clears container roots. Trailing data is rejected.
- JSON/Text method pairs are delegated to. Signatures, not method names alone,
  determine interface satisfaction. Incomplete pairs are refused unless another
  supported codec takes precedence (such as an Appender/Reader pair).
- `any` fields/elements/values encode through stdlib. Fresh interfaces decode
  with zerojson's generic readers. An existing interface holding a non-nil
  concrete pointer decodes through stdlib into that receiver.
- Type aliases are resolved while preserving usable source spellings.
  Generic generated types and recursive generated graphs are refused.
  External generic types with supported custom methods can be delegated to.

## Embedding and package boundaries

Untagged embedded value structs promote fields, including exported fields of
same-package unexported embeds. A named JSON tag instead nests the embed;
option-only tags retain promotion. Field dominance follows classic stdlib:
the shallowest candidate wins; at equal depth, a single explicitly tagged
candidate wins. An otherwise ambiguous tie is refused rather than silently
dropping the field. Promoted fields retain their declaration position.

Embedded pointers and embeds with JSON/Text or Appender/Reader methods are
refused. Cross-package unexported paths or type names that generated code
cannot legally access are also refused. An exported alias, a wrapper, or
generation in the declaring package can resolve that boundary.

Imports receive collision-safe aliases. The runtime qualifier `zerojson` is
reserved; a target package declaring that name at package scope is refused.

## Reusable decoders

`-pool` emits a `<Type>Decoder` per listed type that reuses one arena
across decodes, eliminating the per-decode arena allocation. Combined
with `DecodeBorrow`, a decode loop over a leaf-only shape allocates
**zero** bytes (fields that delegate to user methods, `any`, `[]byte`,
and maps of structs still allocate on their own):

```go
var d models.EventDecoder // pool one per goroutine, or via sync.Pool
for _, msg := range batch {
    var e models.Event
    if err := d.DecodeBorrow(msg, &e); err != nil {
        return err
    }
    apply(&e) // must finish before the next Decode reuses the arena
}
```

Lifetime: the decoded value holds pointers into the Decoder's arena (and,
in borrow mode, into `msg`), so it is invalidated by the next
`Decode`/`DecodeBorrow` on that Decoder. Finish with — or copy out of —
one result before decoding the next.

Slice and map fields also alias the Decoder's arena (a persistent
per-field cell, resliced/reclaimed across decodes so their backing array
or table is reused instead of reallocated — the same lifetime rule
applies to them, not just pointer fields): don't retain `e.Items` or
`e.Tags` past the next `Decode` either. Value-typed fields with no
pointers, slices, or maps in their tree are unaffected (copies, as
always).

Memory: the arena keeps the high-water mark of the largest message the
Decoder has seen and never shrinks (see
[resource considerations](compatibility.md#resource-considerations)), so a
`sync.Pool` of Decoders that each met one outlier pins that many peaks.
Bound message size, or drop a Decoder after an outlier.

## Projection / passthrough decoding

Declare only the fields you need. Unknown fields are grammar-checked and
skipped; `zerojson.Raw` captures a value without materializing its contents:

```go
type Route struct {
    Table    string       `json:"table,omitempty"`
    Metadata zerojson.Raw `json:"metadata,omitempty"`
    Origin   string       `json:"origin,omitempty"`
}
```

A Raw value retains its exact JSON span, copied by default or aliased in borrow
mode. Generated encoding validates non-empty Raw values and appends them
verbatim. Nil and empty Raw values encode as `null`; unlike an empty non-nil
`json.RawMessage`, they do not cause an encode error.

JSON `null` stores the literal bytes in a value-typed Raw field and clears a
`*zerojson.Raw` field. Raw also implements `MarshalJSON` and `UnmarshalJSON`
for stdlib interoperability. Its `UnmarshalJSON` copies into fresh storage,
so reusing a previously borrowed Raw cannot overwrite the old input buffer.

Declare projection fields in their source-wire order to keep eligible inputs
on the ordered path. Benchmark your own shape: skipping scalar fields is not
necessarily cheaper than decoding them.

## Ordered decode

For eligible structs, the decoder first matches compact keys in declaration
order using fixed-width little-endian comparisons. Missing fields and unknown
members are supported; unknown values are grammar-checked without being
materialized.

Out-of-order or duplicate keys, escaped keys, and syntax the matcher cannot
consume cause a rewind to the keyed decoder (a rewind does not cost a
pooled Decoder its reuse: members the fast path already filled are
re-read into the same arena cells). **A value or grammar error is
final; it does not trigger a second parse.** The two paths share field readers
and are compared by differential tests.

Schemas with reachable JSON/Text/Appender decode callbacks use the keyed
decoder directly to avoid replaying side effects. Reused targets with existing
direct pointers or non-nil interface fields also bypass speculation. Registered
`-leaf` readers remain eligible and must tolerate replay (see
[custom leaf types](#custom-leaf-types)).

Ordered decoding is enabled by default. Use `-no-ordered` to disable it for
comparison. Field names requiring escapes or non-ASCII literals automatically
disable the ordered path for the affected schema.

## Custom leaf types

The core runtime has no decimal, UUID, or any other external dependency.
Leaf types that format as a single JSON token can join the fast path two
ways: register with the generator (`-leaf`, below — no code changes to
the type itself, works for types you don't own), or implement the
`Appender`/`Reader` interface pair directly on the type (next section —
no generator flag needed, works best for types you do own).

`zerojson.Raw` (see "Projection / passthrough decoding" above) is always
registered — it's a core-runtime type, not an external dependency, so
there's no opt-out flag for it. `uuid.UUID` fields get a direct fast path
too, but through neither mechanism: the core exposes `[16]byte`-typed
`ReadUUIDBytes`/`AppendUUIDBytes`, and the generator emits the cast to
`uuid.UUID` at the call site in your package (which already imports
`github.com/google/uuid` for its domain types) — so the runtime itself
never imports it.

Register any type with `-leaf` (repeatable):

```
-leaf 'yourpkg.Money=yourpkg/jpmoney.Append,yourpkg/jpmoney.Read'
```

The codec functions must have signatures:

```go
func Append(dst []byte, v *Money) []byte   // infallible; use a Marshaler for fallible types
func Read(l *zerojson.Lexer, v *Money)     // report errors via l.Fail / l.AddError
```

`Read` pulls a token with `l.ReadNumericBytes()`, `l.ReadString()`, etc.
The codec package is qualified in the generated file by its *declared*
package name (the generator lists it to find out), so a directory whose
name differs from its `package` clause works; a package whose name
collides with another import or a generated identifier is aliased.

Registered appenders must produce one valid JSON value; their output is not
automatically validated (the built-in Raw leaf uses a checked path).
For a non-pointer registered leaf, JSON `null` leaves the value unchanged and
does not call the leaf reader or its `UnmarshalJSON`. Pointer leaves clear to
nil. The built-in Raw leaf is the exception: it stores the literal null bytes.
Registered readers must consume one value, report errors through the Lexer,
and be safe to replay: ordered decoding can invoke them again after a
structural fallback. Do not use external side effects or rely on exactly-once
invocation; use `Appender`/`Reader` or JSON/Text methods for that behavior.

## Appender and Reader

For types you own, implement [Appender](https://pkg.go.dev/github.com/neal/zerojson#Appender)
and [Reader](https://pkg.go.dev/github.com/neal/zerojson#Reader) instead of
registering free leaf functions. The generator gives this pair precedence over
JSON/Text methods for fields.

Generated types also implement the pair, enabling fields to compose across
packages. Same-package generated structs share the parent's arena directly.
For slice elements and map values, Appender types must also provide a
JSON method pair (for example through `-compat`); otherwise that shape is refused.

Appenders must preserve the output prefix and produce one valid JSON value.
Readers consume one value and report errors through the Lexer. These callbacks
disable ordered speculation, unlike registered leaf functions. A value-typed
Appender with `UnmarshalJSON` receives JSON null through that method.

For example, this type represents a count as a JSON integer:

```go
package events

import (
    "strconv"

    "github.com/neal/zerojson"
)

//go:generate go tool zerojsongen -types Event

type Count int64

func (c Count) AppendZJSON(dst []byte) ([]byte, error) {
    return strconv.AppendInt(dst, int64(c), 10), nil
}

func (c *Count) ReadZJSON(l *zerojson.Lexer) {
    n := l.ReadInt64()
    if l.Err() == nil {
        *c = Count(n)
    }
}

type Event struct {
    Count Count `json:"count"`
}
```

No `-leaf` flag is needed. This example uses an ordinary integer wire format;
a plain named integer would already be supported without custom methods.
Use the interface pair when your type needs its own representation or checks.
For fallible readers, report custom validation failures with `l.AddError(err)`.
JSON null leaves this scalar field unchanged because it has no
`UnmarshalJSON` method.

## Compatibility wrappers

`-compat` emits a value-receiver `MarshalJSON` wrapper and a pointer-receiver
`UnmarshalJSON` wrapper so both value- and pointer-based `encoding/json` call
sites (including non-addressable map values) route through zerojson with no
code changes. `-compat-easyjson`
additionally emits `MarshalEasyJSON`/`UnmarshalEasyJSON`, so
`easyjson.Marshal(v)` call
sites migrate too — and partial migration stays safe (a not-yet-migrated
easyjson type that references a migrated one still links).

These wrappers **replace** the type's existing `MarshalJSON`, so remove
its easyjson generation in the same change. Cutover per type:

1. Delete the `//easyjson:json` directive (or the type from easyjson's
   `-types`) and its generated methods.
2. Add the type to zerojson's `-types` with `-compat`.

## Generated differential tests (`-tests`)

Add a test output to the same generation directive:

```go
//go:generate go tool zerojsongen -types Event -tests zerojson_gen_fuzz_test.go
```

Generate and replay the checked-in seeds with ordinary tests:

```sh
go generate ./...
go test ./...
```

To search for new cases, run one fuzz target from the package containing the
generated tests:

```sh
go test -run '^$' -fuzz '^FuzzEventZJSON$' -fuzztime 30s -parallel 1
```

Normal `go test` replays seeds; it does not start a timed fuzz campaign.
Go saves reproducible failures under `testdata/fuzz/<target>`; keep those
regression inputs in version control after fixing the bug. Add `-pool` to
also generate `FuzzEventZJSONSequence`, which can be run separately.

`-tests <path>` emits schema-seeded differential fuzz tests. They compare
stdlib and zerojson acceptance, raw Go values where custom codec state permits,
normalized re-encoding, valid output, and ordered/fallback agreement.
Documented differences, such as quoted-int64 marker nulls and the v2-backed
`,string` leading-plus behavior, have targeted exemptions.

With `-pool`, it also emits a four-operation sequence fuzzer per explicit root.
Operations share one decoder and mix copy/borrow modes, including invalid input
and input-buffer scribbling.

**Choose tests or compatibility wrappers for a generated package.** `-tests`
cannot be combined with `-compat` or `-compat-easyjson`: the wrappers would make
the stdlib oracle call zerojson itself. For a wrapper-based integration, keep
independent fixture types in a separate test package without those wrappers,
or write migration tests against the incumbent codec before adding wrappers.
Do not alternate generation flags over the same files and assume the oracle
remains independent.

These generated tests are a starting point; add examples for your custom
codecs and lifetime requirements.

For a type whose graph carries an **invalid tag name** the two
implementations disagree on (see [toolchain differences](compatibility.md#which-encodingjson-go-127-and-later)), the two write
different members, so on a v2-backed toolchain the stdlib comparisons —
and only those — are dropped for that type: the target still runs
zerojson's own invariants (no panics on arbitrary input, valid output,
round-trip, and agreement between the ordered fast path and the fallback
decoder).
