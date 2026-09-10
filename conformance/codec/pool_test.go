package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPooledDecoderReuse verifies a reused Decoder never leaks a field
// from a prior message — especially nested-struct siblings, which are the
// case that requires arena-cell reset.
func TestPooledDecoderReuse(t *testing.T) {
	// msgA populates the nested Meta fully; msgB omits Meta.Note, which
	// would leak from A if the arena cell weren't reset.
	msgA := []byte(`{"origin":"A","meta":{"note":"secret","cost":"1.5","internal":true},"count":7}`)
	msgB := []byte(`{"origin":"B","meta":{"cost":"2.5"},"count":9}`)

	var d EventDecoder

	var a Event
	if err := d.Decode(msgA, &a); err != nil {
		t.Fatal(err)
	}
	var b Event
	if err := d.Decode(msgB, &b); err != nil { // reuses the arena
		t.Fatal(err)
	}

	// b must match a fresh stdlib decode of msgB — no leakage from a.
	var want Event
	if err := json.Unmarshal(msgB, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &b) {
		t.Fatalf("pooled reuse leaked state\n want: %+v\n  got: %+v", &want, &b)
	}
	if b.Meta.Note != nil {
		t.Fatalf("nested sibling leaked: Meta.Note = %q", *b.Meta.Note)
	}
}

// TestPooledDecoderSliceReuseNoGhosts verifies the persistent slice cell a
// pooled Decoder reuses across calls doesn't leak elements: msgA has 3
// items, msgB has 1 — the result must have len 1, not 3 with 2 stale
// "ghost" elements left over from reslicing a longer previous backing
// array.
func TestPooledDecoderSliceReuseNoGhosts(t *testing.T) {
	msgA := []byte(`{"items":[` +
		`{"ratio":"1","label":"ALPHA","mode":"up"},` +
		`{"ratio":"2","label":"BETA","mode":"down"},` +
		`{"ratio":"3","label":"GAMMA","mode":"up"}` +
		`]}`)
	msgB := []byte(`{"items":[{"ratio":"5","label":"DELTA"}]}`)

	var d EventDecoder
	var a Event
	if err := d.Decode(msgA, &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 3 {
		t.Fatalf("setup failed: len(a.Items) = %d, want 3", len(a.Items))
	}

	var b Event
	if err := d.Decode(msgB, &b); err != nil { // reuses the arena's slice cell
		t.Fatal(err)
	}

	var want Event
	if err := json.Unmarshal(msgB, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &b) {
		t.Fatalf("pooled slice reuse leaked state\n want: %+v\n  got: %+v", &want, &b)
	}
	if len(b.Items) != 1 {
		t.Fatalf("ghost elements: len(b.Items) = %d, want 1 (%+v)", len(b.Items), b.Items)
	}
}

// TestPooledDecoderNestedInSliceNoLeak verifies that reusing a slice
// cell's backing array doesn't leak a nested struct element's fields: the
// leg at index 0 has "mode" set in msgA but not in msgB, and the reused
// slot must not retain msgA's Mode value.
func TestPooledDecoderNestedInSliceNoLeak(t *testing.T) {
	msgA := []byte(`{"items":[{"ratio":"1","label":"ALPHA","mode":"up"}]}`)
	msgB := []byte(`{"items":[{"ratio":"2","label":"BETA"}]}`) // mode omitted

	var d EventDecoder
	var a Event
	if err := d.Decode(msgA, &a); err != nil {
		t.Fatal(err)
	}
	if a.Items[0].Mode != ModeUp {
		t.Fatalf("setup failed: a.Items[0].Mode = %q, want %q", a.Items[0].Mode, ModeUp)
	}

	var b Event
	if err := d.Decode(msgB, &b); err != nil {
		t.Fatal(err)
	}
	var want Event
	if err := json.Unmarshal(msgB, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &b) {
		t.Fatalf("nested-in-slice leak\n want: %+v\n  got: %+v", &want, &b)
	}
	if b.Items[0].Mode != "" {
		t.Fatalf("nested field leaked into reused slot: Items[0].Mode = %q", b.Items[0].Mode)
	}

	// EventLeg contains a registered decimal leaf, so its slice keeps the
	// documented reset-on-reuse override rather than stdlib element merging.
	// Same-call reuse needs per-slot zeroing even when pooled-call entry
	// already clears private capacity.
	duplicate := []byte(`{"items":[{"ratio":"1","label":"ALPHA","mode":"up"}],"items":[{"ratio":"2","label":"BETA"}]}`)
	for _, borrow := range []bool{false, true} {
		var got Event
		decode := got.UnmarshalZJSON
		if borrow {
			decode = got.UnmarshalZJSONBorrow
		}
		if err := decode(duplicate); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Items, b.Items) {
			t.Fatalf("nested-in-slice leak on duplicate member: got=%+v want=%+v", got.Items, b.Items)
		}
	}
}

