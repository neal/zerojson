// Package zerojson provides the dependency-free runtime for generated JSON
// codecs over fixed Go schemas. The generator resolves field names, tags,
// and codec dispatch at build time and emits ordinary Go source.
//
// # Generate, then use
//
// Install cmd/zerojsongen as a Go tool and add a directive to your package:
//
//	//go:generate go tool zerojsongen -types Event,Record
//
// Roots may be structs, named slices, or named string-keyed maps. Reachable
// same-package structs are discovered automatically.
//
//	buf, err := v.AppendZJSON(buf[:0]) // append to a reused buffer
//	err = v.UnmarshalZJSON(data)      // decode with copied strings and Raw values
//
// AppendZJSON appends without resetting the buffer. Discard the partial
// encoding on error. Leaf-only shapes can encode without allocations when
// the destination has enough capacity; custom codecs and other shapes may
// allocate.
//
// # Decode modes and lifetimes
//
// UnmarshalZJSON copies strings and Raw values read by the runtime. Ordinary
// decodes preserve existing pointer identities and merge into reused targets,
// except that slice elements are rebuilt from zero. Custom decoders must
// honor their own input-ownership contracts.
//
// UnmarshalZJSONBorrow may alias input bytes for strings and Raw values.
// Keep the input unmodified for the entire lifetime of the result, including
// retained fields. Escaped strings may be copied; do not rely on that when
// deciding whether a buffer can be reused.
//
// With -pool, the generator emits Decode and DecodeBorrow methods on a
// reusable Decoder. Each call resets the target and reuses arena storage,
// invalidating the previous result. Finish using or copy out retained data
// before the next call. Use one Decoder per goroutine; overlapping calls on
// the same Decoder panic. Pooled borrow decoding of leaf-only shapes can
// avoid allocations, but custom codecs and containers may still allocate.
//
// Decoding is not transactional: an error may leave a partially changed
// target, whose contents need not match encoding/json's partial result.
//
// # Validation and compatibility
//
// Valid checks complete JSON grammar with the acceptance policy of
// encoding/json.Valid, including acceptance of invalid UTF-8 in strings.
// It allocates nothing on success and returns a *SyntaxError on failure.
//
// Decode success does not imply strict JSON validity: ordinary string and
// number readers intentionally accept some malformed values. Keys, skipped
// values, and trailing data are checked strictly. Call Valid before decoding
// when strict grammar is required, or use it alone for opaque payloads.
//
// The codec targets classic encoding/json semantics and easyjson-style
// encoding, with documented differences including case-sensitive key matching,
// unsorted maps, float formatting, verbatim Marshaler output, and slice reuse.
// Null handling also depends on custom methods. Read the [compatibility guide]
// before adopting it; byte identity is not universal.
//
// # Custom codecs
//
// Implement the Appender/Reader pair for types you own, or register append/read
// functions with the generator's -leaf flag. Registered leaf readers may be
// replayed by ordered decoding and must be safe to invoke more than once.
// Custom appenders are responsible for producing valid JSON.
// Raw captures a grammar-checked JSON value without materializing it.
//
// External-oracle tests live in the separate conformance module so the
// runtime remains dependency-free. See the repository's conformance README
// for coverage, fuzzing, and verification commands.
//
// [compatibility guide]: https://github.com/neal/zerojson/blob/main/docs/compatibility.md
package zerojson
