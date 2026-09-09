package codec

import (
	"encoding/json"
	"testing"
)

// Public decode success implies valid JSON, including declared field values.
// Check arbitrary documents and structured mutations through copy, borrow,
// pooled and projection entry points against encoding/json.Valid.

// spliceUnknownMember inserts an extra, always-genuinely-unknown member
// ("zzzunknown") as the last member of a compact JSON object, with value
// bytes taken verbatim from the fuzzer — exercising exactly what Skip's
// grammar-checking (and, transitively, the ordered decoder's inline skip,
// which tries known keys first and falls through to Skip on this one)
// must cover. Returns base unchanged if it isn't shaped like a
// compact `{...}` object.
func spliceUnknownMember(base, valueNoise []byte) []byte {
	if len(base) < 2 || base[0] != '{' || base[len(base)-1] != '}' {
		return base
	}
	inner := base[1 : len(base)-1]
	out := make([]byte, 0, len(base)+len(valueNoise)+16)
	out = append(out, '{')
	out = append(out, inner...)
	if len(inner) > 0 {
		out = append(out, ',')
	}
	out = append(out, `"zzzunknown":`...)
	out = append(out, valueNoise...)
	out = append(out, '}')
	return out
}

// spliceUnknownKey inserts a member whose KEY is fuzzer-controlled bytes
// (wrapped in quotes) with a fixed valid value, as the last member of a
// compact object. Now that keys are grammar-strict on every decode path,
// decode success must imply the whole document — including this key — is
// valid JSON. Returns base unchanged if it isn't a compact `{...}` object.
func spliceUnknownKey(base, keyNoise []byte) []byte {
	if len(base) < 2 || base[0] != '{' || base[len(base)-1] != '}' {
		return base
	}
	inner := base[1 : len(base)-1]
	out := make([]byte, 0, len(base)+len(keyNoise)+16)
	out = append(out, '{')
	out = append(out, inner...)
	if len(inner) > 0 {
		out = append(out, ',')
	}
	out = append(out, '"')
	out = append(out, keyNoise...)
	out = append(out, `":1`...)
	out = append(out, '}')
	return out
}

// FuzzDecodeImpliesValid checks arbitrary input and documents with injected
// keys, values and suffixes. Success must imply json.Valid; the reverse is
// not required because a syntactically valid document can violate the schema.
func FuzzDecodeImpliesValid(f *testing.F) {
	for _, tr := range variants() {
		b, _ := tr.AppendZJSON(nil)
		f.Add(b, []byte("zzz"), []byte(`1`), []byte(""))
		f.Add(b, []byte(`bad\qkey`), []byte(`"ok"`), []byte("  "))
		f.Add(b, []byte("raw\nctl"), []byte(`{"a":01}`), []byte("garbage"))
		f.Add(b, []byte(`k`), []byte(`truex`), []byte(","))
	}
	f.Add([]byte(`{"origin":"a","active":true}`), []byte(`ok`), []byte(`[1,]`), []byte("x"))

	f.Fuzz(func(t *testing.T, base, keyNoise, valueNoise, trailingNoise []byte) {
		if hugeExponent(valueNoise) || hugeExponent(trailingNoise) {
			return
		}

		// Splice fuzzer bytes into an unknown KEY and an unknown member's
		// VALUE, then append trailing bytes — the three things strict key
		// scanning, strict skip, and trailing-data rejection own.
		spliced := spliceUnknownMember(spliceUnknownKey(base, keyNoise), valueNoise)
		full := append(append([]byte(nil), spliced...), trailingNoise...)

		for _, full := range [][]byte{base, full} {
			wantValid := json.Valid(full)

			checkEntry := func(name string, err error) {
				t.Helper()
				gotOK := err == nil
				if gotOK && !wantValid {
					t.Fatalf("%s: decode succeeded on invalid JSON\ninput: %q", name, full)
				}
			}

			var a Event
			checkEntry("Event.UnmarshalZJSON", a.UnmarshalZJSON(full))
			var b Event
			checkEntry("Event.UnmarshalZJSONBorrow", b.UnmarshalZJSONBorrow(full))
			var dec EventDecoder
			var c Event
			checkEntry("EventDecoder.Decode", dec.Decode(full, &c))
			var dec2 EventDecoder
			var d Event
			checkEntry("EventDecoder.DecodeBorrow", dec2.DecodeBorrow(full, &d))

			var p1 EventProjection
			checkEntry("EventProjection.UnmarshalZJSON", p1.UnmarshalZJSON(full))
			var p2 EventProjection
			checkEntry("EventProjection.UnmarshalZJSONBorrow", p2.UnmarshalZJSONBorrow(full))
			var pdec EventProjectionDecoder
			var p3 EventProjection
			checkEntry("EventProjectionDecoder.Decode", pdec.Decode(full, &p3))
			var pdec2 EventProjectionDecoder
			var p4 EventProjection
			checkEntry("EventProjectionDecoder.DecodeBorrow", pdec2.DecodeBorrow(full, &p4))
		}
	})
}

// TestDecodeImpliesValidHandPicked pins a handful of concrete cases
// end-to-end, independent of the fuzzer, for a fast/readable signal (and a
// worked example of what decode-implies-valid covers): an injected unknown
// field with malformed grammar, and trailing garbage, both make decode
// fail — matching json.Valid — for both Event and EventProjection.
func TestDecodeImpliesValidHandPicked(t *testing.T) {
	base, err := variants()["full"].AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		build func() []byte
	}{
		{"clean", func() []byte { return base }},
		{"unknown_malformed_number", func() []byte {
			return spliceUnknownMember(base, []byte("01"))
		}},
		{"unknown_malformed_literal", func() []byte {
			return spliceUnknownMember(base, []byte("truex"))
		}},
		{"unknown_bad_escape_string", func() []byte {
			return spliceUnknownMember(base, []byte(`"\z"`))
		}},
		{"unknown_well_formed", func() []byte {
			return spliceUnknownMember(base, []byte(`{"a":[1,2,3],"b":"ok"}`))
		}},
		{"trailing_garbage", func() []byte {
			return append(append([]byte(nil), base...), "garbage"...)
		}},
		{"trailing_whitespace", func() []byte {
			return append(append([]byte(nil), base...), " \t\n"...)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			wantValid := json.Valid(data)

			var full Event
			fullErr := full.UnmarshalZJSON(data)
			if (fullErr == nil) != wantValid {
				t.Errorf("Event.UnmarshalZJSON: err=%v (ok=%v), json.Valid=%v", fullErr, fullErr == nil, wantValid)
			}

			var proj EventProjection
			projErr := proj.UnmarshalZJSON(data)
			if (projErr == nil) != wantValid {
				t.Errorf("EventProjection.UnmarshalZJSON: err=%v (ok=%v), json.Valid=%v", projErr, projErr == nil, wantValid)
			}
		})
	}
}
