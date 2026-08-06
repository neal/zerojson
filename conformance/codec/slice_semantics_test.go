package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestDecodeEmptyArrayYieldsNonNilSlice pins that decoding a JSON `[]` into
// a slice field produces a non-nil, zero-length slice, not nil — matching
// both oracles:
//
//   - encoding/json: decodes `[]` into a non-nil len-0 slice (the primary
//     decode contract per this project's precedence rules).
//   - easyjson: its generated decode for a slice field yields `[]T{}` for
//     an empty wire array (e.g. `out.InvalidLabels = []string{}`).
//
// The two oracles agree, so this is not a documented divergence. The
// generated slice decoder must force the arena's slice cell non-nil after
// the decode loop (which EnterArray skips entirely for `[]`) — in the
// fallback decoder, the ordered decoder (same emitDecode-generated
// field-value code), and the -pool arena's first use.
//
// This test covers all three declared slice-kind fields on Event —
// Items ([]EventLeg, kSlice, value elements), PtrItems ([]*EventLeg,
// kSlice, pointer elements), and Names ([]string, kLeafSlice) — through
// the fallback decoder directly, the public (ordered-first) path, and a
// pooled Decoder's first-ever use.
func TestDecodeEmptyArrayYieldsNonNilSlice(t *testing.T) {
	// Compact, declaration-ordered so the public path exercises the
	// ordered fast path (Items, PtrItems, Names appear in that relative
	// order on Event already).
	data := []byte(`{"items":[],"ptr_items":[],"names":[]}`)

	assertNonNilEmpty := func(t *testing.T, label string, tr *Event) {
		t.Helper()
		if tr.Items == nil || len(tr.Items) != 0 {
			t.Errorf("%s: Items = %#v, want non-nil empty", label, tr.Items)
		}
		if tr.PtrItems == nil || len(tr.PtrItems) != 0 {
			t.Errorf("%s: PtrItems = %#v, want non-nil empty", label, tr.PtrItems)
		}
		if tr.Names == nil || len(tr.Names) != 0 {
			t.Errorf("%s: Names = %#v, want non-nil empty", label, tr.Names)
		}
	}

	var want Event
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	assertNonNilEmpty(t, "stdlib oracle (sanity check)", &want)

	t.Run("fallback", func(t *testing.T) {
		got, err := decodeFallbackOnly(data, false)
		if err != nil {
			t.Fatal(err)
		}
		assertNonNilEmpty(t, "fallback", &got)
		if !reflect.DeepEqual(&want, &got) {
			t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
		}
	})

	t.Run("public (ordered-first)", func(t *testing.T) {
		var got Event
		if err := got.UnmarshalZJSON(data); err != nil {
			t.Fatal(err)
		}
		assertNonNilEmpty(t, "public", &got)
		if !reflect.DeepEqual(&want, &got) {
			t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
		}
	})

	t.Run("public borrow", func(t *testing.T) {
		var got Event
		if err := got.UnmarshalZJSONBorrow(data); err != nil {
			t.Fatal(err)
		}
		assertNonNilEmpty(t, "borrow", &got)
		if !reflect.DeepEqual(&want, &got) {
			t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
		}
	})

	t.Run("pooled first use", func(t *testing.T) {
		var d EventDecoder
		var got Event
		if err := d.Decode(data, &got); err != nil {
			t.Fatal(err)
		}
		assertNonNilEmpty(t, "pooled first use (copy)", &got)
		if !reflect.DeepEqual(&want, &got) {
			t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
		}
	})

	t.Run("pooled first use borrow", func(t *testing.T) {
		var d EventDecoder
		var got Event
		if err := d.DecodeBorrow(data, &got); err != nil {
			t.Fatal(err)
		}
		assertNonNilEmpty(t, "pooled first use (borrow)", &got)
		if !reflect.DeepEqual(&want, &got) {
			t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
		}
	})
}

// TestDecodeEmptyArrayVsNullVsAbsent distinguishes the three cases a
// slice field can be in on the wire, all of which must match
// encoding/json exactly: `[]` (non-nil, len 0), `null` (nil, and clears
// an already-populated field), and absent (nil, since the fixture never
// touches the field).
func TestDecodeEmptyArrayVsNullVsAbsent(t *testing.T) {
	t.Run("absent leaves nil", func(t *testing.T) {
		var got Event
		if err := got.UnmarshalZJSON([]byte(`{"origin":"a"}`)); err != nil {
			t.Fatal(err)
		}
		if got.Items != nil || got.PtrItems != nil || got.Names != nil {
			t.Fatalf("absent field(s) not nil: Items=%#v PtrItems=%#v Names=%#v", got.Items, got.PtrItems, got.Names)
		}
	})

	t.Run("empty array yields non-nil empty", func(t *testing.T) {
		var got Event
		if err := got.UnmarshalZJSON([]byte(`{"items":[],"ptr_items":[],"names":[]}`)); err != nil {
			t.Fatal(err)
		}
		if got.Items == nil || got.PtrItems == nil || got.Names == nil {
			t.Fatalf("empty array field(s) came back nil: Items=%#v PtrItems=%#v Names=%#v", got.Items, got.PtrItems, got.Names)
		}
		if len(got.Items) != 0 || len(got.PtrItems) != 0 || len(got.Names) != 0 {
			t.Fatalf("empty array field(s) non-empty: Items=%#v PtrItems=%#v Names=%#v", got.Items, got.PtrItems, got.Names)
		}
	})

	t.Run("null clears an already-populated field", func(t *testing.T) {
		base := []byte(`{"items":[{"label":"ALPHA"}],"ptr_items":[{"label":"ALPHA"}],"names":["a"]}`)
		var got Event
		if err := got.UnmarshalZJSON(base); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || len(got.PtrItems) != 1 || len(got.Names) != 1 {
			t.Fatalf("setup failed: %+v", got)
		}
		if err := got.UnmarshalZJSON([]byte(`{"items":null,"ptr_items":null,"names":null}`)); err != nil {
			t.Fatal(err)
		}
		if got.Items != nil || got.PtrItems != nil || got.Names != nil {
			t.Fatalf("null did not clear: Items=%#v PtrItems=%#v Names=%#v", got.Items, got.PtrItems, got.Names)
		}
	})
}