// TestPooledDecoderMapReuseNoGhosts verifies the persistent map cell a
// pooled Decoder reuses is clear()ed, not merged: msgA's Amounts has 2
// keys, msgB's has 1 different key — the result must have exactly msgB's
// key, not a union of both messages' keys.
func TestPooledDecoderMapReuseNoGhosts(t *testing.T) {
	msgA := []byte(`{"amounts":{"ALPHA":"12.5","BETA":"7.25"}}`)
	msgB := []byte(`{"amounts":{"GAMMA":"3.5"}}`)

	var d EventDecoder
	var a Event
	if err := d.Decode(msgA, &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Amounts) != 2 {
		t.Fatalf("setup failed: len(a.Amounts) = %d, want 2", len(a.Amounts))
	}

	var b Event
	if err := d.Decode(msgB, &b); err != nil { // reuses the arena's map cell
		t.Fatal(err)
	}
	var want Event
	if err := json.Unmarshal(msgB, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &b) {
		t.Fatalf("pooled map reuse leaked state\n want: %+v\n  got: %+v", &want, &b)
	}
	if len(b.Amounts) != 1 {
		t.Fatalf("ghost keys: len(b.Amounts) = %d, want 1 (%+v)", len(b.Amounts), b.Amounts)
	}
	if _, ok := b.Amounts["ALPHA"]; ok {
		t.Fatalf("stale key ALPHA leaked from prior decode: %+v", b.Amounts)
	}
}

// TestPooledDecoderSliceGrowsAcrossCalls verifies the reverse direction
// too: a slice cell must grow (not truncate/lose elements) when a later
// message has more elements than a prior one used the same Decoder for.
func TestPooledDecoderSliceGrowsAcrossCalls(t *testing.T) {
	msgSmall := []byte(`{"items":[{"ratio":"1","label":"ALPHA"}]}`)
	msgBig := []byte(`{"items":[` +
		`{"ratio":"1","label":"ALPHA"},` +
		`{"ratio":"2","label":"BETA"},` +
		`{"ratio":"3","label":"GAMMA"}` +
		`]}`)

	var d EventDecoder
	var small Event
	if err := d.Decode(msgSmall, &small); err != nil {
		t.Fatal(err)
	}
	var big Event
	if err := d.Decode(msgBig, &big); err != nil {
		t.Fatal(err)
	}
	var want Event
	if err := json.Unmarshal(msgBig, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &big) {
		t.Fatalf("pooled slice growth mismatch\n want: %+v\n  got: %+v", &want, &big)
	}
}

