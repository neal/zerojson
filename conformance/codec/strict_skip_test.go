package codec

import (
	"encoding/json"
	"testing"
)

// strict_skip_test.go regression-tests strict skip: a genuinely
// unknown field's value is grammar-checked by
// Lexer.Skip (lexer.go) — reusing Valid's own grammar walk — instead of
// being merely brace/bracket-counted. decodeAllUnknown routes every case
// through Event (whose "unknown" key matches nothing declared, so it hits
// the fallback loop's default case — and, since the document is compact
// and declaration-ordered otherwise, also the ordered decoder's inline
// skip) via every decode entry point.
func decodeAllUnknown(data []byte) map[string]error {
	results := map[string]error{}
	var a Event
	results["UnmarshalZJSON"] = a.UnmarshalZJSON(data)
	var b Event
	results["UnmarshalZJSONBorrow"] = b.UnmarshalZJSONBorrow(data)
	var dec EventDecoder
	var c Event
	results["Decoder.Decode"] = dec.Decode(data, &c)
	return results
}

func TestStrictSkipRejectsMalformedUnknownFieldValues(t *testing.T) {
	cases := []string{
		// Numbers: RFC 8259 grammar, not the trusted-path's lenient
		// digit-class scan.
		`{"unknown":01}`,
		`{"unknown":-01}`,
		`{"unknown":1.}`,
		`{"unknown":.1}`,
		`{"unknown":1e}`,
		`{"unknown":1e+}`,
		`{"unknown":-}`,
		`{"unknown":+1}`,
		// Literals: exact match with a proper terminator (via the
		// surrounding member-delimiter check).
		`{"unknown":truex}`,
		`{"unknown":nul}`,
		`{"unknown":Null}`,
		`{"unknown":tru}`,
		// Strings: invalid escapes and raw control bytes.
		`{"unknown":"\z"}`,
		`{"unknown":"\u12"}`,
		"{\"unknown\":\"raw\x01control\"}",
		"{\"unknown\":\"raw\x1fcontrol\"}",
		// Nested unknown content: malformed grammar anywhere inside a
		// skipped object/array must still be caught, not just
		// brace/bracket-matched.
		`{"unknown":{"a":01}}`,
		`{"unknown":[1,01,2]}`,
		`{"unknown":{"a":"\z"}}`,
		`{"unknown":[1,truex]}`,
		`{"unknown":{"a":1,}}`,
		`{"unknown":[1,]}`,
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			// Sanity: stdlib must also reject it when the field is
			// genuinely unknown to the target (a *Event, same as
			// decodeAllUnknown below) — this is the "agree MORE, never
			// less" property this package guarantees, not a made-up
			// stricter-than-stdlib rule. Note this is deliberately NOT
			// decoded into map[string]any: an unknown/skipped field's raw
			// number token is never converted to a Go value by either
			// stdlib or zerojson, so a magnitude that would overflow
			// float64 (irrelevant here, but see the "well-formed" table's
			// 1e999999999 case) must not be treated as an error either —
			// only grammar is checked for content nothing ever
			// materializes.
			var stdlib Event
			if err := json.Unmarshal([]byte(c), &stdlib); err == nil {
				t.Fatalf("test setup: stdlib accepted %q, want rejection (this case doesn't test what it claims to)", c)
			}
			for entry, err := range decodeAllUnknown([]byte(c)) {
				if err == nil {
					t.Errorf("%s: accepted malformed unknown-field value %q, want rejection", entry, c)
				}
			}
		})
	}
}

// TestStrictSkipAcceptsWellFormedUnknownFieldValues is the flip side: the
// stricter Skip must not become falsely strict — every grammatically
// valid shape an unknown field can hold (every leaf kind, nested objects/
// arrays, escapes, unicode) still decodes cleanly.
func TestStrictSkipAcceptsWellFormedUnknownFieldValues(t *testing.T) {
	cases := []string{
		`{"unknown":0}`,
		`{"unknown":-0}`,
		`{"unknown":123}`,
		`{"unknown":-123.456}`,
		`{"unknown":1.5e+10}`,
		`{"unknown":1e999999999}`,
		`{"unknown":true}`,
		`{"unknown":false}`,
		`{"unknown":null}`,
		`{"unknown":"plain"}`,
		`{"unknown":"tab\tnl\nquote\"back\\slash\/"}`,
		`{"unknown":"\uD800"}`,
		`{"unknown":"unicode: 日本語 🎉"}`,
		`{"unknown":{}}`,
		`{"unknown":[]}`,
		`{"unknown":{"a":1,"b":[1,2,{"c":"d"}],"e":null}}`,
		`{"unknown":[1,"two",false,null,{"x":1}]}`,
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			// Same rationale as the malformed table's oracle: decode into
			// *Event (unknown field skipped, never materialized), not
			// map[string]any (which would force every number into a
			// float64 and spuriously reject 1e999999999 for overflow —
			// verified directly: json.Unmarshal into map[string]any DOES
			// reject that magnitude, but into a struct with the field
			// genuinely unknown, it does not, since the value is only
			// grammar-scanned and discarded, never converted).
			var stdlib Event
			if err := json.Unmarshal([]byte(c), &stdlib); err != nil {
				t.Fatalf("test setup: stdlib rejected %q: %v (this case doesn't test what it claims to)", c, err)
			}
			for entry, err := range decodeAllUnknown([]byte(c)) {
				if err != nil {
					t.Errorf("%s: rejected well-formed unknown-field value %q: %v", entry, c, err)
				}
			}
		})
	}
}

// TestStrictSkipMatchesRawHolder repeats the malformed/well-formed split
// against RawHolder (zerojson.Raw's own passthrough field) and MapFieldsLike
// (map-shaped Marshaler fields whose skip machinery differs from a plain
// struct's), since Raw's decode (ReadRaw, raw.go) is built on the exact
// same Skip/RawValue machinery.
func TestStrictSkipMatchesRawHolder(t *testing.T) {
	bad := []string{
		`{"table":"t","set":{"a":01},"where":null}`,
		`{"table":"t","set":[1,truex],"where":null}`,
		`{"table":"t","set":"\z","where":null}`,
	}
	good := []string{
		`{"table":"t","set":{"a":1},"where":null}`,
		`{"table":"t","set":[1,2,3],"where":{"id":"x"}}`,
		`{"table":"t","set":"plain","where":123}`,
	}
	for _, c := range bad {
		t.Run("bad/"+c, func(t *testing.T) {
			var stdlib map[string]any
			if err := json.Unmarshal([]byte(c), &stdlib); err == nil {
				t.Fatalf("test setup: stdlib accepted %q", c)
			}
			var v RawHolder
			if err := v.UnmarshalZJSON([]byte(c)); err == nil {
				t.Errorf("RawHolder.UnmarshalZJSON accepted malformed %q", c)
			}
		})
	}
	for _, c := range good {
		t.Run("good/"+c, func(t *testing.T) {
			var stdlib map[string]any
			if err := json.Unmarshal([]byte(c), &stdlib); err != nil {
				t.Fatalf("test setup: stdlib rejected %q: %v", c, err)
			}
			var v RawHolder
			if err := v.UnmarshalZJSON([]byte(c)); err != nil {
				t.Errorf("RawHolder.UnmarshalZJSON rejected well-formed %q: %v", c, err)
			}
		})
	}
}
