package codec

import (
	"encoding/json"
	"reflect"
	"testing"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"

	"github.com/neal/zerojson"
)

// root_leaf_test.go pins the named-slice-root-of-a-custom-leaf-element path
// (RawList = []zerojson.Raw, DecimalList = []decimal.Decimal). The root
// emit passes each element cell to the leaf's read func, which takes *T, so
// the element expression must be &cell[idx]; emitting cell[idx] produced
// uncompilable code (T where *T is required). That these packages compile
// at all is most of the regression; the round trips below confirm the
// generated codec is also correct, including null elements and borrow mode.

func TestRawListRootRoundTrip(t *testing.T) {
	src := RawList{
		zerojson.Raw(`123`),
		zerojson.Raw(`"a string"`),
		zerojson.Raw(`{"nested":[1,2,3]}`),
		nil, // encodes as null; decodes back to a nil Raw
		zerojson.Raw(`true`),
	}
	enc, err := src.AppendZJSON(nil)
	if err != nil {
		t.Fatalf("AppendZJSON: %v", err)
	}
	if want := `[123,"a string",{"nested":[1,2,3]},null,true]`; string(enc) != want {
		t.Fatalf("encode\n got: %s\nwant: %s", enc, want)
	}
	if !json.Valid(enc) {
		t.Fatalf("encoded RawList is not valid JSON: %s", enc)
	}

	for _, borrow := range []bool{false, true} {
		var got RawList
		var derr error
		if borrow {
			derr = got.UnmarshalZJSONBorrow(enc)
		} else {
			derr = got.UnmarshalZJSON(enc)
		}
		if derr != nil {
			t.Fatalf("borrow=%v decode: %v", borrow, derr)
		}
		reEnc, err := got.AppendZJSON(nil)
		if err != nil {
			t.Fatalf("borrow=%v re-encode: %v", borrow, err)
		}
		if string(reEnc) != string(enc) {
			t.Fatalf("borrow=%v round-trip mismatch\n got: %s\nwant: %s", borrow, reEnc, enc)
		}
	}
}

func TestDecimalListRootRoundTrip(t *testing.T) {
	src := DecimalList{
		decimal.NewFromInt(0),
		decimal.RequireFromString("185.50"),
		decimal.RequireFromString("-0.00123"),
		decimal.RequireFromString("9223371.999999999999"),
	}
	enc, err := src.AppendZJSON(nil)
	if err != nil {
		t.Fatalf("AppendZJSON: %v", err)
	}
	if !json.Valid(enc) {
		t.Fatalf("encoded DecimalList is not valid JSON: %s", enc)
	}

	// decimal.Decimal implements MarshalJSON/UnmarshalJSON, so encoding/json
	// is a valid oracle here.
	var stdWant DecimalList
	if err := json.Unmarshal(enc, &stdWant); err != nil {
		t.Fatalf("stdlib rejected zerojson output %s: %v", enc, err)
	}
	for _, borrow := range []bool{false, true} {
		var got DecimalList
		var derr error
		if borrow {
			derr = got.UnmarshalZJSONBorrow(enc)
		} else {
			derr = got.UnmarshalZJSON(enc)
		}
		if derr != nil {
			t.Fatalf("borrow=%v decode: %v", borrow, derr)
		}
		if !reflect.DeepEqual(stdWant, got) {
			t.Fatalf("borrow=%v mismatch\nstdlib:   %+v\nzerojson: %+v", borrow, stdWant, got)
		}
	}
}
