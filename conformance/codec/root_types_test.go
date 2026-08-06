package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// root_types_test.go exercises the generator's named slice/map ROOT support
// (cmd/zerojsongen/root.go): EventList ([]Event), LegPtrList ([]*EventLeg,
// covering []*T with null elements), and LegIndex (map[string]EventLeg).
//
// Oracle strategy mirrors the slice/map FIELD suites (slice_semantics_test.go,
// event_codec_test.go):
//
//   - Encode byte-identity is checked against encoding/json. The root loop
//     is a trivial structural wrapper ([ , ] / { , }) around per-element
//     encodings that are ALREADY pinned byte-identical to easyjson
//     (Event via EventEJ; EventLeg transitively, as Event.Items []EventLeg
//     is inside EventEJ's pinned output), so root byte-identity reduces to
//     that already-pinned element identity plus the wrapper. A map root
//     uses Go range order (matching easyjson; encoding/json sorts keys), so
//     its byte-identity check uses a single-entry map — the same convention
//     Event.Amounts uses — with multi-entry maps covered by round-trip.
//   - The nil->null / non-nil-empty->[]|{} encode rules and the
//     null->nil / []|{}->non-nil-empty decode rules match BOTH oracles.
//     encoding/json is checked directly here; easyjson's identical behavior
//     was confirmed by generating its codec for equivalent slice/map root
//     types (nil slice/map -> `out.RawString("null")`; a decoded `[]`/`{}`
//     -> a non-nil empty via `make(...)`/`T{}`; range order on map encode),
//     the same evidence slice_semantics_test.go records for slice FIELDS.

func borrowRoot[T interface {
	UnmarshalZJSON([]byte) error
	UnmarshalZJSONBorrow([]byte) error
}](v T, data []byte, borrow bool) error {
	if borrow {
		return v.UnmarshalZJSONBorrow(data)
	}
	return v.UnmarshalZJSON(data)
}

func TestRootNullAndMapReuseMatchStdlib(t *testing.T) {
	leg := fullEventLeg()
	stdList, zjList := EventList{*variants()["full"]}, EventList{*variants()["full"]}
	if err := json.Unmarshal([]byte("null"), &stdList); err != nil {
		t.Fatal(err)
	}
	if err := zjList.UnmarshalZJSON([]byte("null")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zjList, stdList) {
		t.Fatalf("slice-root null mismatch: std=%v zj=%v", stdList, zjList)
	}

	stdMap := LegIndex{"old": leg}
	zjMap := LegIndex{"old": leg}
	in := []byte(`{"new":{"ratio":12.34,"label":"x","mode":"up"}}`)
	if err := json.Unmarshal(in, &stdMap); err != nil {
		t.Fatal(err)
	}
	if err := zjMap.UnmarshalZJSON(in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zjMap, stdMap) {
		t.Fatalf("map-root reuse mismatch: std=%v zj=%v", stdMap, zjMap)
	}
}

// TestRootSliceEncodeByteIdentity: a slice root's AppendZJSON is
// byte-identical to encoding/json across nil, empty, and populated cases
// (including a null element in the []*T case).
func TestRootSliceEncodeByteIdentity(t *testing.T) {
	full := *variants()["full"]
	empty := *variants()["empty"]
	leg := fullEventLeg()

	t.Run("EventList", func(t *testing.T) {
		for name, v := range map[string]EventList{
			"nil":   nil,
			"empty": {},
			"one":   {full},
			"two":   {full, empty},
		} {
			t.Run(name, func(t *testing.T) {
				want, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				got, err := v.AppendZJSON(nil)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(want) {
					t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
				}
			})
		}
	})

	t.Run("LegPtrList", func(t *testing.T) {
		l2 := leg
		for name, v := range map[string]LegPtrList{
			"nil":            nil,
			"empty":          {},
			"with null elem": {&leg, nil, &l2},
		} {
			t.Run(name, func(t *testing.T) {
				want, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				got, err := v.AppendZJSON(nil)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(want) {
					t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
				}
			})
		}
	})
}

// TestRootMapEncodeByteIdentity: a map root's AppendZJSON is byte-identical
// to encoding/json for nil, empty, and single-entry maps (multi-entry
// differs only in key order — Go range vs stdlib sort — and is covered by
// round-trip below).
func TestRootMapEncodeByteIdentity(t *testing.T) {
	leg := fullEventLeg()
	for name, v := range map[string]LegIndex{
		"nil":          nil,
		"empty":        {},
		"single entry": {"ALPHA": leg},
	} {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			got, err := v.AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
			}
		})
	}
}