// TestPooledDecoderPtrStructReuseNoSubfieldGhosts verifies that reusing
// the arena cell backing a pointer-to-struct field (MetaPtr *EventMeta)
// doesn't leak that struct's OWN subfields into the next decode: msgA
// populates every field of MetaPtr (Note, Cost, Internal); msgB's
// meta_ptr is present but empty ({}), so every subfield of b.MetaPtr must
// come back zero, not retain a's values. This is the scalar-subfield
// analogue of TestPooledDecoderReuse (which covers a nested VALUE
// struct's sibling field, Event.Meta) and TestPooledDecoderNestedInSliceNoLeak
// (nested-in-slice) — neither exercises a reused *Struct cell, which is
// reset by a dedicated "a.vMetaPtr = EventMeta{}" statement the pool
// decoder emits (cmd/zerojsongen/decode.go) before decoding into the
// reused cell; removing that reset is caught deterministically here
// instead of only by FuzzPoolPair, which needs on the order of 11s of
// fuzzing to rediscover the {"meta_ptr":{}} shape.
func TestPooledDecoderPtrStructReuseNoSubfieldGhosts(t *testing.T) {
	msgA := []byte(`{"meta_ptr":{"note":"secret","cost":"1.5","internal":true}}`)
	msgB := []byte(`{"meta_ptr":{}}`) // present, but every subfield omitted

	check := func(t *testing.T, decode func(d *EventDecoder, data []byte, v *Event) error) {
		t.Helper()
		var d EventDecoder

		var a Event
		if err := decode(&d, msgA, &a); err != nil {
			t.Fatal(err)
		}
		if a.MetaPtr == nil || a.MetaPtr.Note == nil || *a.MetaPtr.Note != "secret" ||
			a.MetaPtr.Cost == nil || !a.MetaPtr.Internal {
			t.Fatalf("setup failed: a.MetaPtr = %+v", a.MetaPtr)
		}

		var b Event
		if err := decode(&d, msgB, &b); err != nil { // reuses the arena's *EventMeta cell
			t.Fatal(err)
		}

		var want Event
		if err := json.Unmarshal(msgB, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(&want, &b) {
			t.Fatalf("pooled ptr-struct reuse leaked state\n want: %+v\n  got: %+v", &want, &b)
		}
		if b.MetaPtr == nil {
			t.Fatal("b.MetaPtr is nil, want a non-nil *EventMeta (key was present)")
		}
		if b.MetaPtr.Note != nil {
			t.Errorf("subfield ghost: b.MetaPtr.Note = %q, want nil", *b.MetaPtr.Note)
		}
		if b.MetaPtr.Cost != nil {
			t.Errorf("subfield ghost: b.MetaPtr.Cost = %v, want nil", *b.MetaPtr.Cost)
		}
		if b.MetaPtr.Internal {
			t.Error("subfield ghost: b.MetaPtr.Internal = true, want false")
		}
	}

	t.Run("Decode", func(t *testing.T) {
		check(t, (*EventDecoder).Decode)
	})
	t.Run("DecodeBorrow", func(t *testing.T) {
		check(t, (*EventDecoder).DecodeBorrow)
	})
}

// TestPooledDecoderPtrStructReuseNullOrOmitted covers the two other shapes
// message B's meta_ptr can take on the same reused Decoder — explicit
// null and the key omitted entirely — matching the existing null
// semantics (v.MetaPtr = nil directly, never touching the arena cell) so
// this doesn't regress alongside the empty-object case above.
func TestPooledDecoderPtrStructReuseNullOrOmitted(t *testing.T) {
	msgA := []byte(`{"meta_ptr":{"note":"secret","cost":"1.5","internal":true}}`)
	cases := map[string][]byte{
		"null":    []byte(`{"meta_ptr":null}`),
		"omitted": []byte(`{}`),
	}

	for name, msgB := range cases {
		t.Run(name, func(t *testing.T) {
			for _, borrow := range []bool{false, true} {
				mode := "Decode"
				if borrow {
					mode = "DecodeBorrow"
				}
				t.Run(mode, func(t *testing.T) {
					var d EventDecoder

					var a Event
					var err error
					if borrow {
						err = d.DecodeBorrow(msgA, &a)
					} else {
						err = d.Decode(msgA, &a)
					}
					if err != nil {
						t.Fatal(err)
					}

					var b Event
					if borrow {
						err = d.DecodeBorrow(msgB, &b)
					} else {
						err = d.Decode(msgB, &b)
					}
					if err != nil {
						t.Fatal(err)
					}

					if b.MetaPtr != nil {
						t.Fatalf("%s: b.MetaPtr = %+v, want nil", name, b.MetaPtr)
					}
				})
			}
		})
	}
}

// TestPooledDecoderMatchesUnmarshal fuzz-free equivalence across variants.
func TestPooledDecoderMatchesUnmarshal(t *testing.T) {
	var d EventDecoder
	for name, tr := range variants() {
		data, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		var viaPool, viaPlain Event
		if err := d.Decode(data, &viaPool); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := viaPlain.UnmarshalZJSON(data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(&viaPool, &viaPlain) {
			t.Fatalf("%s: pooled != plain\n pool: %+v\nplain: %+v", name, &viaPool, &viaPlain)
		}
	}
}
