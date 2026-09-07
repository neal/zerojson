package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neal/zerojson"
)

// rawHolderStd mirrors RawHolder with encoding/json.RawMessage in place
// of zerojson.Raw, for byte-identity comparison: json.RawMessage's
// MarshalJSON/UnmarshalJSON have exactly the contract AppendRaw/ReadRaw
// are built to match (nil encodes as null, everything else appended
// verbatim; UnmarshalJSON just captures the raw bytes).
type rawHolderStd struct {
	Table string           `json:"table,omitempty"`
	Set   json.RawMessage  `json:"set"`
	Where *json.RawMessage `json:"where,omitempty"`
}

// rawPayloads covers every JSON value shape Raw might capture: object,
// array (including nested), string (with an escape), number, and bool.
//
// A bare `"set":null` on this non-pointer field is covered by
// TestRawUnconditionalEmit: like json.RawMessage (whose UnmarshalJSON
// is invoked even for a null literal and so ends up holding the 4 bytes
// "null"), a value-typed Raw is delivered the null and holds those
// bytes. TestRawPointerNullClears covers null through the *Raw field,
// where it behaves exactly like every other pointer field (cleared).
var rawPayloads = []string{
	`{"a":1,"b":[1,2,3]}`,
	`[1,2,3]`,
	`[{"x":1},{"y":[true,false,null]}]`,
	`"a string with an escape: \"quoted\""`,
	`182.123456789`,
	`-42`,
	`true`,
}

// TestRawRoundTrip checks each payload shape decodes and re-encodes
// byte-identically to the same field typed as json.RawMessage.
func TestRawRoundTrip(t *testing.T) {
	for _, payload := range rawPayloads {
		t.Run(payload, func(t *testing.T) {
			data := []byte(`{"table":"items","set":` + payload + `}`)

			var std rawHolderStd
			if err := json.Unmarshal(data, &std); err != nil {
				t.Fatalf("stdlib rejected: %v", err)
			}
			wantEnc, err := json.Marshal(&std)
			if err != nil {
				t.Fatal(err)
			}

			var got RawHolder
			if err := got.UnmarshalZJSON(data); err != nil {
				t.Fatalf("zerojson rejected: %v", err)
			}
			if string(got.Set) != payload {
				t.Fatalf("captured span mismatch: got %q want %q", got.Set, payload)
			}
			gotEnc, err := got.AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wantEnc, gotEnc) {
				t.Fatalf("encode mismatch\nstdlib:   %s\nzerojson: %s", wantEnc, gotEnc)
			}

			// Re-decoding zerojson's own output must reproduce the same
			// Raw content (a full round trip, not just decode-once).
			var got2 RawHolder
			if err := got2.UnmarshalZJSON(gotEnc); err != nil {
				t.Fatalf("zerojson could not re-decode its own output: %v", err)
			}
			if !bytes.Equal(got.Set, got2.Set) {
				t.Fatalf("round-trip mismatch: %q vs %q", got.Set, got2.Set)
			}
		})
	}
}

// TestRawPointerNullClears checks the *zerojson.Raw field: present with
// a value, then cleared by null, matching encoding/json's pointer-field
// semantics for a reused target.
func TestRawPointerNullClears(t *testing.T) {
	base := []byte(`{"where":{"origin":"acme"}}`)
	null := []byte(`{"where":null}`)

	var std rawHolderStd
	if err := json.Unmarshal(base, &std); err != nil {
		t.Fatal(err)
	}
	var got RawHolder
	if err := got.UnmarshalZJSON(base); err != nil {
		t.Fatal(err)
	}
	if got.Where == nil || std.Where == nil {
		t.Fatalf("setup failed: std.Where=%v got.Where=%v", std.Where, got.Where)
	}
	if string(*got.Where) != string(*std.Where) {
		t.Fatalf("mismatch after base decode: got %q want %q", *got.Where, *std.Where)
	}

	if err := json.Unmarshal(null, &std); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalZJSON(null); err != nil {
		t.Fatal(err)
	}
	if std.Where != nil {
		t.Fatalf("test setup assumption broken: stdlib didn't clear Where on null")
	}
	if got.Where != nil {
		t.Fatalf("null did not clear *Raw field: %q", *got.Where)
	}
}

