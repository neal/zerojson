package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// pool_fuzz_test.go is pool-pair fuzzing. pool_test.go's hand-picked cases (TestPooledDecoderReuse and its
// siblings) prove specific leakage shapes (nested-struct siblings,
// slice ghosts, map ghosts, slice growth) on hand-picked message pairs;
// this generalizes the same property — a pooled Decoder's second
// Decode/DecodeBorrow call must produce exactly what an independent
// fresh decode of the same bytes would, with zero residue from the
// prior call reusing the arena/slice-cell/map-cell — to arbitrary fuzzed
// message pairs, in both non-borrow and borrow mode.

// FuzzPoolPair decodes a then b through one pooled EventDecoder and
// checks both against independent fresh decodes: a's result (captured
// immediately, before b is decoded and reuses the arena) must match a
// fresh decode of a, and b's result must match a fresh decode of b —
// the second check is the one that actually catches arena/slice-cell
// leakage, since any residue from decoding a would only be visible in
// b's result once the shared arena/cells are reused.
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

// checkPoolPair runs the pool-pair check in the given mode (borrow or
// not) with a freshly pooled Decoder, so the two modes never share an
// arena with each other.
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

	// Decode b on the SAME pooled Decoder — this reuses (and may
	// resize/reclaim) the arena and any slice/map cells a's decode
	// populated. Any leakage from a into b would only surface here.
	var gotB Event
	errBPooled := decode(&pooled, b, &gotB)
	var freshB Event
	errBFresh := fresh(b, &freshB)
	if (errBPooled != nil) != (errBFresh != nil) {
		t.Fatalf("borrow=%v: error-agreement mismatch on b (after a decoded)\na: %q\nb: %q\npooled err: %v\nfresh err:  %v", borrow, a, b, errBPooled, errBFresh)
	}
	if errBPooled == nil && !reflect.DeepEqual(&freshB, &gotB) {
		t.Fatalf("borrow=%v: b's result (decoded after a on the same pooled Decoder) diverged from a fresh decode of b — arena/slice-cell leakage\na: %q\nb: %q\nfresh: %+v\npooled: %+v", borrow, a, b, &freshB, &gotB)
	}
}
