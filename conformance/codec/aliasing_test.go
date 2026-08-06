package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// aliasing_test.go is a copy-mode aliasing guard. Copy-mode decode's whole contract is that the decoded struct
// owns its own memory — no string, slice, map, or Raw field may alias
// the input buffer, unlike borrow mode (which legitimately aliases and
// is deliberately not tested here; see raw_test.go's
// TestRawBorrowAliasing for the borrow-does/copy-doesn't contrast on a
// single field). The check: decode in copy mode, independently
// reference-decode the same original bytes via encoding/json into a
// snapshot that can never share memory with the input buffer (stdlib
// always allocates its own strings/slices/maps), scribble every byte of
// the input buffer, then assert the copy-mode result still matches the
// independent snapshot. If any field silently aliased the input, the
// scribble corrupts it and the comparison fails.

// scribble overwrites every byte of buf in place with 0xFF — a value
// that can never be part of any valid decoded field content the corpus
// below produces, so any residual aliasing is guaranteed to be visible.
func scribble(buf []byte) {
	for i := range buf {
		buf[i] = 0xFF
	}
}

// TestCopyModeDoesNotAliasInputBuffer sweeps every Event variant (every
// field kind: interned/plain strings, slices of structs and leaves,
// pointer slices, maps, nested structs, custom Marshalers) through the
// plain (non-pooled) copy-mode decode path.
func TestCopyModeDoesNotAliasInputBuffer(t *testing.T) {
	for name, tr := range variants() {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(tr)
			if err != nil {
				t.Fatal(err)
			}

			// Independent reference snapshot, decoded from its own
			// separately-allocated copy of data — stdlib's Unmarshal into
			// a fresh Event never shares backing memory with data, so
			// scribbling data below can never disturb want.
			want := new(Event)
			if err := json.Unmarshal(append([]byte(nil), data...), want); err != nil {
				t.Fatal(err)
			}

			buf := append([]byte(nil), data...)
			var got Event
			if err := got.UnmarshalZJSON(buf); err != nil {
				t.Fatal(err)
			}
			scribble(buf)

			if !reflect.DeepEqual(want, &got) {
				t.Fatalf("copy-mode decode aliased the input buffer\nwant (pre-scribble snapshot): %+v\ngot (post-scribble):          %+v", want, &got)
			}
		})
	}
}

// TestPooledCopyDecodeDoesNotAliasInputBuffer is the same guard for the
// pooled Decoder's non-borrow Decode: the arena/slice-cell reuse
// machinery is exactly the kind of place an accidental borrow could
// sneak in (e.g. a slice cell resliced from borrowed backing memory
// instead of copied), so it needs its own scribble check independent of
// the plain path above.
func TestPooledCopyDecodeDoesNotAliasInputBuffer(t *testing.T) {
	var d EventDecoder
	for name, tr := range variants() {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(tr)
			if err != nil {
				t.Fatal(err)
			}

			want := new(Event)
			if err := json.Unmarshal(append([]byte(nil), data...), want); err != nil {
				t.Fatal(err)
			}

			buf := append([]byte(nil), data...)
			var got Event
			if err := d.Decode(buf, &got); err != nil {
				t.Fatal(err)
			}
			scribble(buf)

			if !reflect.DeepEqual(want, &got) {
				t.Fatalf("pooled copy-mode Decode aliased the input buffer\nwant (pre-scribble snapshot): %+v\ngot (post-scribble):          %+v", want, &got)
			}
		})
	}
}

// TestRawHolderCopyModeDoesNotAliasInputBuffer covers zerojson.Raw
// specifically: ReadRaw's copy-mode path (bytes.Clone) is the one place
// most likely to regress into an accidental borrow, since the
// borrow-mode sibling path is a plain slice (re)assignment one line
// away in the same function (see raw.go's ReadRaw). Exercises both the
// plain and pooled non-borrow Decode entry points, full-buffer
// scribbled (a stronger check than raw_test.go's single-byte-flip
// TestRawBorrowAliasing, which targets one field one way).
func TestRawHolderCopyModeDoesNotAliasInputBuffer(t *testing.T) {
	data := []byte(`{"table":"records","set":{"a":1,"b":[1,2,{"c":"d"}]},"where":{"id":"x"}}`)

	t.Run("plain", func(t *testing.T) {
		// zerojson.Raw is a []byte, which stdlib would base64-decode as
		// bytes rather than capture a JSON span — not a valid oracle here
		// (see rawHolderStd in raw_test.go for the json.RawMessage-typed
		// mirror stdlib comparisons use instead). This test only needs an
		// aliasing check, not a byte-identity oracle: capture the
		// zerojson-decoded content itself as the pre-scribble reference.
		buf := append([]byte(nil), data...)
		var got RawHolder
		if err := got.UnmarshalZJSON(buf); err != nil {
			t.Fatal(err)
		}
		wantSet := string(got.Set)
		var wantWhere string
		if got.Where != nil {
			wantWhere = string(*got.Where)
		}
		scribble(buf)
		if string(got.Set) != wantSet {
			t.Fatalf("Set aliased input: before=%q after=%q", wantSet, got.Set)
		}
		if got.Where != nil && string(*got.Where) != wantWhere {
			t.Fatalf("Where aliased input: before=%q after=%q", wantWhere, *got.Where)
		}
	})

	t.Run("pooled", func(t *testing.T) {
		var d RawHolderDecoder
		buf := append([]byte(nil), data...)
		var got RawHolder
		if err := d.Decode(buf, &got); err != nil {
			t.Fatal(err)
		}
		wantSet := string(got.Set)
		var wantWhere string
		if got.Where != nil {
			wantWhere = string(*got.Where)
		}
		scribble(buf)
		if string(got.Set) != wantSet {
			t.Fatalf("Set aliased input (pooled): before=%q after=%q", wantSet, got.Set)
		}
		if got.Where != nil && string(*got.Where) != wantWhere {
			t.Fatalf("Where aliased input (pooled): before=%q after=%q", wantWhere, *got.Where)
		}
	})
}
