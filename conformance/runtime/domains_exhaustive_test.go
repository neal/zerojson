//go:build exhaustive

// This file holds the full-domain enumerations too large for the per-commit
// tier: the complete 2^32 int32/uint32 decode round-trips, the complete 2^32
// float32 encode sweep, the full 2048x2048 surrogate-pair decode grid, and
// the full 1e8 ParseEightDigits grid. They are the pre-RELEASE gate, not the
// per-commit gate — run with:
//
//	go test -tags exhaustive -timeout 30m ./conformance/runtime
//
// The enumerated domains are recorded in ../DOMAINS.md. Every check
// still uses an INDEPENDENT oracle: strconv formats what zerojson decodes
// (mutual-inverse check), strconv formats what zerojson encodes, and
// encoding/json decodes what zerojson decodes.
package runtimeconformance

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/neal/zerojson"
)

// TestInt32FullEnumeration decodes strconv.AppendInt(v) for EVERY int32 v
// and requires zerojson to recover exactly v with no error — strconv and
// zerojson must be mutual inverses across the entire signed 32-bit domain
// (4,294,967,296 values). Buffers are reused so the loop does no per-value
// heap allocation.
func TestInt32FullEnumeration(t *testing.T) {
	var buf [16]byte
	for v := int64(math.MinInt32); v <= math.MaxInt32; v++ {
		b := strconv.AppendInt(buf[:0], v, 10)
		l := zerojson.NewLexer(b, false)
		got := l.ReadInt32()
		if err := l.Err(); err != nil {
			t.Fatalf("decode %q: unexpected error %v", b, err)
		}
		if int64(got) != v {
			t.Fatalf("decode %q: got %d want %d", b, got, v)
		}
	}
}

// TestUint32FullEnumeration is the unsigned sibling: every uint32 value's
// decimal string decodes back to itself.
func TestUint32FullEnumeration(t *testing.T) {
	var buf [16]byte
	for v := uint64(0); v <= math.MaxUint32; v++ {
		b := strconv.AppendUint(buf[:0], v, 10)
		l := zerojson.NewLexer(b, false)
		got := l.ReadUint32()
		if err := l.Err(); err != nil {
			t.Fatalf("decode %q: unexpected error %v", b, err)
		}
		if uint64(got) != v {
			t.Fatalf("decode %q: got %d want %d", b, got, v)
		}
	}
}

// TestFloat32FullEnumeration encodes EVERY one of the 2^32 float32 bit
// patterns and checks AppendFloat32 byte-for-byte against strconv (the
// documented byte-identity target), asserting the NaN/Inf error contract on
// the non-finite patterns. Buffers are reused; the whole 2^32 space is
// walked, including both signed zeros, every denormal, and the exponent-
// field edges a strided sweep steps over.
func TestFloat32FullEnumeration(t *testing.T) {
	var gotBuf, wantBuf [40]byte
	for bitsU := uint64(0); bitsU <= math.MaxUint32; bitsU++ {
		f := math.Float32frombits(uint32(bitsU))
		got, err := zerojson.AppendFloat32(gotBuf[:0], f)
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			if err == nil {
				t.Fatalf("bits %#08x (%v): expected NaN/Inf error", bitsU, f)
			}
			continue
		}
		if err != nil {
			t.Fatalf("bits %#08x: unexpected error %v", bitsU, err)
		}
		want := strconv.AppendFloat(wantBuf[:0], float64(f), 'g', -1, 32)
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#08x: want %s got %s", bitsU, want, got)
		}
	}
}

// TestUnescapeSurrogatePairsFull enumerates the ENTIRE first-\u x second-\u
// surrogate grid (2048 x 2048 = 4,194,304 pairs over [D800,DFFF]^2),
// checking the decoded string value against encoding/json for every
// combination — the full space the normal tier covers only with
// representative low classes.
func TestUnescapeSurrogatePairsFull(t *testing.T) {
	// The 14-byte template "\uXXXX\uXXXX": quotes at 0 and 13, the two
	// backslash-u prefixes at 1..2 and 7..8, hi digits at 3..6, lo at 9..12.
	in := [14]byte{'"', '\\', 'u', '0', '0', '0', '0', '\\', 'u', '0', '0', '0', '0', '"'}
	const hex = "0123456789abcdef"
	put := func(off, v int) {
		in[off+0] = hex[(v>>12)&0xF]
		in[off+1] = hex[(v>>8)&0xF]
		in[off+2] = hex[(v>>4)&0xF]
		in[off+3] = hex[v&0xF]
	}
	for hi := 0xD800; hi <= 0xDFFF; hi++ {
		put(3, hi) // digits of the first \uXXXX start at index 3
		for lo := 0xD800; lo <= 0xDFFF; lo++ {
			put(9, lo) // digits of the second \uXXXX start at index 9
			var want string
			if err := json.Unmarshal(in[:], &want); err != nil {
				t.Fatalf("%s: stdlib unmarshal errored: %v", in[:], err)
			}
			l := zerojson.NewLexer(in[:], false)
			got := l.ReadString()
			if err := l.Err(); err != nil {
				t.Fatalf("%s: zerojson errored: %v", in[:], err)
			}
			if got != want {
				t.Fatalf("%s: want %q got %q", in[:], want, got)
			}
		}
	}
}

// TestParseEightDigitsFull walks the ENTIRE valid-word domain of
// ParseEightDigits: every value 0..99,999,999 spelled as 8 ASCII digits
// must parse back to itself, and IsEightDigits must accept every such word.
func TestParseEightDigitsFull(t *testing.T) {
	var buf [8]byte
	for n := uint64(0); n < 100_000_000; n++ {
		s := strconv.AppendUint(buf[:0], n, 10)
		// Left-pad to 8 digits in place.
		var w [8]byte
		pad := 8 - len(s)
		for i := 0; i < pad; i++ {
			w[i] = '0'
		}
		copy(w[pad:], s)
		word := binary.LittleEndian.Uint64(w[:])
		if !zerojson.IsEightDigits(word) {
			t.Fatalf("IsEightDigits(%q) = false", w[:])
		}
		if got := zerojson.ParseEightDigits(word); got != n {
			t.Fatalf("ParseEightDigits(%q) = %d want %d", w[:], got, n)
		}
	}
}
