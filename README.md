# zerojson

Codegen-based JSON codec for high-throughput message shapes. It is
semantically compatible with `encoding/json` v1 — a value encodes and
decodes with the same meaning the standard library gives it — but built for
zero-alloc encoding and near-zero-alloc decoding (~2.6× faster than
`encoding/json` encoding, ~3.4× decoding; see Benchmarks). It is also a
drop-in successor to [easyjson](https://github.com/mailru/easyjson), with
byte-identical output so a migration can be verified byte-for-byte against
an existing wire format (see "easyjson migration").

**`go get github.com/neal/zerojson` pulls zero dependencies.** The
root module's `go.mod` has no `require` lines at all — the code generator's
`x/tools` dependency and the conformance suite's external test oracles live
in their own nested modules. See "Module layout" below.

## Module layout

```
zerojson/                    runtime — zero dependencies
zerojson/cmd/zerojsongen/    the generator binary — its own module (x/tools)
zerojson/conformance/        differential/byte-identity test suites — its own module
zerojson/go.work             ties all three together for in-repo development
```

Consumers don't need `go.work` — it exists only so this repo's own tests
and generator invocations resolve across the three modules without manual
`replace` directives during local development. A consumer's recipe:

```sh
go get github.com/neal/zerojson@latest
go get -tool github.com/neal/zerojson/cmd/zerojsongen@latest
```

The first command adds the zero-dependency runtime. The second records the
generator as a reproducibly pinned Go tool; its `x/tools` dependencies remain
tool-only and are not linked into your application.

## Benchmarks

The benchmark suite lives in `conformance/codec/benchmark_test.go` and runs
against both `encoding/json` (the baseline a general user starts from) and
easyjson v0.9.2 (previously the fastest library tested — ahead of sonic,
goccy, segmentio, jsoniter, and encoding/json/v2). Run it with:

```
cd conformance/codec
go test -run '^$' -bench . -benchmem
```

To compare the bounded interner with the `unique` candidate without changing
the production backend, run `scripts/benchmark-intern.sh`. It copies the same
working tree into two temporary directories, replaces only `intern.go` in the
candidate, and benchmarks hot hits, contention, cardinality, forced-GC epochs,
and generated decoding of explicitly interned strings and map keys. Raw output,
resource measurements, metadata, and `benchstat` comparisons are written to a
timestamped directory under `$TMPDIR`:

```sh
BENCH_COUNT=10 BENCH_TIME=750ms BENCH_CPUS=1,8,32 \
  scripts/benchmark-intern.sh
```

Run both backends sequentially on the same amd64 host before making a backend
decision; separate CI jobs add enough host-to-host noise to swamp small
differences. `BENCH_OUT`, `BENCH_GOGC`, and `BENCH_BACKENDS` override the output
directory, GC target, and execution order.

The numbers below were measured on an Apple M4 Pro (arm64, go 1.26.4) with
`go test -benchtime 2s -count 6`, reported as the median of six; the
benchmarks are single-threaded and the six samples spread under ~2%.
`Event` is a 35-field message deliberately mixing every field kind —
including several non-fast-path ones (Marshaler-delegated and
`interface{}`-fallback fields) that carry their own allocations — so it
shows a realistic mixed shape, not a best case. `EventLeg` is all direct
leaves (decimal, string, enum), showing the fast-path floor the zero-alloc
claims are about.

**Encode** (`Event`, 35 fields, ns/op · B/op · allocs/op — median of six):

| | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `AppendZJSON`, fresh buffer | 1798 | 3905 | 31 |
| `AppendZJSON`, reused buffer | **1478** | 584 | 22 |
| `easyjson.Marshal` | 2464 | 3290 | 49 |
| `encoding/json.Marshal` | 3810 | 2194 | 44 |

**Decode** (`Event`):

| | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `UnmarshalZJSON` (copy) | 3030 | 2403 | 54 |
| `UnmarshalZJSONBorrow` | 2976 | 2366 | 48 |
| pooled `Decode` (copy) | 2746 | 1610 | 42 |
| pooled `DecodeBorrow` | **2729** | 1612 | 36 |
| `easyjson.Unmarshal` | 3840 | 2477 | 56 |
| `encoding/json.Unmarshal` | 9265 | 3776 | 94 |

`encoding/json` reflects `Event` structurally (it has no `MarshalJSON`), so
these are the standard-library baseline a general user actually pays:
zerojson is ~2.6× faster encoding (reused buffer) and ~3.4× faster decoding
(pooled borrow). The `encoding/json` rows were re-measured on the same
machine (median of three at `-benchtime 1s`); the zerojson/easyjson rows
above reproduced within ~2%.

`Event`'s non-trivial alloc counts are its exotic coverage fields, not the
codec: Marshaler-delegated (`Tags`, `Parents`), `interface{}`-fallback
(`Bag`, `Extra`, `AnyList`), and `MarshalText` (`YMD`) fields each allocate
independently of zerojson. **`EventLeg`** (decimal, string, enum — all
direct leaves) shows the fast-path floor with none of that:

| `EventLeg` | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `AppendZJSON`, reused buffer | 22.0 | **0** | **0** |
| `UnmarshalZJSONBorrow` | 30.8 | **0** | **0** |
| pooled `DecodeBorrow` | 30.8 | **0** | **0** |

**Ordered fast path vs keyed fallback** is benchmarked on callback-free
`EventLeg` using identical bytes and a fresh arena. Callback-bearing schemas
use the keyed path directly so user decode methods are never replayed.

**Other** (median of six):

| | ns/op | throughput | allocs/op |
|---|--:|--:|--:|
| Projection (`EventProjection` over full `Event` bytes) | 771 | — | 1 |
| `Valid`, `record_insert` (golden) | 140 | 2.1 GB/s | 0 |
| `Valid`, `record_update` (golden) | 82 | 2.2 GB/s | 0 |
| `Valid`, `summary_update` (golden) | 125 | 1.9 GB/s | 0 |
| Golden batch replay (3 records: pooled borrow decode + reused-buffer re-encode) | 8800 | 82 MB/s | 193 |

The decode numbers already reflect the ordered fast path (on by default,
not a separate opt-in); the ordered-vs-fallback rows isolate what it buys.
`AllocsPerRun` budgets for the zero-alloc cases are additionally pinned as
hard test assertions in `allocation_test.go`, so a regression fails the build,
not just a benchmark trend.

## Design notes

Why it's fast, for a reader deciding whether to adopt it:

- **A compile-time schema instead of reflection.** The generator reads
  your types once, at build time, and emits ordinary Go — a straight-line
  encode function and a keyed decode function per type. There is no
  `reflect` walk, no per-value type switch, and no struct-tag parsing at
  run time: the field set, order, tags, and per-field codec are all
  resolved when the code is generated.

- **Leaf values are formatted straight into the output buffer.** Registered
  custom leaves, RFC 3339 timestamps, UUIDs, quoted int64s,
  bools, and integers append their bytes directly with digit-pair tables —
  no per-field `MarshalJSON` returning a throwaway `[]byte` that then has
  to be copied in. Strings escape through a SWAR scan that clears 16 bytes
  per step, skipping whole runs with no byte needing an escape. The encoder
  appends to a caller-provided buffer and never resets it, so a reused
  buffer makes encoding allocation-free.

- **Decoding allocates once — or not at all.** A single arena struct backs
  every pointer field of the target, so a decode is one allocation, not one
  per pointer field. A pooled `Decoder` (`-pool`) reuses that arena — plus
  a persistent cell per slice/map field — across calls, and borrow-mode
  decoding aliases the input instead of copying strings, so a steady-state
  decode loop allocates zero bytes.

- **Fixed-offset leaf parsing.** Timestamps decode by arithmetic
  (days-since-epoch + `time.Unix`) rather than the general `time.Date`
  calendar path; canonical UUIDs decode from their fixed 36-byte layout
  with a hex table, no scan; enums (named string types) switch straight to
  the declared constant with no allocation. Low-cardinality string, enum,
  or map fields tagged `zerojson:"intern"` resolve through a bounded shared
  intern table; untagged unknown enum values and arbitrary map keys are copied.

- **Speculative ordered decode, with a transparent fallback.** Because
  zerojson's own encoder (and easyjson) emit fields compact and in
  declaration order, the decoder first tries an ordered fast path that
  matches keys with fixed-width word compares instead of extracting and
  switching on the key string. On any deviation — a reordered or duplicate
  key, whitespace, an escaped key — it rewinds and retries the general
  keyed decoder, with no observable difference. Both paths run the same
  leaf readers, so they are identical by construction (pinned by a
  differential fuzzer). See "Ordered decode" below.

### Scope: what it is and isn't

zerojson is a typed struct codec for a **known, fixed schema compiled ahead
of time**. It is deliberately not:

- **a dynamic-JSON library.** For decoding arbitrary or schema-less JSON
  into `map[string]any`, or assembling JSON values on the fly, use
  `encoding/json` (or `encoding/json/v2`). zerojson does fall back to
  `encoding/json` for `interface{}` fields, but that's an escape hatch, not
  the fast path.
- **a streaming or incremental parser.** It parses a complete document held
  in memory; there is no token stream or `io.Reader`-driven decode. For
  SAX-style or very-large-document streaming, use `encoding/json`'s
  `Decoder` or `jsontext`.
- **a blanket drop-in for `encoding/json`.** It targets one specific wire
  format (byte-identical to easyjson, including HTML escaping and float
  formatting) and makes a few deliberate divergences from `encoding/json`
  v1 (see "Compatibility contract"). Adopt it where you own the format and
  need the throughput, not as a general replacement.

Giving up dynamic shapes and streaming is precisely what buys the
compile-time schema, the direct-to-buffer formatting, and the single-arena
decode.

## Ordered decode

zerojson's own encoder (and easyjson, whose bytes fill the existing
wire format) emits fields in **declaration order, compact, with omitempty
fields skipped**. The generator exploits this: for each type, an
ordered fast-path decoder is tried first, matching keys via
fixed-width little-endian word comparisons (`binary.LittleEndian.Uint64`
against a compile-time constant per field, chunked 8/4/2/1 bytes) instead of
extracting the key string and switching on it. It maintains a cursor
into the declared fields and, at each object member, scans forward from
that cursor — so a message missing some omitempty fields still matches
(this is what makes **projection structs** — declaring only the wire
fields a reader needs — fast too: an undeclared key is recognized by
name and its value skipped inline, without ever being parsed).