// TestRawBorrowAliasing verifies the documented borrow-mode contract:
// UnmarshalZJSONBorrow's Raw bytes alias the input buffer (mutating
// the input after decode is visible through Raw), while plain
// UnmarshalZJSON copies (mutating the input after decode is NOT
// visible).
func TestRawBorrowAliasing(t *testing.T) {
	const payload = `{"a":1}`
	// mutIndex points at the '1' inside the payload within data, so we
	// can flip it and check whether the decoded Raw sees the change.
	data := []byte(`{"table":"x","set":` + payload + `}`)
	mutIndex := bytes.LastIndexByte(data, '1')
	if mutIndex < 0 {
		t.Fatal("test data setup broken")
	}

	t.Run("borrow aliases", func(t *testing.T) {
		buf := append([]byte(nil), data...)
		var got RawHolder
		if err := got.UnmarshalZJSONBorrow(buf); err != nil {
			t.Fatal(err)
		}
		before := string(got.Set)
		buf[mutIndex] = '9'
		after := string(got.Set)
		if before == after {
			t.Fatalf("borrow mode did not alias input: before=%q after=%q (buf mutated)", before, after)
		}
		if after != `{"a":9}` {
			t.Fatalf("unexpected aliased content: %q", after)
		}
	})

	t.Run("copy does not alias", func(t *testing.T) {
		buf := append([]byte(nil), data...)
		var got RawHolder
		if err := got.UnmarshalZJSON(buf); err != nil {
			t.Fatal(err)
		}
		before := string(got.Set)
		buf[mutIndex] = '9'
		after := string(got.Set)
		if before != after {
			t.Fatalf("copy mode aliased input: before=%q after=%q", before, after)
		}
		if after != payload {
			t.Fatalf("unexpected content: %q", after)
		}
	})
}

// TestRawUnconditionalEmit checks Set (no omitempty tag; kCustomLeaf
// fields always emit, matching decimal.Decimal's existing convention,
// since a generic leaf's "emptiness" generally isn't determinable) is
// present even when nil, encoding as null like json.RawMessage(nil).
func TestRawUnconditionalEmit(t *testing.T) {
	var v RawHolder
	v.Table = "items"
	enc, err := v.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"table":"items","set":null}`)
	if !bytes.Equal(enc, want) {
		t.Fatalf("got %s want %s", enc, want)
	}

	// Set is a non-pointer Raw field: a JSON null is DELIVERED to it, so
	// it holds the four bytes null afterwards — exactly what
	// json.RawMessage (whose UnmarshalJSON is invoked with "null" by
	// encoding/json) ends up holding — and a pre-populated value is
	// replaced, not left stale.
	got := RawHolder{Set: zerojson.Raw(`{"stale":true}`)}
	if err := got.UnmarshalZJSON(enc); err != nil {
		t.Fatal(err)
	}
	if string(got.Set) != "null" {
		t.Fatalf("Set should hold the literal null after a null member: %q", got.Set)
	}
	var std struct {
		Set json.RawMessage `json:"set"`
	}
	if err := json.Unmarshal(enc, &std); err != nil || string(std.Set) != "null" {
		t.Fatalf("stdlib RawMessage oracle assumption broken: %q err=%v", std.Set, err)
	}
}

// TestRawMatchesZerojsonGenericLeafContract is a sanity check that
// AppendRaw/ReadRaw are reachable directly too (not just through
// generated code), matching the documented custom-leaf function
// signatures.
func TestRawMatchesZerojsonGenericLeafContract(t *testing.T) {
	var r zerojson.Raw
	if got := zerojson.AppendRaw(nil, &r); string(got) != "null" {
		t.Fatalf("AppendRaw(nil Raw) = %q, want null", got)
	}
	r = zerojson.Raw(`{"x":1}`)
	if got := zerojson.AppendRaw(nil, &r); string(got) != `{"x":1}` {
		t.Fatalf("AppendRaw = %q", got)
	}

	l := zerojson.NewLexer([]byte(`{"x":1} `), false)
	var got zerojson.Raw
	zerojson.ReadRaw(&l, &got)
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, zerojson.Raw(`{"x":1}`)) {
		t.Fatalf("ReadRaw = %q", got)
	}
}