// TestRootRoundTrip: marshal a populated root, then decode via encoding/json
// and via zerojson (both copy and borrow) and require the decoded Go values
// to be reflect.DeepEqual — the same round-trip contract the struct suite
// uses, extended to multi-entry maps (where encode order differs but the
// decoded map does not).
func TestRootRoundTrip(t *testing.T) {
	full := *variants()["full"]
	empty := *variants()["empty"]
	leg := fullEventLeg()
	l2 := leg

	t.Run("EventList", func(t *testing.T) {
		src := EventList{full, empty, full}
		data, err := json.Marshal(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, borrow := range []bool{false, true} {
			var want EventList
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			var got EventList
			if err := borrowRoot(&got, data, borrow); err != nil {
				t.Fatalf("borrow=%v: %v", borrow, err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("borrow=%v mismatch\nstdlib:   %+v\nzerojson: %+v", borrow, want, got)
			}
		}
	})

	t.Run("LegPtrList", func(t *testing.T) {
		src := LegPtrList{&leg, nil, &l2}
		data, err := json.Marshal(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, borrow := range []bool{false, true} {
			var want LegPtrList
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			var got LegPtrList
			if err := borrowRoot(&got, data, borrow); err != nil {
				t.Fatalf("borrow=%v: %v", borrow, err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("borrow=%v mismatch\nstdlib:   %+v\nzerojson: %+v", borrow, want, got)
			}
		}
	})

	t.Run("LegIndex multi-entry", func(t *testing.T) {
		src := LegIndex{"ALPHA": leg, "BETA": l2, "GAMMA": fullEventLeg()}
		data, err := json.Marshal(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, borrow := range []bool{false, true} {
			var want LegIndex
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			var got LegIndex
			if err := borrowRoot(&got, data, borrow); err != nil {
				t.Fatalf("borrow=%v: %v", borrow, err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("borrow=%v mismatch\nstdlib:   %+v\nzerojson: %+v", borrow, want, got)
			}
		}
	})
}

// TestRootNullAndEmptyDecode pins the null/empty decode rules for roots
// against encoding/json: `null` -> nil; `[]`/`{}` -> non-nil empty.
func TestRootNullAndEmptyDecode(t *testing.T) {
	t.Run("slice null -> nil", func(t *testing.T) {
		var stdWant EventList
		if err := json.Unmarshal([]byte(`null`), &stdWant); err != nil {
			t.Fatal(err)
		}
		if stdWant != nil {
			t.Fatalf("stdlib sanity: null -> %#v, want nil", stdWant)
		}
		var got EventList = EventList{{}} // pre-populate to prove null clears
		if err := got.UnmarshalZJSON([]byte(`null`)); err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("null -> %#v, want nil", got)
		}
	})

	t.Run("slice [] -> non-nil empty", func(t *testing.T) {
		var got EventList
		if err := got.UnmarshalZJSON([]byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("[] -> %#v, want non-nil empty", got)
		}
		// Re-encode must be "[]", not "null" (which nil would produce).
		b, err := got.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]" {
			t.Fatalf("re-encoded empty slice root as %s, want []", b)
		}
	})

	t.Run("map null -> nil", func(t *testing.T) {
		var got LegIndex = LegIndex{"X": fullEventLeg()}
		if err := got.UnmarshalZJSON([]byte(`null`)); err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("null -> %#v, want nil", got)
		}
	})

	t.Run("map {} -> non-nil empty", func(t *testing.T) {
		var got LegIndex
		if err := got.UnmarshalZJSON([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("{} -> %#v, want non-nil empty", got)
		}
		b, err := got.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "{}" {
			t.Fatalf("re-encoded empty map root as %s, want {}", b)
		}
	})
}

// TestRootPooledDecode exercises the -pool Decoders for roots: a reused
// backing cell (slice) / cleared table (map) across decodes must still
// round-trip byte-identically, and must not leak elements/entries from a
// prior decode.
func TestRootPooledDecode(t *testing.T) {
	full := *variants()["full"]
	empty := *variants()["empty"]
	leg := fullEventLeg()

	t.Run("EventList reuse", func(t *testing.T) {
		var d EventListDecoder
		bigSrc := EventList{full, empty, full}
		bigData, _ := bigSrc.AppendZJSON(nil)
		smallSrc := EventList{empty}
		smallData, _ := smallSrc.AppendZJSON(nil)

		var big EventList
		if err := d.Decode(bigData, &big); err != nil {
			t.Fatal(err)
		}
		if len(big) != 3 {
			t.Fatalf("len = %d, want 3", len(big))
		}
		// Reuse the same Decoder for a shorter input: no ghost elements.
		var small EventList
		if err := d.Decode(smallData, &small); err != nil {
			t.Fatal(err)
		}
		if len(small) != 1 {
			t.Fatalf("reused-cell len = %d, want 1", len(small))
		}
		re, _ := small.AppendZJSON(nil)
		if string(re) != string(smallData) {
			t.Fatalf("reused-cell re-encode mismatch\nwant: %s\ngot:  %s", smallData, re)
		}
	})

	t.Run("LegIndex reuse", func(t *testing.T) {
		var d LegIndexDecoder
		aSrc := LegIndex{"A": leg, "B": leg}
		aData, _ := aSrc.AppendZJSON(nil)
		bSrc := LegIndex{"C": leg}
		bData, _ := bSrc.AppendZJSON(nil)

		var a LegIndex
		if err := d.Decode(aData, &a); err != nil {
			t.Fatal(err)
		}
		if len(a) != 2 {
			t.Fatalf("len = %d, want 2", len(a))
		}
		var b LegIndex
		if err := d.Decode(bData, &b); err != nil {
			t.Fatal(err)
		}
		if len(b) != 1 {
			t.Fatalf("reused-table len = %d, want 1 (no ghost keys)", len(b))
		}
		if _, ok := b["A"]; ok {
			t.Fatal("ghost key A leaked from prior decode")
		}
	})
}