**It bails to the general keyed decoder — transparently, with zero
observable difference from never having tried the fast path — on any
deviation**: an out-of-order or duplicate key, non-compact syntax
(any whitespace defeats the literal match), an escaped key that might
decode to a declared name, or a value parse error (including a nested
field's own ordered decoder bailing, which propagates outward: the
*whole* top-level object restarts on the fallback, not just the nested
part). Values are decoded with the exact same Lexer functions and arena
assignments either way, so both paths are provably identical by
construction — this is checked by a differential fuzzer
(`conformance/codec`'s `FuzzOrderedVsFallback` and the `-tests`-generated
fuzzers) that decodes the same input via the public path and via the
fallback decoder called directly and requires `reflect.DeepEqual`.

Types whose reachable fields invoke user `UnmarshalJSON`, `UnmarshalText`,
or `ReadZJSON` callbacks use the keyed decoder directly. Speculatively
invoking a callback and later rewinding could replay receiver or external
side effects, which cannot be rolled back safely.

This is on by default; disable it per generate call with `-no-ordered`
(e.g. for A/B benchmarking, or if a type's field names can't be
represented as plain-ASCII literals — the generator detects this and
skips the fast path for that type automatically either way).

## Projection / passthrough decoding

Because the ordered decoder tolerates (and stays fast through) declared
fields that are a strict subsequence of the wire's fields, a struct that
declares only a handful of a large wire shape's fields — routing the
rest through unexamined via `zerojson.Raw` — decodes without ever
parsing what it doesn't need:

```go
// Route a message by table and pass its payload through unparsed.
type Route struct {
    Table    string       `json:"table,omitempty"`
    Metadata zerojson.Raw `json:"metadata,omitempty"`
    Origin   string       `json:"origin,omitempty"`
}
```

`zerojson.Raw` ([]byte) captures the exact byte span of a value —
copied, or aliased to the input in borrow mode — and re-encodes it
verbatim (or as `null` if nil), matching `encoding/json.RawMessage`'s
contract (**the caller guarantees the captured bytes are valid JSON**,
including the "empty-but-non-nil is appended as-is, even if invalid"
footgun `RawMessage` has). It's registered as a built-in leaf type, so
it's dispatched directly like any other leaf, not through the slower
`Marshaler` interface path — but as a consequence, a `null` on a
**non-pointer** `Raw` field is left untouched rather than captured as
the literal `"null"` bytes the way `json.RawMessage`'s `UnmarshalJSON`
would (matching every other value-typed custom leaf field); use a
`*zerojson.Raw` field if you need null to
clear it, which behaves exactly like every other pointer field.

Field **declaration order matters for staying on the fast path**: the
ordered matcher only tolerates fields appearing in non-decreasing wire
order, so a projection struct's fields should be declared in the same
relative order they appear on the source type's wire format — declaring
them in a different relative order still decodes correctly (a
mis-ordered projection just bails to the fallback decoder every time,
which is still correct, just not fast). Benchmark projections against your
own message shapes: skipping a mostly-scalar shape isn't necessarily cheaper
than parsing it, and the pattern pays off most when the skipped content is
itself expensive to parse, such as nested objects.