// TestDecodeEmptyArrayReencodeFidelity checks the round-trip: encoding/
// json.Marshal of a nil slice produces "null", but of a non-nil empty
// slice produces "[]" — so a decode of `[]` that yielded nil would turn
// into "null" on re-encode instead of round-tripping as "[]". This checks
// the decoded Go value directly with encoding/json (independent of Event's
// own omitempty tags on these fields, which would otherwise hide a
// nil-vs-empty difference by omitting the field either way).
func TestDecodeEmptyArrayReencodeFidelity(t *testing.T) {
	var got Event
	if err := got.UnmarshalZJSON([]byte(`{"items":[],"ptr_items":[],"names":[]}`)); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		v    any
	}{
		{"Items", got.Items},
		{"PtrItems", got.PtrItems},
		{"Names", got.Names},
	} {
		b, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(b) != "[]" {
			t.Errorf("%s: re-encoded as %s, want [] (nil slice would encode as null)", tc.name, b)
		}
	}
}

// TestOmitZeroEmptySliceRoundTrips extends
// TestOmitZeroByteIdenticalToStdlib's now-fixed gap: decoding a bare "[]"
// into OmitZeroCases.EmptySlice must produce a non-nil, zero-length
// slice, both matching encoding/json and round-tripping back through
// omitzero's "non-nil empty is not zero" gate (so it's emitted again as
// "[]", not omitted).
func TestOmitZeroEmptySliceRoundTrips(t *testing.T) {
	data := []byte(`{"empty_slice":[]}`)

	var want OmitZeroCases
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var got OmitZeroCases
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &got) {
		t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
	}
	if got.EmptySlice == nil {
		t.Fatal("EmptySlice came back nil, want non-nil empty")
	}

	// Round-trip: re-encoding must still emit "[]" (omitzero's gate is on
	// nil, not len==0 — this was already correct on encode; this pins
	// that the now-fixed decode feeds back into it correctly).
	reencoded, err := got.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	wantReencoded, err := json.Marshal(&want)
	if err != nil {
		t.Fatal(err)
	}
	if string(reencoded) != string(wantReencoded) {
		t.Fatalf("re-encode mismatch\nstdlib:   %s\nzerojson: %s", wantReencoded, reencoded)
	}
}

// TestPooledDecoderEmptyArrayFirstUse specifically covers a fresh -pool
// Decoder's very first Decode call: the arena's slice cell starts at its
// Go zero value (nil), identical to the non-pooled case. It guards against
// a non-nil-empty-slice fix that special-cases only the non-pooled arena,
// which would pass the other test's non-pooled assertions while still
// failing here.
func TestPooledDecoderEmptyArrayFirstUse(t *testing.T) {
	var d EventDecoder
	var got Event
	if err := d.Decode([]byte(`{"items":[],"ptr_items":[],"names":[]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Items == nil || got.PtrItems == nil || got.Names == nil {
		t.Fatalf("pooled first-use empty array field(s) nil: Items=%#v PtrItems=%#v Names=%#v", got.Items, got.PtrItems, got.Names)
	}

	// And still correct on a *second* call reusing the now-non-nil cell
	// with actual elements, then a *third* call back to empty — the cell
	// must go from non-nil/len>0 back to non-nil/len==0, not leak
	// elements (covered by TestPooledDecoderSliceReuseNoGhosts) and not
	// regress to nil.
	var got2 Event
	if err := d.Decode([]byte(`{"items":[{"label":"ALPHA"}]}`), &got2); err != nil {
		t.Fatal(err)
	}
	if len(got2.Items) != 1 {
		t.Fatalf("setup: len(got2.Items) = %d, want 1", len(got2.Items))
	}
	var got3 Event
	if err := d.Decode([]byte(`{"items":[]}`), &got3); err != nil {
		t.Fatal(err)
	}
	if got3.Items == nil || len(got3.Items) != 0 {
		t.Fatalf("pooled reused-cell empty array = %#v, want non-nil empty", got3.Items)
	}
}
