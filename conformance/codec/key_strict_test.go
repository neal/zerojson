package codec

import (
	"encoding/json"
	"testing"
)

// key_strict_test.go pins that object KEYS are grammar-strict on every
// decode path that reads a key (the general keyed fallback, the ordered
// decoder's skip of an unknown key, and map decode) — a raw control byte or
// an invalid escape in a key makes decode fail, matching json.Valid. A
// known field's own VALUE stays leniently parsed (the documented
// exception), so every case here puts the only potential grammar problem
// in a KEY, making decode-success exactly equivalent to json.Valid.
func TestDecodeKeyStrictMatchesValid(t *testing.T) {
	rawNL := "{\"bad\nkey\":1}"     // raw newline (0x0A) inside a key
	rawCtl := "{\"comm\x01ent\":1}" // raw 0x01 control byte inside a key
	cases := []struct {
		name string
		doc  string
	}{
		{"invalid_escape_unknown_key", `{"bad\qkey":1}`},
		{"raw_newline_in_key", rawNL},
		{"raw_control_in_key", rawCtl},
		{"invalid_escape_knownish_key", `{"comm\qent":"hi"}`},
		{"invalid_hex_escape_in_key", `{"a\uZZZZb":1}`},
		// Valid escapes in a key must still be accepted (json.Valid agrees):
		{"valid_backslash_escape_in_key", `{"a\\b":1}`},
		{"valid_tab_escape_in_key", `{"a\tb":1}`},
		{"valid_quote_escape_in_key", `{"a\"b":1}`},
		{"clean", `{"comment":"hi"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.doc)
			wantValid := json.Valid(data)

			// Event: the general keyed fallback path (an escaped/control key
			// never matches the ordered literal, so it falls through to it).
			var ev Event
			if (ev.UnmarshalZJSON(data) == nil) != wantValid {
				t.Errorf("Event.UnmarshalZJSON: decode-ok=%v != json.Valid=%v", ev.UnmarshalZJSON(data) == nil, wantValid)
			}
			// EventProjection declares only a few fields, so unknown keys here
			// route through the ordered decoder's unknown-key skip -> ReadKey.
			var pr EventProjection
			if (pr.UnmarshalZJSON(data) == nil) != wantValid {
				t.Errorf("EventProjection.UnmarshalZJSON: decode-ok != json.Valid=%v", wantValid)
			}
		})
	}
}

// TestDecodeMapKeyStrictMatchesValid covers the map-decode ReadKey path
// specifically (LegIndex is a map[string]EventLeg root), with EventLeg-shaped
// values so the only grammar variable is the key.
func TestDecodeMapKeyStrictMatchesValid(t *testing.T) {
	cases := []string{
		`{"bad\qkey":{}}`,   // invalid escape
		"{\"bad\nkey\":{}}", // raw control
		`{"a\\b":{}}`,       // valid escape
		`{"good":{}}`,       // clean
	}
	for _, doc := range cases {
		t.Run(doc, func(t *testing.T) {
			data := []byte(doc)
			wantValid := json.Valid(data)
			var li LegIndex
			if (li.UnmarshalZJSON(data) == nil) != wantValid {
				t.Errorf("LegIndex.UnmarshalZJSON(%q): decode-ok != json.Valid (%v)", doc, wantValid)
			}
		})
	}
}
