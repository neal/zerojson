package codec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// golden_test.go is a golden corpus of synthetic, generic-record-shaped
// fixtures — decoded via MapFieldsLike
// (types.go's stand-in for a common change-data-capture row shape: an Op
// string plus Set/Where map-shaped Marshaler payloads), covering three
// canonical operation shapes such a pipeline might produce: a record
// INSERT (Set only), a small record UPDATE (Set plus a single-key
// Where), and a wider summary UPDATE (a larger Set plus a compound-key
// Where). Every value is realistic-looking but entirely synthetic — no
// real customer, owner, or record data, and no resemblance to any
// specific production schema.
//
// Each fixture (testdata/golden/*.json) is checked in already in
// zerojson's own canonical output form — produced once by
// MapFieldsLike.AppendZJSON on a hand-built Go value, not hand-typed JSON —
// so the test's core property is trivial to state: decode, re-encode,
// and the result must be byte-identical to the checked-in file,
// forever. A pinned SHA-256 hash of the same (trailing-newline-trimmed)
// bytes is the second, independent guard: even a change that somehow
// left the round-trip self-consistent (decode(encode(x)) still equals
// encode(x) under some NEW encoding) would still change the hash and
// fail loudly, rather than silently establishing a new "canonical" form
// that the round-trip check alone couldn't catch.
var goldenFixtures = []struct {
	name string
	hash string // sha256 (hex) of the canonical, trailing-newline-trimmed file bytes
}{
	{"record_insert", "8e205d559c7cf91931165bf48072297707de9ef8268db095001f2671630b6431"},
	{"record_update", "54401e0abc8dae4afeb887331d6b2f8464ea4f5f14446883aacf1601e5d163cb"},
	{"summary_update", "7cca50ce245868f135982c9691065ef984abb5185e0e35b6087d59f33ba45c40"},
}

func TestGoldenCorpusDecodeReencodeByteIdentity(t *testing.T) {
	for _, fx := range goldenFixtures {
		t.Run(fx.name, func(t *testing.T) {
			path := filepath.Join("testdata", "golden", fx.name+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := bytes.TrimRight(raw, "\n")

			sum := sha256.Sum256(want)
			if got := hex.EncodeToString(sum[:]); got != fx.hash {
				t.Fatalf("%s: checked-in bytes' hash changed: got %s want %s (if this change is intentional, update goldenFixtures' pinned hash deliberately — this failure exists specifically so that doesn't happen silently)\nbytes: %s", path, got, fx.hash, want)
			}

			// Plain (non-pooled) copy-mode decode, and the pooled
			// borrow-mode decode, both round-trip byte-identically.
			var plain MapFieldsLike
			if err := plain.UnmarshalZJSON(want); err != nil {
				t.Fatalf("UnmarshalZJSON: %v", err)
			}
			gotPlain, err := plain.AppendZJSON(nil)
			if err != nil {
				t.Fatalf("AppendZJSON: %v", err)
			}
			if !bytes.Equal(want, gotPlain) {
				t.Fatalf("decode->re-encode (plain) diverged from golden\nwant: %s\ngot:  %s", want, gotPlain)
			}

			var d MapFieldsLikeDecoder
			var pooled MapFieldsLike
			if err := d.DecodeBorrow(append([]byte(nil), want...), &pooled); err != nil {
				t.Fatalf("pooled DecodeBorrow: %v", err)
			}
			gotPooled, err := pooled.AppendZJSON(nil)
			if err != nil {
				t.Fatalf("AppendZJSON (pooled): %v", err)
			}
			if !bytes.Equal(want, gotPooled) {
				t.Fatalf("decode->re-encode (pooled borrow) diverged from golden\nwant: %s\ngot:  %s", want, gotPooled)
			}
		})
	}
}