## Usage

```go
//go:generate go tool zerojsongen -dir . -types Event,Record -out zerojson_gen.go
```

Generated API per type:

```go
buf, err := v.AppendZJSON(buf[:0])        // encode, zero-alloc with a reused buffer
err := v.UnmarshalZJSON(data)             // decode (strings are copied)
err := v.UnmarshalZJSONBorrow(data)       // zero-copy decode: strings alias data
```

Encode errors come only from delegated `MarshalJSON` implementations and
are propagated, never silently encoded as `null`. On error the returned
buffer holds a partial encoding and must be discarded.

Field handling:

- The `json` tag carries only encoding/json's own standard options: the
  name, `-`, `omitempty`, `omitzero`, and `string`. Custom options live
  in a separate `zerojson:"..."` tag — `intern` is the only one today,
  e.g. `json:"origin,omitempty" zerojson:"intern"`. An unrecognized
  option in either tag is a generate-time error naming the field, so a
  stray option (or one left behind by a migration) can never silently
  do nothing. On a map field, `intern` applies to its keys; untagged map
  keys are copied. Field order is declaration order.
- `omitempty` gates on emptiness (nil pointer/slice/map/interface, zero
  scalar, empty string); `omitzero` (Go 1.24 stdlib semantics) instead
  gates on the *zero value*. An `IsZero() bool` method takes precedence for
  every named shape, including pointers and named scalar/slice/map types;
  types without one use the ordinary zero value (nil for pointers, slices,
  maps, and interfaces). The difference shows up on a non-nil-but-empty slice,
  which `omitempty` drops but `omitzero` keeps (its zero value is nil,
  not len 0). Value-typed struct/time/custom-leaf fields, which `omitempty`
  cannot gate at all, can be gated with `omitzero` instead. **easyjson
  ignores `omitzero`** (treats it as an unrecognized option, so it's
  simply never checked) — this is the one field-level feature where
  byte-identity with easyjson does not hold; every other tag/kind
  combination still round-trips byte-for-byte.
- `string` (stdlib's "quoted" convention) is implemented for plain
  int/uint fields: encodes quoted and requires a quoted number on decode. Other kinds
  are a generate-time "not yet supported" error rather than silent
  mishandling.
- Direct fast paths: registered custom leaf types, `time.Time`, `uuid.UUID`,
  strings, bools, all int/uint/float kinds (including named types like
  `time.Duration`), and string-quoted int64 types (the common
  quoted-int64 convention for surviving float64-based decoders). A custom
  int64 type opts into that convention with an exact
  `ZeroJSONQuotedInt64()` marker method; without the marker its JSON methods
  are delegated normally, because an int64-backed type may use any wire
  representation.
- Named string types are enums: constants are collected automatically
  and decode via switch dispatch.
- Nested same-package structs are **auto-discovered** — list only the
  root types in `-types`. Slices of structs (`[]T` and `[]*T`, with
  null elements), slices of leaf types, and string-keyed maps
  (leaf/struct values, `struct{}` sets, named map/slice types) are
  supported. Map encode order is Go range order (matching easyjson;
  encoding/json would sort keys).
- Recursive generated schema graphs are refused at generation time. Their
  value-embedded arenas would otherwise be invalid recursive Go types, and
  safely encoding cyclic values requires cycle detection rather than an
  unbounded generated call chain.
- A **root** listed in `-types` may itself be a named slice (`type X
  []T`, `[]*T`) or a named string-keyed map (`type Y map[string]T`), not
  just a struct — the top-level shape easyjson handles but that otherwise
  needs a hand-written lexer loop. Roots get the same
  `AppendZJSON`/`UnmarshalZJSON(Borrow)` (and pooled `Decoder`) API; a nil
  root encodes as `null`, a non-nil empty one as `[]`/`{}`, and element
  structs keep their ordered fast path. A non-string map key (or a root
  that is neither struct, slice, nor string-keyed map) is a generate-time
  error.
- Types implementing `MarshalJSON`/`UnmarshalJSON` are delegated to;
  `MarshalText`/`UnmarshalText` types (e.g. `civil.Date`) serialize as
  quoted strings. A one-sided JSON or Text method pair is refused at
  generation time rather than silently using different encode/decode
  representations. Interface satisfaction is checked by **method
  signature**, not name: a same-named method with the wrong signature
  (e.g. `MarshalJSON() string`) is ignored and the field is encoded
  structurally, exactly as `encoding/json` and easyjson do.
- `[]byte` and named byte-slice types (`type Blob []byte`) encode as
  **base64 strings** and decode from them, matching `encoding/json` and
  easyjson — as fields, `*[]byte`, slice elements (`[][]byte`), and map
  values (`map[string][]byte`). `json.RawMessage` (a `Marshaler`) and
  `zerojson.Raw` (a registered leaf) are passthrough types and are *not*
  base64'd.
- `interface{}` / `any` fields (also as slice elements and map values)
  fall back to `encoding/json`, matching easyjson — so nested maps inside
  an `any` value have sorted keys.
- Untagged embedded value structs are **flattened** (fields promoted),
  matching `encoding/json` — including an **unexported** embedded value
  struct, whose *exported* fields are still promoted (e.g. an unexported
  `hidden` with an exported `Visible` contributes `"visible"`). Embedded
  **pointers** (exported or unexported), embedded types with their own
  JSON/Text methods, and promoted-field name conflicts are refused at
  generate time — an unexported embedded pointer specifically because
  `encoding/json` cannot allocate through it on decode, so round-trip
  parity is impossible.
- **Invalid JSON tag names fall back to the Go field name**, matching
  `encoding/json`'s `isValidTag` (a name containing e.g. a backslash is
  rejected and the field name is used). A `struct{}`-valued (set) map
  requires each value to be an **object or null** on decode, rejecting a
  number/array/string — matching both oracles.
