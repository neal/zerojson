package codec

import (
	"encoding/json"
	"testing"
)

// truncation_test.go is a deterministic truncation sweep. For every
// message in a corpus chosen to exercise every leaf kind — UUIDs and
// RFC3339 timestamps (fixed-offset parsers
// that read fixed windows past the current position), decimals, quoted
// ints, escaped strings, nested structs, slices, maps, Raw fields, and
// the compact declaration-ordered shape that engages the ordered
// decoder's word-compare loads — every prefix data[:i] is decoded in
// every mode (plain copy, borrow, pooled copy, pooled borrow, and the
// fallback-only decoder directly). The only acceptable outcomes are a
// clean error or (for a prefix that happens to be valid JSON — possible
// only when the original document has a top-level closing brace before
// the end, which compact single-object documents don't) a clean success:
// never a panic, never an out-of-bounds read. This is a plain test, not
// a fuzzer: truncation is enumerable, so enumerate it.

// truncationCorpus returns documents that collectively hit every leaf
// kind's decode path. Each entry is compact and declaration-ordered so
// the ordered fast path (and its unsafe word-compare key loads) engages
// before bailing at the truncation point.
func truncationCorpus(t testing.TB) map[string][]byte {
	t.Helper()
	corpus := make(map[string][]byte)

	for name, tr := range variants() {
		b, err := tr.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		corpus["event_"+name] = b
	}

	// Escaped strings and interned fields (Origin is intern-tagged).
	corpus["event_escapes"] = []byte(`{"origin":"a<b>c&d","comment":"tab\tnl\nunié ","mode":"up"}`)

	// RawHolder: Raw value capture (object, array, string, number) and a
	// pointer Raw.
	corpus["raw_object"] = []byte(`{"table":"records","set":{"a":1,"b":[1,2,{"c":"d"}]},"where":{"id":"x"}}`)
	corpus["raw_scalar"] = []byte(`{"table":"items","set":"str","where":123}`)

	// MapFieldsLike: map-shaped Marshaler fields (RawValue spans).
	corpus["dbop"] = []byte(`{"op":"UPDATE","set":{"amt":"1.5"},"where":{"id":"y"}}`)

	// Appender extension decode (Cents.ReadZJSON).
	corpus["appender"] = []byte(`{"amount":"123.45","cost":"-0.99"}`)

	// Quoted ints via the ,string option.
	corpus["stringopt"] = []byte(`{"count":"4294967295","amount":"-123456789"}`)

	return corpus
}

// decodeAllModes decodes data into a fresh Event-shaped target in every
// decode mode the generated code offers, returning the first error (the
// assertion is only "no panic"; error-vs-success may legitimately differ
// per prefix). The pooled decoders are fresh per call so a poisoned
// arena from one truncated decode can't mask a crash in the next — pool
// reuse across garbage inputs is pool_fuzz_test.go's job, not this
// sweep's.
func decodeEventAllModes(data []byte) {
	var a, b, c, d Event
	_ = a.UnmarshalZJSON(data)
	_ = b.UnmarshalZJSONBorrow(data)
	var dec1, dec2 EventDecoder
	_ = dec1.Decode(data, &c)
	_ = dec2.DecodeBorrow(data, &d)
	// Fallback-only path (bypassing the ordered fast path entirely), via
	// ordered_test.go's helper — the sweep must cover it directly since
	// a truncation-triggered ordered bail otherwise reaches it only at
	// whatever position the ordered decoder rewound to.
	_, _ = decodeFallbackOnly(data, false)
	_, _ = decodeFallbackOnly(data, true)
}

func decodeRawHolderAllModes(data []byte) {
	var a, b, c, d RawHolder
	_ = a.UnmarshalZJSON(data)
	_ = b.UnmarshalZJSONBorrow(data)
	var dec1, dec2 RawHolderDecoder
	_ = dec1.Decode(data, &c)
	_ = dec2.DecodeBorrow(data, &d)
}

