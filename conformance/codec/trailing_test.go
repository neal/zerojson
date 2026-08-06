package codec

import (
	"bytes"
	"encoding/json"
	"testing"
)

// trailing_test.go checks trailing-data rejection: every top-level decode
// entry point must reject content after the top-level value (garbage, or a
// second value) while still accepting trailing whitespace, matching
// encoding/json.Unmarshal.
//
// eventBase is compact and declaration-ordered (AppendZJSON's own output
// always is), so every case here exercises the ordered fast path, not
// just the fallback decoder — the trailing check lives in the shared
// wrapper both paths return through (see decode.go's unmarshalZJSON),
// not in either decode path itself, so this also covers the
// ordered-fast-path-then-garbage case.
func eventBase(t *testing.T) []byte {
	t.Helper()
	b, err := variants()["full"].AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// eventEntryPoints decodes data through every top-level Event decode
// entry point (plain copy, borrow, pooled copy, pooled borrow) and
// returns each one's error, keyed by entry point name.
func eventEntryPoints(data []byte) map[string]error {
	results := map[string]error{}

	var a Event
	results["UnmarshalZJSON"] = a.UnmarshalZJSON(data)

	var b Event
	results["UnmarshalZJSONBorrow"] = b.UnmarshalZJSONBorrow(data)

	var dec1 EventDecoder
	var c Event
	results["Decoder.Decode"] = dec1.Decode(data, &c)

	var dec2 EventDecoder
	var d Event
	results["Decoder.DecodeBorrow"] = dec2.DecodeBorrow(data, &d)

	return results
}

func TestTrailingDataRejectedAllEntryPoints(t *testing.T) {
	base := eventBase(t)
	if !json.Valid(base) {
		t.Fatalf("setup: base fixture is not valid JSON: %s", base)
	}

	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	cases := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"value_only", base, false},
		{"value_plus_garbage", cat(base, []byte("garbage")), true},
		{"value_plus_one_byte_garbage", cat(base, []byte("x")), true},
		{"value_plus_whitespace_space", cat(base, []byte("   ")), false},
		{"value_plus_whitespace_mixed", cat(base, []byte(" \t\n\r\n ")), false},
		{"value_plus_second_value", cat(base, base), true},
		{"value_plus_comma_second_value", cat(base, []byte(","), base), true},
		{"empty_input", []byte{}, true},
		{"whitespace_only", []byte("   \t\n"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for entry, err := range eventEntryPoints(tc.data) {
				if (err != nil) != tc.wantErr {
					t.Errorf("%s: err=%v, want error=%v (input %q)", entry, err, tc.wantErr, tc.data)
				}
			}
			// Cross-check against the oracle this behavior is modeled on:
			// stdlib rejects garbage/second-values and empty/whitespace-only
			// input the same way, and accepts trailing whitespace the same
			// way.
			var v map[string]any
			stdlibErr := json.Unmarshal(tc.data, &v)
			if (stdlibErr != nil) != tc.wantErr {
				t.Errorf("stdlib disagrees with the test's own wantErr for %q: stdlib err=%v, wantErr=%v", tc.data, stdlibErr, tc.wantErr)
			}
		})
	}
}

// TestTrailingDataRejectedEventLeg repeats the core cases (garbage,
// trailing whitespace, empty input) against EventLeg — an all-leaf-kind
// type distinct from Event's exotic-field coverage — for a second,
// differently-shaped entry point.
func TestTrailingDataRejectedEventLeg(t *testing.T) {
	leg := fullEventLeg()
	base, err := leg.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	run := func(name string, data []byte, wantErr bool) {
		t.Run(name, func(t *testing.T) {
			var a EventLeg
			if err := a.UnmarshalZJSON(data); (err != nil) != wantErr {
				t.Errorf("UnmarshalZJSON: err=%v, want error=%v", err, wantErr)
			}
			var b EventLeg
			if err := b.UnmarshalZJSONBorrow(data); (err != nil) != wantErr {
				t.Errorf("UnmarshalZJSONBorrow: err=%v, want error=%v", err, wantErr)
			}
			var dec EventLegDecoder
			var c EventLeg
			if err := dec.Decode(data, &c); (err != nil) != wantErr {
				t.Errorf("Decoder.Decode: err=%v, want error=%v", err, wantErr)
			}
			var dec2 EventLegDecoder
			var d EventLeg
			if err := dec2.DecodeBorrow(data, &d); (err != nil) != wantErr {
				t.Errorf("Decoder.DecodeBorrow: err=%v, want error=%v", err, wantErr)
			}
		})
	}

	run("value_only", base, false)
	run("value_plus_garbage", append(bytes.Clone(base), "not json"...), true)
	run("value_plus_whitespace", append(bytes.Clone(base), " \n\t"...), false)
	run("value_plus_second_value", append(bytes.Clone(base), base...), true)
	run("empty_input", []byte{}, true)
	run("whitespace_only", []byte(" \t\n"), true)
}
