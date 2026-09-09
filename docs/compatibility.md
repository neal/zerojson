# Compatibility

[Overview](../README.md) · [User guide](guide.md)

- [Contract and differences](#contract)
- [Toolchain differences](#which-encodingjson-go-127-and-later)
- [Validation](#validation)
- [Ownership and pooled decoding](#ownership-and-pooled-decoding)
- [Target reuse and nulls](#target-reuse-and-nulls)
- [Errors and callbacks](#errors-and-callbacks)
- [Resource considerations](#resource-considerations)

## Contract

The target is classic `encoding/json` semantics for supported schemas,
subject to the differences below and the ownership/error rules in
[Caveats](#caveats--read-before-production-use). Unsupported shapes are rejected
at generation time.

Differential tests compare acceptance, decoded values, and encoding against
stdlib and easyjson. Coverage includes a finite type-shape matrix, generated
schema campaigns, and fuzzing; it is not a proof over all schemas or inputs.

### Deliberate divergences from `encoding/json`

The table summarizes decode/encode differences. Test-specific allowlists live
in `conformance/generator/typeshape` and the generated fuzzers; they cover the
properties each harness checks, not a mechanically generated copy of this table.

| Difference | zerojson behavior | Comparison |
|---|---|---|
| Map ordering | Go range order | Stdlib sorts keys; easyjson uses range order |
| Marshaler output | Validated, then appended verbatim | Stdlib also compacts and escapes HTML-sensitive characters and U+2028/U+2029 |
| Float notation | Shortest `strconv` `'g'` spelling | Same parsed value, but notation thresholds differ from stdlib (`1e+09` vs `1000000000`) |
| String escapes | Backspace/form-feed use `\u0008`/`\u000c` | Stdlib uses `\b`/`\f`; easyjson uses the long forms |
| Quoted-int64 marker null | Leaves a value-typed marker unchanged | Its `UnmarshalJSON` is bypassed; stdlib would call it |
| Registered leaf null | Leaves a non-pointer leaf unchanged, except the built-in Raw leaf | Registration overrides the type's JSON methods; see custom leaf rules |
| Registered-leaf container reuse | Containers whose traversal contains an explicit leaf/marker override keep the generated reader's reset behavior | Ordinary containers use stdlib element reuse; extension contracts can override it |
| Escaped direct-leaf strings | Timestamp, UUID, and quoted-int64 paths unescape before parsing; custom leaves follow their selected reader | Stdlib passes raw JSON to `UnmarshalJSON`, which may reject an escaped spelling |

Duplicate members are applied in order. Objects merge into reused structs
and maps; ordinary slices reuse their elements as stdlib does. Field lookup
prefers exact names, then the first case-insensitive match in field order.

Nil and empty `zerojson.Raw` values encode as `null`; stdlib rejects an empty
non-nil `json.RawMessage`. Pooled decoding resets the destination instead of
merging into it. Failed decodes may leave different partial state from stdlib.
These API-specific rules are detailed below.

`omitzero` is honored with **Go 1.24 stdlib parity** (it gates on the zero
value, including user `IsZero` methods; reference shapes without one gate on
nil). easyjson ignores the option.

Built-in encoders reject NaN/Inf floats and unrepresentable timestamps,
including years outside [0,9999]. Error types and text need not match stdlib.
Custom append/leaf codecs are responsible for producing valid JSON. Invalid
UTF-8 in a string value is replaced with the literal six-byte `\ufffd`
escape, exactly as classic `encoding/json` (and easyjson) does.

### Which `encoding/json`? (Go 1.27 and later)

The contract above is written against **classic `encoding/json` v1**,
the implementation every Go release through 1.26 shipped. Go 1.27
re-based `encoding/json` on the `encoding/json/v2` implementation
(`GOEXPERIMENT=jsonv2` became the default); the resulting "v1-on-v2"
layer preserves v1's API and almost all of its behavior, but not every
edge. zerojson's direct readers and writers retain their classic-v1 policies.
Delegated JSON/Text methods and stdlib-backed interface paths may change with
the toolchain or the custom type's implementation. The suite pins these
observed implementation differences:

- **Invalid UTF-8 in an encoded string**: classic v1 writes the `\ufffd`
  escape; v1-on-v2 writes the raw U+FFFD rune. zerojson writes the
  escape. This also reaches zerojson's stdlib-backed
  `interface{}`/`any` encoding — so on Go 1.27+ an invalid byte
  inside an `any` value encodes as the raw rune, and byte-identity with
  easyjson for that (already exotic) case no longer holds there.
- **`,string` fields**: v1-on-v2 accepts a leading `+` inside the quotes
  (`"+00"`); classic v1, easyjson, and zerojson reject it. And for a
  `,string` *pointer* field, an escaped quoted null (`"\u006eull"`)
  preserves an existing pointer under v1-on-v2, but allocates a pointer
  to zero if it was nil. Plain `"null"` clears it; classic v1 and
  zerojson clear it for both spellings.
- **Invalid tag names** (a name containing a character classic v1's
  `isValidTag` rejects — a backslash, a backtick, a quote): classic v1
  falls back to the Go field name. In the tested v1-on-v2 toolchain,
  `a\\b`, `a"b`, and a name containing a backtick after `a` become `a`;
  a name consisting only of that invalid character falls back to the Go
  field name. Truncation can also create a name collision that drops the
  competing fields. A tab remains part of the name (escaped in JSON).
  zerojson follows classic v1.
- **TextUnmarshaler slices and maps on `null`**: classic v1 clears a
  value-typed named slice or map, without invoking `UnmarshalText`.
  v1-on-v2 leaves it untouched. zerojson clears it, following classic v1;
  pointer fields still clear to nil on both toolchains.
- The `any`-fallback encode path allocates more under v1-on-v2 than under
  classic v1 (it is stdlib's allocation, not zerojson's; the conformance
  suite pins both figures).

The conformance suite detects which implementation it is running
against by probing behavior (not by Go version, so
`GOEXPERIMENT=nojsonv2` on a 1.27+ toolchain behaves like 1.26) and pins
the known differences. CI currently tests Go 1.26.x and 1.27.x.

### easyjson migration

zerojson provides easyjson-compatible encoding for supported shapes, including
HTML escaping of `<`, `>`, and `&`. Verify your types against the incumbent
encoder before migrating: the following differences prevent a universal
byte-identity guarantee.

- Promoted embedded fields are emitted at the embed's declaration position;
  easyjson emits them after direct fields. Declaring embeds last aligns them.
- `omitzero` is honored; easyjson ignores it.
- Invalid JSON tag names fall back to the Go field name; easyjson uses the tag.
- Interface encoding delegates to the selected Go toolchain's `encoding/json`.
  Its output can differ from classic-v1 fixtures, including invalid UTF-8
  replacement spelling under the v2-backed implementation.

Maps use Go range order, so multi-key map encodings are not deterministic even
between two calls to the same encoder. Compare those objects semantically or
normalize key order; use byte comparisons where ordering is deterministic.
See [compatibility wrappers](guide.md#compatibility-wrappers) for API migration.

## Validation

`zerojson.Valid(data []byte) error` checks one complete JSON value plus optional
trailing whitespace. It returns nil without allocating on success, or a
`*zerojson.SyntaxError` on failure.

```go
if err := zerojson.Valid(data); err != nil {
    return err
}
```

Its contract is the acceptance verdict of `encoding/json.Valid`, including
acceptance of invalid UTF-8 and unpaired Unicode escapes inside strings and a
limit of 10,000 nested containers. Error messages and offsets are not a stdlib
compatibility guarantee.

Public decode entry points validate complete JSON grammar before decoding.
Use `Valid` alone for opaque storage/passthrough. Low-level Lexer methods
remain building blocks, not whole-document validators. Syntax validation
does not validate your schema, business rules, or resource limits.

## Caveats — read before production use

### Ownership and pooled decoding

- **Borrow mode aliases the input buffer.** Never use it when the
  buffer is pooled/reused (e.g. Kafka fetch buffers) unless lifetimes
  are pinned. Note that *which* strings alias is input-dependent: a
  plain string (or `json.Number`) aliases, but one that needed unescaping was copied into
  a fresh allocation, an enum value that matched a declared constant
  is that constant, and an enum value that matched none was copied — so
  a test that happens to use escaped strings or declared constants can
  pass while production input (no escapes, an undeclared value) aliases
  and later corrupts. Treat every string from a borrow decode as aliased.

- **A pooled `Decoder` zeroes `*v` before every decode and is
  single-goroutine.** It never adopts a caller-owned map or pointee as an
  arena cell (a later decode would clear it in place), so decode into a
  fresh or throwaway value, not into a struct you also populate yourself.
  Overlapping calls on one Decoder panic with a descriptive message;
  keep one Decoder per goroutine
  (`sync.Pool` works well). A decoded value is invalidated by the next
  decode on the same Decoder; keeping it yields a value stitched from two
  messages, silently — copy out what you keep.

### Target reuse and nulls

- **JSON `null` clears pointer, slice, map, and interface fields and
  leaves other value fields untouched** — the same semantics as
  `encoding/json` on a reused target. A value-typed `json.Unmarshaler`
  (field, slice element, or map value) is handed `"null"` exactly as
  stdlib does, so a nullable custom type that clears a `Valid` flag on
  null behaves the same under both decoders — this includes an
  `Appender`/`Reader` type that also implements `UnmarshalJSON` (the
  null goes to `UnmarshalJSON`, since that is what stdlib knows —
  whether or not the type also has `MarshalJSON`, which decoding never
  consults). Without `UnmarshalJSON`, an `Appender` or `TextUnmarshaler`
  whose underlying type is a slice or map is cleared to nil; scalar,
  array, and struct values are left untouched. A value-typed
  `zerojson.Raw` stores the literal `null`. Quoted-int64 markers and registered
  leaves bypass JSON methods; see the [custom leaf rules](guide.md#custom-leaf-types).

- **Reused targets with existing pointers or non-nil interface fields use
  the keyed decoder.** Existing pointers retain their identity and may
  share storage; interfaces may hold custom decoders. Speculative replay
  cannot safely repeat their writes or callbacks. Fresh and pooled targets
  keep the ordered fast path when their schema is otherwise eligible.

### Errors and callbacks

- **Containers of custom JSON/Text values use stdlib traversal.** This
  includes pointer elements and named collection roots. Custom methods see
  the active container as stdlib publishes it, rather than a private arena
  header published after the loop. These containers may allocate on each
  pooled call; the pooled entry point still resets the destination first.
  Borrow mode does not override the custom methods' own ownership behavior.

- **Decoding is not transactional.** After an error, the target can be
  partially changed, and that partial state need not match stdlib. Treat it
  as failed output. A `Valid` pre-pass prevents syntax errors from reaching
  callbacks, but not later type or custom-validation errors. Public entry
  points perform that syntax pre-pass automatically.

- **Delegated `UnmarshalJSON`/`UnmarshalText` run only on a cleanly
  captured value.** If the value's own grammar walk fails, the decode
  error stands and the callback is not invoked with an empty or partial
  span — user code never sees a malformed value. Decoding is single-pass,
  after a whole-document syntax pre-pass at public entry points. A callback
  can still run before a later type or custom-validation error, as in stdlib.

- **Decode errors carry the field path and byte offset**: a
  `*zerojson.FieldError` such as `field legs.price: zerojson: expected
  number at offset 41` (the innermost field last), unwrapping to the
  underlying error; errors from delegated `UnmarshalJSON`/`UnmarshalText`
  are wrapped the same way. Framing errors may have no field path; custom
  errors need not contain offsets. `Valid` returns a `*SyntaxError`.
  Avoid logging raw payloads that may contain sensitive data.

- **Reachable JSON/Text/Appender dispatch disables the ordered fast path**
  to avoid callback replay. A type selected as a built-in or registered leaf
  instead follows that leaf's contract.
  Allocation cost depends on the codec and shape. Registered `-leaf` readers
  retain the ordered path under the [leaf replay contract](guide.md#custom-leaf-types).

### Resource considerations

A pooled arena retains the high-water mark of the largest message it has seen
(slice and map backing storage is reused, never shrunk), so bound message size,
or recycle Decoders that have seen an outlier.

- **The intern table is process-global and entry-bounded** (32 shards ×
  4096 entries of ≤24 bytes). The cap does not bound strings retained by
  callers or temporary memory awaiting garbage collection. A shard that fills
  starts a new generation rather than freezing, so a burst of
  high-cardinality input (a mistakenly interned map key space) costs a
  re-intern of the hot values afterwards, not permanent loss of
  interning. Still: tag `zerojson:"intern"` only on closed
  enumerations, never on keys from untrusted input.

### Migration checks

- **Write your own oracle tests** when migrating a type: marshal with
  the incumbent codec and zerojson, assert `bytes.Equal`; decode with
  both, compare values and any caller-visible pointer identity. Normalize
  map order where necessary and account for documented divergences.
  See `conformance/codec` for examples.
