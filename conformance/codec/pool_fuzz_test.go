package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// FuzzPoolPair checks independent calls through one Decoder against fresh
// decoding, including that decoding the second message cannot change the first
// result. The stdlib-based ownership enumerations complement this path check.
func FuzzPoolPair(f *testing.F) {
	vs := variants()
	full, _ := json.Marshal(vs["full"])
	empty, _ := json.Marshal(vs["empty"])
	f.Add(full, empty)
	f.Add(empty, full)
	f.Add(full, full)
	f.Add(empty, empty)
	for _, seed := range orderedTrapSeeds {
		f.Add(full, []byte(seed))
		f.Add([]byte(seed), full)
	}

	f.Fuzz(func(t *testing.T, a, b []byte) {
		if hugeExponent(a) || hugeExponent(b) {
			return
		}
		checkPoolPair(t, a, b, false)
		checkPoolPair(t, a, b, true)
	})
}

// checkPoolPair exercises one Decoder in copy or borrow mode.
func checkPoolPair(t *testing.T, a, b []byte, borrow bool) {
	t.Helper()
	decode := func(d *EventDecoder, data []byte, v *Event) error {
		if borrow {
			return d.DecodeBorrow(data, v)
		}
		return d.Decode(data, v)
	}
	fresh := func(data []byte, v *Event) error {
		if borrow {
			return v.UnmarshalZJSONBorrow(data)
		}
		return v.UnmarshalZJSON(data)
	}

	var pooled EventDecoder

	var gotA Event
	errAPooled := decode(&pooled, a, &gotA)
	var freshA Event
	errAFresh := fresh(a, &freshA)
	if (errAPooled != nil) != (errAFresh != nil) {
		t.Fatalf("borrow=%v: error-agreement mismatch on a (before b decoded)\na: %q\npooled err: %v\nfresh err:  %v", borrow, a, errAPooled, errAFresh)
	}
	if errAPooled == nil && !reflect.DeepEqual(&freshA, &gotA) {
		t.Fatalf("borrow=%v: a's result (captured before b) diverged from a fresh decode of a\na: %q\nfresh: %+v\npooled: %+v", borrow, a, &freshA, &gotA)
	}

	// Decode b on the same Decoder while retaining a's result.
	var gotB Event
	errBPooled := decode(&pooled, b, &gotB)
	if errAPooled == nil && !reflect.DeepEqual(&freshA, &gotA) {
		t.Fatalf("borrow=%v: earlier result changed after decoding b", borrow)
	}
	var freshB Event
	errBFresh := fresh(b, &freshB)
	if (errBPooled != nil) != (errBFresh != nil) {
		t.Fatalf("borrow=%v: error-agreement mismatch on b (after a decoded)\na: %q\nb: %q\npooled err: %v\nfresh err:  %v", borrow, a, b, errBPooled, errBFresh)
	}
	if errBPooled == nil && !reflect.DeepEqual(&freshB, &gotB) {
		t.Fatalf("borrow=%v: b's result (decoded after a on the same pooled Decoder) diverged from a fresh decode of b — arena/slice-cell leakage\na: %q\nb: %q\nfresh: %+v\npooled: %+v", borrow, a, b, &freshB, &gotB)
	}
}