func decodeDBOpAllModes(data []byte) {
	var a, b, c, d MapFieldsLike
	_ = a.UnmarshalZJSON(data)
	_ = b.UnmarshalZJSONBorrow(data)
	var dec1, dec2 MapFieldsLikeDecoder
	_ = dec1.Decode(data, &c)
	_ = dec2.DecodeBorrow(data, &d)
}

func decodeAppenderAllModes(data []byte) {
	var a, b, c, d AppenderCases
	_ = a.UnmarshalZJSON(data)
	_ = b.UnmarshalZJSONBorrow(data)
	var dec1, dec2 AppenderCasesDecoder
	_ = dec1.Decode(data, &c)
	_ = dec2.DecodeBorrow(data, &d)
}

func decodeStringOptAllModes(data []byte) {
	var a, b, c, d StringOptCases
	_ = a.UnmarshalZJSON(data)
	_ = b.UnmarshalZJSONBorrow(data)
	var dec1, dec2 StringOptCasesDecoder
	_ = dec1.Decode(data, &c)
	_ = dec2.DecodeBorrow(data, &d)
}

// TestTruncationSweepNoPanic is the sweep proper. Each prefix is decoded
// inside its own recover() scope so a panic reports the exact corpus
// entry, prefix length, and prefix bytes.
func TestTruncationSweepNoPanic(t *testing.T) {
	corpus := truncationCorpus(t)

	decodersFor := func(name string) func([]byte) {
		switch {
		case name == "raw_object" || name == "raw_scalar":
			return decodeRawHolderAllModes
		case name == "dbop":
			return decodeDBOpAllModes
		case name == "appender":
			return decodeAppenderAllModes
		case name == "stringopt":
			return decodeStringOptAllModes
		default:
			return decodeEventAllModes
		}
	}

	for name, data := range corpus {
		decode := decodersFor(name)
		t.Run(name, func(t *testing.T) {
			// Sanity: the full document must be valid (the sweep is
			// meaningless over a corpus entry that never decoded).
			if !json.Valid(data) {
				t.Fatalf("corpus entry is not valid JSON: %s", data)
			}
			for i := 0; i <= len(data); i++ {
				prefix := data[:i:i] // capped capacity: an append past len is an
				// immediate re-allocation, never a silent write into the
				// original document's remaining bytes
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("panic decoding prefix [:%d] of %s\nprefix: %q\npanic: %v", i, name, prefix, r)
						}
					}()
					decode(prefix)
				}()
			}
		})
	}
}

// TestTruncationSweepErrorsAreReported additionally asserts that a
// strictly truncated prefix of a single-object document is reported as
// an error (not silently accepted as a successful decode) by the plain
// copy path — the property that makes the no-panic sweep meaningful.
// The full document (i == len) must decode cleanly.
func TestTruncationSweepErrorsAreReported(t *testing.T) {
	corpus := truncationCorpus(t)
	for name, data := range corpus {
		t.Run(name, func(t *testing.T) {
			// For the error-reporting property the target's shape mostly
			// doesn't matter (any truncated object must error), so most
			// entries decode into Event: unknown keys are skipped, and a
			// truncated document errors during skip just as it does
			// mid-field. The stringopt corpus is the exception: its
			// `,string`-quoted numbers land on Event's PLAIN numeric
			// fields of the same name, which (correctly) reject a quoted
			// token, so it decodes into its own StringOptCases shape.
			decode := func(b []byte) error {
				if name == "stringopt" {
					var v StringOptCases
					return v.UnmarshalZJSON(b)
				}
				var v Event
				return v.UnmarshalZJSON(b)
			}
			if err := decode(data); err != nil {
				t.Fatalf("full document rejected: %v", err)
			}
			for i := range data {
				if err := decode(data[:i:i]); err == nil {
					t.Fatalf("prefix [:%d] of %s decoded without error\nprefix: %q", i, name, data[:i])
				}
			}
		})
	}
}
