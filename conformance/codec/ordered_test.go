package codec

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neal/zerojson"
)

// decodeFallbackOnly decodes data via zjDecodeFallback_Event directly,
// bypassing the public UnmarshalZJSON entry point (and therefore the
// speculative ordered fast path entirely). Comparing this against the
// public path is the differential check for the ordered decoder: by
// construction, both must produce identical results for every input.
//
// It calls Lexer.Finish itself, mirroring exactly what the public
// entry point's wrapper does (see decode.go's unmarshalZJSON) — Finish
// lives in the wrapper, deliberately not in zjDecodeFallback_<T>/
// zjDecodeOrdered_<T> themselves (a nested/recursive decode must not
// trailing-check), so a bare zjDecodeFallback_Event call has no opinion
// on trailing data at all. Without this, "{}0" would (correctly)
// diverge: the public path rejects the trailing "0", a bare
// zjDecodeFallback_Event call ignores it — not a decode bug, just this
// helper not applying the same entry-point contract the comparison
// needs to be meaningful.
func decodeFallbackOnly(data []byte, borrow bool) (Event, error) {
	l := zerojson.NewLexer(data, borrow)
	var v Event
	// Mirror the entry-point wrapper's top-level null handling too: null is
	// a stdlib-style no-op success, checked before the object decode, so the
	// raw fallback function never sees a top-level null. IsNull consumes the
	// token when it matches (the generated zjDecode_<T> relies on the same),
	// so Finish alone follows it — no extra Skip.
	if l.IsNull() {
		l.Finish()
		return v, l.Err()
	}
	a := &zjArenaEvent{}
	zjDecodeFallback_Event(&l, &v, a)
	l.Finish()
	return v, l.Err()
}

// FuzzOrderedVsFallback is the differential fuzzer required for the
// ordered decode feature: it decodes fuzzer input via the public path
// (ordered decode, falling back on any deviation) and via the fallback
// decoder directly, and checks the results agree exactly — both on
// success (reflect.DeepEqual) and on failure (both err or neither).
func FuzzOrderedVsFallback(f *testing.F) {
	for _, tr := range variants() {
		b, _ := json.Marshal(tr)
		f.Add(b)
	}
	// Seeds that exercise the ordered matcher's trap conditions directly
	// (out-of-order, duplicate, escaped, unknown-interleaved, whitespace,
	// null mid-sequence, nested bail) in addition to whatever the fuzzer
	// discovers on its own.
	for _, seed := range orderedTrapSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if hugeExponent(data) {
			return
		}
		wantV, wantErr := decodeFallbackOnly(data, false)
		var gotV Event
		gotErr := gotV.UnmarshalZJSON(data)
		if (wantErr != nil) != (gotErr != nil) {
			t.Fatalf("error-agreement mismatch\ninput: %q\nfallback-only err: %v\npublic (ordered+fallback) err: %v", data, wantErr, gotErr)
		}
		if wantErr == nil && !reflect.DeepEqual(&wantV, &gotV) {
			t.Fatalf("decode divergence between fallback-only and public path\ninput: %q\nfallback-only: %+v\npublic:        %+v", data, &wantV, &gotV)
		}
	})
}

// orderedTrapSeeds are hand-picked documents that exercise each of the
// ordered matcher's documented trap conditions. TestOrderedTraps checks
// each against encoding/json directly (proving end-to-end correctness,
// not just internal agreement); they're also used to seed the fuzzer
// above so mutations continue to explore near them.
var orderedTrapSeeds = []string{
	`{"active":true,"origin":"acme"}`,                                       // out-of-order
	`{"origin":"first","origin":"second"}`,                                  // duplicate key, last wins
	`{"i\u0064":"11111111-1111-1111-1111-111111111111"}`,                    // escaped key that decodes to "id"
	`{"unknown1":123,"origin":"acme","unknown2":[1,2,3],"active":true}`,     // unknown keys interleaved (projection)
	`{"origin": "acme", "active":true}`,                                     // whitespace after colon
	`{ "origin":"acme","active":true}`,                                      // whitespace after brace
	`{"origin":"acme","active":true}`,                                       // omitempty subsequence (skips 1..33)
	`{"origin":"acme","location":null,"active":true}`,                       // null mid-sequence
	`{"origin":"acme","meta":{"internal":true,"cost":"1.5"},"active":true}`, // nested out-of-order -> bail propagates
	`{ }`,  // whitespace-padded empty object: must bail (IsDelim guard), not ReadKey the brace
	`{  }`, // same, wider padding — with ordered errors now final, a missing guard would reject valid input
}

// TestOrderedTraps checks each documented ordered-matcher trap against
// encoding/json directly, so correctness is verified end-to-end
// regardless of which internal path (ordered fast path or fallback)
// zerojson actually took.
func TestOrderedTraps(t *testing.T) {
	for _, data := range orderedTrapSeeds {
		t.Run(data, func(t *testing.T) {
			var want Event
			if err := json.Unmarshal([]byte(data), &want); err != nil {
				t.Fatalf("stdlib rejected seed: %v", err)
			}
			var got Event
			if err := got.UnmarshalZJSON([]byte(data)); err != nil {
				t.Fatalf("zerojson rejected seed: %v", err)
			}
			if !reflect.DeepEqual(&want, &got) {
				t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
			}
		})
	}
}

// TestOrderedNullMidSequenceClearsPointer specifically checks that a null
// appearing mid-sequence on the ordered fast path clears an
// already-populated pointer field, exactly as encoding/json does on a
// reused target — the null-mid-sequence trap, but starting from non-zero
// state (the zero-value case can't distinguish "cleared" from "never
// set").
func TestOrderedNullMidSequenceClearsPointer(t *testing.T) {
	base := []byte(`{"origin":"acme1","location":"ZONE1","active":true}`)
	patch := []byte(`{"origin":"acme2","location":null,"active":false}`)

	var want, got Event
	if err := json.Unmarshal(base, &want); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalZJSON(base); err != nil {
		t.Fatal(err)
	}
	if want.Location == nil || got.Location == nil {
		t.Fatalf("setup failed: want.Location=%v got.Location=%v", want.Location, got.Location)
	}

	if err := json.Unmarshal(patch, &want); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalZJSON(patch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &got) {
		t.Fatalf("mismatch after null patch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
	}
	if got.Location != nil {
		t.Fatalf("Location not cleared by mid-sequence null: %q", *got.Location)
	}
}

// TestOrderedNestedBailPropagates directly checks the nested-struct-bail
// trap: EventMeta's own ordered decode must bail on an out-of-order key,
// and that bail must propagate all the way out to Event's fallback
// (rather than, say, leaving Event's arena or fields half-written from
// the abandoned attempt) while still producing a fully correct result.
func TestOrderedNestedBailPropagates(t *testing.T) {
	data := []byte(`{"origin":"acme","meta":{"internal":true,"cost":"1.5","note":"n"},"active":true}`)
	var want Event
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &got) {
		t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
	}
	// Cross-check against the fallback-only path too: the whole point of
	// the ordered decoder is that both must agree, always.
	viaFallback, err := decodeFallbackOnly(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&viaFallback, &got) {
		t.Fatalf("public path diverged from fallback-only\nfallback-only: %+v\npublic:        %+v", &viaFallback, &got)
	}
}
