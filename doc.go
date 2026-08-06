// Package zerojson is the runtime for a codegen-based JSON codec built for
// high-throughput, fixed-schema message shapes: zero-allocation encoding
// and near-zero-allocation decoding, with output byte-identical to
// easyjson so an existing wire format can be adopted and verified
// byte-for-byte. Installing it pulls no dependencies — the root module's
// go.mod has no requires.
//
// You don't write codecs by hand or reflect at runtime. The generator
// (cmd/zerojsongen) reads your Go types once, at build time, and emits the
// codec as ordinary Go source.
//
// # Generate, then use
//
// Point the generator at a package and list the root types (nested and
// element/value structs are auto-discovered):
//
//	//go:generate go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types Event,Record -out zerojson_gen.go
//
// It writes AppendZJSON / UnmarshalZJSON / UnmarshalZJSONBorrow methods for
// each type. Roots may be structs, named slices (type X []T, []*T), or
// named string-keyed maps (type Y map[string]T).
//
//	buf, err := v.AppendZJSON(buf[:0]) // encode; append-style, reuse buf for zero alloc
//	err := v.UnmarshalZJSON(data)      // decode; strings are copied
//
// AppendZJSON never resets dst — it appends and returns the extended
// slice. On error the returned buffer holds a partial encoding and must be
// discarded.
//
// # Three decode modes and their lifetimes
//
// The modes trade copying for aliasing; pick by how long the decoded value
// must outlive the input buffer.
//
//   - UnmarshalZJSON(data): decoded strings are COPIED, so the value is
//     independent of data — safe once data is reused or freed. Costs one
//     arena allocation per call (backing the target's pointer fields).
//
//   - UnmarshalZJSONBorrow(data): decoded strings (and zerojson.Raw fields,
//     and pooled slice/map contents) ALIAS data. Zero-copy, but data must
//     stay live and unmodified for the whole lifetime of the value. Never
//     use it with a pooled or reused input buffer (e.g. a Kafka fetch
//     buffer) unless lifetimes are pinned.
//
//   - A pooled Decoder (generate with -pool): Decode / DecodeBorrow reuse a
//     single arena across calls, eliminating the per-decode arena
//     allocation — combined with DecodeBorrow, a decode loop allocates
//     zero bytes. The catch is the arena is shared: the value holds
//     pointers into it (and, in borrow mode, into data), so it is
//     invalidated by the next Decode/DecodeBorrow on that Decoder. Finish
//     with — or copy out of — one result before decoding the next. Pool one
//     Decoder per goroutine, or via a sync.Pool.
//
// JSON null clears pointer, slice, and map fields (and leaves value fields
// untouched), matching encoding/json on a reused target.
//
// # Valid: validate without decoding
//
// Valid(data) is a standalone, zero-allocation, full RFC 8259 grammar
// validator whose verdict matches encoding/json.Valid for every input. A
// successful decode already implies the payload was grammar-valid JSON
// (unknown fields and skipped content are grammar-checked, and trailing
// data is rejected), with one documented exception: a known field's own
// value stays leniently parsed. So Valid is for validate-WITHOUT-decode
// call sites — an opaque payload stored or forwarded without ever being
// decoded — which have no decode call to inherit that guarantee from.
//
// # Compatibility, in brief
//
// Encoded output is byte-identical to easyjson's, including its default
// HTML escaping. Keys are matched case-sensitively (like easyjson and
// encoding/json/v2). A handful of deliberate divergences from
// encoding/json v1 (float formatting, verbatim Marshaler output, lenient
// known-field values) and the single knowing divergence from easyjson
// (omitzero, which easyjson ignores) are enumerated in the README and
// pinned by differential tests.
//
// # Extending the fast path
//
// A type that formats as one JSON token can join the zero-alloc path
// without reflection two ways: implement the Appender/Reader interface
// pair on the type (best for types you own), or register free append/read
// functions with the generator's -leaf flag (for types you don't).
// zerojson.Raw captures and re-emits an arbitrary JSON value verbatim, for
// projection/passthrough decoding.
//
// # Correctness
//
// This runtime package is deliberately dependency-free; the differential
// and byte-identity test suites that need external oracles (easyjson,
// encoding/json v1 and v2, google/uuid) live in the
// separate conformance module. See github.com/neal/zerojson/conformance
// for the oracle-by-property story and the fuzzers that pin it.
package zerojson