- **Top-level JSON `null` follows stdlib target semantics**: it is a no-op
  for struct roots, while slice/map roots are set to nil. Every decode entry
  point rejects trailing data after the `null` (e.g. `nullx`).
- **Precedence**: hand-written JSON/Text methods are always respected.
  Methods living in generated files (easyjson output — detected via the
  standard `// Code generated` marker) are treated as replaceable, so
  listing only root types in `-types` discovers and generates the whole
  reachable graph even mid-migration. Explicitly listed types are
  always generated regardless.
## Compatibility contract

**The primary contract is semantic compatibility with `encoding/json`
v1.** For every type shape zerojson supports, a value encodes and decodes
with the same meaning the standard library gives it — and, outside a small
closed set of deliberate divergences (below), byte-for-byte the same
output. This is verified continuously against `encoding/json` by the
JSONTestSuite corpus, the decode differential fuzzers (in **both** oracle
directions — stdlib-rejects ⇒ zerojson-rejects, and zerojson-rejects ⇒
stdlib-rejects, each with an explicit allowlist), the byte-identity and
round-trip suites, and the **type-shape enumeration matrix**
(`conformance/generator/typeshape`), which cross-checks every field-kind × tag ×
value-state cell against `encoding/json` as the primary oracle.

### Deliberate divergences from `encoding/json`

This table is the single source of truth for every intentional difference;
it is the same allowlist the type-shape matrix and the decode differential
enforce in code (`conformance/generator/typeshape` `allowedReasons` and the fuzzers'
`zjDecodeStricterAllowed`). Nothing diverges that is not listed here.

| Divergence | Where | Reason | Alignment |
|---|---|---|---|
| Object keys match **case-sensitively** | decode | correctness + perf; a key must equal a field's name exactly | json/v2's default direction |
| Map keys in **Go range order**, not sorted | encode | perf; key order is semantically meaningless | matches easyjson |
| Valid `Marshaler` output **appended verbatim** | encode | perf; the span is grammar-checked but not rewritten | stdlib re-compacts it |
| Float formatting via `strconv` shortest `'g'` | encode | same *parsed value*; avoids stdlib's notation threshold | easyjson-identical (e.g. `4.96e+09` vs stdlib `4962676536.46`) |
| Declared-field **value leniency** | decode | trusted-path perf: a declared field's value may carry a leading zero, a raw control byte, or an invalid `\` escape without erroring | keys, unknown-field content, and trailing data stay grammar-strict (`Valid`) |

`omitzero` is honored with **Go 1.24 stdlib parity** (it gates on the zero
value, including user `IsZero` methods; reference shapes without one gate on
nil). This is not a divergence
from `encoding/json` — it is the one place byte-identity with **easyjson**
does not hold, because easyjson ignores the option (see the migration
section).

Unencodable values error identically to `encoding/json` (zerojson never
silently emits bytes stdlib wouldn't): NaN/Inf floats
(`json: unsupported value`) and `time.Time` years outside [0,9999]. Invalid
UTF-8 in a string value is replaced with the literal `�` escape, exactly
as `encoding/json` does.

### easyjson migration

zerojson is a drop-in successor to
[easyjson](https://github.com/mailru/easyjson): its output is **byte-identical
to easyjson's** (including easyjson's default HTML escaping of `<`, `>`, `&`),
so a cutover can be verified byte-for-byte against an existing wire format
rather than trusted. This identity is a migration convenience layered on top
of the `encoding/json` contract above, not the headline guarantee. It holds
everywhere except three spots where zerojson deliberately sides with
`encoding/json` instead:

- **Untagged embedded structs**: promoted fields are emitted at the embed's
  declaration position (matching `encoding/json` and json/v2); easyjson
  emits them after all direct fields. A type that needs easyjson-identical
  bytes should declare its embeds last, where the two conventions coincide.
  Pinned in `conformance/codec/embed_order_test.go`.
- **`omitzero`**: honored (above); easyjson ignores it.
- **Invalid tag names**: a tag name `encoding/json` would reject (e.g.
  containing a backslash) falls back to the Go field name, matching stdlib;
  easyjson emits the invalid tag verbatim.

## Validation

`zerojson.Valid(data []byte) error` is a standalone, zero-allocation,
full RFC 8259 grammar validator — genuinely a validator, unlike the
trusted-path decoder it composes with rather than replaces:

```go
if err := zerojson.Valid(data); err != nil {
    return err // *zerojson.SyntaxError, shaped like encoding/json's
}
```

It walks the entire payload (objects, arrays, strings — including every
escape form and control-byte rejection — numbers, and literals) and
requires exactly one top-level value plus optional trailing whitespace.
Its validity verdict matches `encoding/json.Valid` for every input
(`conformance`'s `FuzzValidAgainstStdlib`), including accepting invalid
UTF-8 inside strings (matching v1's byte-oriented scanner — a stricter
opt-in `ValidUTF8` variant matching `encoding/json/v2`'s default is a
possible future addition) and stdlib's `maxNestingDepth` of 10000.

**Decode success now implies the payload was valid JSON — with one
narrow, documented exception.** Every byte of a document that decodes
successfully is either parsed by a known field's own decode statement or
grammar-checked by `Skip` (unknown fields, and anything a projection
struct doesn't declare — see "Projection / passthrough decoding" above),
object **keys** are grammar-strict on every path (`ReadKey` validates
escapes and rejects raw control bytes, matching `json.Valid` — the
ordered fast path additionally only ever accepts a key that exactly
equals a declared-name literal), and trailing content after the top-level
value is always rejected. The exception is narrower than it once was: a
**known/declared field's own VALUE** still goes through the trusted-path
leaf readers unchanged — a bare number may have a leading zero, and a
string may contain a raw control byte or an invalid escape, without
erroring (see the compatibility contract above). Closing that last gap
(for declared-field values) was prototyped and measured directly against
this repo's pinned decode benchmark: it cost ~4-5% on a pooled decode in
the production-shaped benchmark harness, which this
project isn't paying for the general case, so it was reverted rather than
landed.

This gives the composition story its shape:

- **Decode-only call sites don't need a `Valid` pre-pass.** Internal
  replay of a durable log, or any path decoding into fields it actually
  declares, already gets grammar-checked skip and trailing-data
  rejection for free — that is precisely what a successful
  `UnmarshalZJSON`/`Decode` now means.
- **`Valid` is for validate-WITHOUT-decode call sites**: raw
  passthrough/storage of client-supplied JSON that nothing ever decodes —
  a jsonb column written through unread, a gateway forwarding a payload
  it doesn't parse itself, an opaque blob accepted and stored as-is.
  Those paths have no decode call to inherit a guarantee from, so `Valid`
  is how they get one.
- Regression-tested directly: `conformance/codec`'s
  `FuzzDecodeImpliesValid` splices fuzzer-controlled bytes into an
  injected unknown member's KEY, into an injected unknown field's VALUE,
  and into trailing content (never into an existing known field's value,
  which would spuriously trip the documented exception above) and
  requires decode success to imply `encoding/json.Valid` agrees, through
  every entry point.

## Caveats — read before production use

- **JSON `null` clears pointer and slice fields and leaves value fields
  untouched** — the same semantics as `encoding/json` on a reused
  target. (One divergence: value-typed `json.Unmarshaler` fields are
  skipped on null rather than handed `"null"`, matching easyjson.)
- **Borrow mode aliases the input buffer.** Never use it when the
  buffer is pooled/reused (e.g. Kafka fetch buffers) unless lifetimes
  are pinned.
- **Write your own oracle tests** when migrating a type: marshal with
  the incumbent codec and zerojson, assert `bytes.Equal`; decode with
  both, assert `reflect.DeepEqual`. See `conformance/codec` for the
  pattern (including the fuzz harness).

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

## Extension interface: `Appender`/`Reader`

A hand-written type can join the zero-alloc encode/decode path directly,
without a `-leaf` registration, by implementing:

```go
type Appender interface {
	AppendZJSON(dst []byte) ([]byte, error)
}

type Reader interface {
	ReadZJSON(l *zerojson.Lexer) // pointer receiver; report errors via l.Fail/l.AddError
}
```

This is the same append/read calling convention the generator itself
emits — an append-style extension point for custom field types, the same
pattern used by other codec projects. The generator detects the pair on a
field's type the same way it detects `MarshalJSON`/`UnmarshalJSON`, but
gives it precedence: a type implementing both gets the fast path. This
removes the main reason a `-leaf` registration exists for a type you
own — implement the interface directly on the type, in its own package,
and any struct that embeds it as a field picks up the fast path
automatically the next time it's generated.

`AppendZJSON` follows the same contract as every generated `AppendZJSON`
method: append to `dst` (never reset it), return the extended slice, and
on error return a buffer holding a partial encoding (which the caller
must discard). `ReadZJSON` follows the leaf `Read` convention: pull a
token from the `Lexer` and report failures via `l.Fail`/`l.AddError`
rather than a return value.

See `conformance/codec`'s `Cents` type for a complete worked example
(a fixed-point money type implementing both the extension pair and
`MarshalJSON`/`UnmarshalJSON`, with tests proving the two encodings are
byte-identical, round-trip, and that the extension path doesn't
allocate on encode).

## Drop-in adoption (`-compat`)

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

`-tests <path>` emits a fuzz test per generated type that needs no
hand-written samples: it decodes fuzzer input through both `encoding/json`
and zerojson, compares raw Go values for fields without opaque custom codec
state, compares again via normalized re-encode, and checks that zerojson's own
output re-decodes and is stdlib-parseable. With `-pool`, it also emits a
stateful four-operation fuzzer per explicitly listed type: all operations run
through one reused decoder with independently mixed copy/borrow modes and are
checked against fresh decodes, including invalid-input arena poisoning and a
copy-mode caller-buffer scribble that exposes accidental aliases. Run `-tests`
**without** `-compat` on the same types, so `encoding/json` stays an independent
oracle.

## Zero-alloc decode loops (`-pool`)

`-pool` emits a `<Type>Decoder` per listed type that reuses one arena
across decodes, eliminating the per-decode arena allocation. Combined
with `DecodeBorrow`, a decode loop allocates **zero** bytes:

```go
var d models.EventDecoder // pool one per goroutine, or via sync.Pool
for _, msg := range batch {
    var e models.Event
    if err := d.DecodeBorrow(msg, &e); err != nil { /* ... */ }
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

## Testing story

Correctness is checked continuously against three independent oracles,
not just internal self-consistency: **easyjson** (encode's byte-identity
target, including a genuinely easyjson-generated oracle type, not a
hand-simulated comparison), **`encoding/json` v1** (decode's primary
semantic contract), and **`encoding/json/v2`** (a third opinion, via
`go-json-experiment/json`, used to pin the specific points where v1 and
v2 disagree and confirm which side zerojson takes). On top of the
differential tests this buys, the suite adds: value-space fuzzing
(builds struct values directly, not JSON text, reaching encoder-only
states like NaN floats and invalid UTF-8); a truncation sweep over every
prefix of a message exercising every leaf kind; a copy-mode aliasing
guard (scribbles the input buffer after decode, asserts the result is
unaffected); a pool-pair fuzz for arena/slice-cell leakage across
back-to-back pooled decodes; a float-formatting property test against
`strconv.AppendFloat` directly; a `-race` hammer on the shared intern
table; and a golden corpus of synthetic record-shaped fixtures pinned by
byte-identity and a SHA-256 hash. `Valid` gets its own verdict-parity
fuzzer against `encoding/json.Valid` (including a truncation sweep
requiring exact agreement at every prefix length, and an adversarial
suite covering the maxNestingDepth boundary, multi-MB strings, and
pathological number grammar under generous time budgets), and the
decode-implies-valid composition story (see "Validation" above) has its
own differential fuzzer, `FuzzDecodeImpliesValid`. `scripts/verify.sh race`
is green across all three modules and the production-shaped
benchmark harness. Every fuzz target also receives a fixed execution budget
before a change lands; using counts rather than wall-clock deadlines avoids a
known Go fuzz deadline race while keeping the PR gate predictable. See
`conformance/README.md` for the full
oracle-by-property table and the v1-vs-v2 divergence details.

The generated fuzzers are schema-aware: every declared field contributes a
correctly-shaped seed, containers receive non-empty element/value seeds, and
escaped-number/string, invalid-UTF-8, and both `[]byte` input forms are added
where relevant. This reaches the field reader immediately instead of trying to
invent a key and value from `{}`. A separate source-level behavior matrix
generates temporary packages spanning custom JSON/Text methods, value/pointer
receivers, direct/slice/map addressability, explicit root membership, and
compatibility flags; it executes stdlib-vs-zerojson behavior tests after
generation, so a wrong method classification cannot hide behind code that
merely compiles.

`conformance/generator` also runs a deterministic generated-schema campaign:
each campaign creates one temporary package containing 40 multi-field structs
assembled from the supported scalar, pointer, container, embedding, tag, and
custom-method grammar, generates pooled codecs plus fuzz tests, and executes
stdlib-vs-zerojson behavior checks. Reproduce or expand a campaign with
`ZEROJSON_SCHEMA_SEED` and `ZEROJSON_SCHEMA_COUNT` (up to 500).

Adversarial resource tests measure both wall time and total allocated bytes at
N and 4N for base64 and numeric-array byte slices, invalid base64, byte-valued
maps, and arbitrary interface arrays. They require linear scaling, enforce
absolute allocation-amplification ceilings, and require zerojson to stay within
1.5x of `encoding/json` on the same input. The allocation checks run in the
ordinary suite; wall-clock ratios are enabled by `scripts/verify.sh full` (or
explicitly with `ZEROJSON_LINEARITY=1`) because shared-runner timing is too
noisy for a reliable CI gate.

Run the ordinary, race/checkptr, or pre-release verification tier across every
nested module with one command (a root-level `go test ./...` covers only the
root module):

```sh
scripts/verify.sh quick
scripts/verify.sh race
scripts/verify.sh full
```

GitHub Actions intentionally runs only the ordinary tests, vet, and the
regeneration drift check on code changes. The ordinary tests replay every
committed fuzz regression seed. Expensive race/checkptr, mutation, portability,
schema, fuzz, and benchmark campaigns run locally, where they do not consume
hosted-runner quota and benchmark results are less noisy. Run
`scripts/verify.sh full` plus the relevant fuzz and benchmark commands before a
release.

Run the complete, dynamically discovered fuzz campaign across all three modules
with `scripts/fuzz-all.sh` (60 seconds per target by default). It supports CI
sharding without maintaining a target list and continues after individual
failures so the final summary reports every failing target. Coverage-input
minimization is capped at 5 seconds by default (`FUZZMINIMIZETIME` overrides
it), preventing Go's 60-second minimizer default from consuming an entire
target budget; actual failure corpora remain saved for dedicated minimization.
For example:

```sh
FUZZTIME=60s FUZZ_SHARD_INDEX=0 FUZZ_SHARD_COUNT=4 scripts/fuzz-all.sh
```

Validate that the regression suite actually detects representative faults:

```sh
scripts/mutation-test.sh        # quick developer tier
scripts/mutation-test.sh full   # pre-release tier
```

Replay additional sanitized production-shaped JSON/JSONL records through the
stdlib shadow oracle without adding them to the repository:

```sh
scripts/shadow-corpus.sh /path/to/sanitized-corpus
```

When no production captures are available, exercise the modeled wire space:

```sh
scripts/synthetic-shadow.sh        # exhaustive pairwise interactions
scripts/synthetic-shadow.sh full   # three-wise + adversarial scale cases
```

## License

zerojson is licensed under the [Apache License 2.0](LICENSE). The vendored
JSONTestSuite corpus retains its original MIT license; see
[third-party licenses](THIRD_PARTY_LICENSES.md).

Copyright 2026 Neal Patel.
