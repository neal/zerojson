package zerojson

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

// TestReadStringMatchesStdlib validates escape handling — including
// UTF-16 surrogate pairs and lone surrogates — against encoding/json.
func TestReadStringMatchesStdlib(t *testing.T) {
	cases := []string{
		`"plain"`,
		`"tab\tnl\ncr\rquote\"back\\slash\/"`,
		`"Aé中"`,      // ASCII, Latin-1, CJK
		`"🎉"`,        // 🎉 surrogate pair
		`"pre🎉post"`, // pair mid-string
		`"😀😁"`,       // two pairs back to back
		`"\ud800"`,   // lone high surrogate
		`"\udc00"`,   // lone low surrogate
		`"\ud800x"`,  // high surrogate then normal char
		`"\ud800A"`,  // high surrogate then escaped non-surrogate
		`"🎉"`,        // uppercase hex
		`"mixed & raw & escaped"`,
	}
	for _, c := range cases {
		var want string
		if err := json.Unmarshal([]byte(c), &want); err != nil {
			t.Fatalf("stdlib rejected %s: %v", c, err)
		}
		l := NewLexer([]byte(c), false)
		got := l.ReadString()
		if err := l.Err(); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if got != want {
			t.Errorf("%s: want %q got %q", c, want, got)
		}
	}
}

// TestReadInterfaceDepthCap ensures deeply nested input errors instead of
// exhausting the stack.
func TestReadInterfaceDepthCap(t *testing.T) {
	deep := make([]byte, 0, 2*maxDepth+2)
	for range maxDepth + 5 {
		deep = append(deep, '[')
	}
	l := NewLexer(deep, false)
	l.ReadInterface()
	if l.Err() == nil {
		t.Fatal("expected depth-limit error, got nil")
	}

	// A reasonable nesting still succeeds.
	ok := []byte(`{"a":{"b":[{"c":1}]}}`)
	l2 := NewLexer(ok, false)
	l2.ReadInterface()
	if l2.Err() != nil {
		t.Fatalf("unexpected error on shallow input: %v", l2.Err())
	}
}

// TestReadUUIDBytesVectors validates the fixed-offset UUID fast path and
// its dep-free fallback (parseUUIDBytes) against precomputed vectors —
// captured once from github.com/google/uuid's ParseBytes across formats
// and malformed input, so the expectations stay pinned without the root
// module depending on that package. The full differential version (run
// live against uuid.ParseBytes) lives in the conformance module.
func TestReadUUIDBytesVectors(t *testing.T) {
	cases := []struct {
		in      string
		want    [16]byte
		wantErr bool
	}{
		{`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // canonical lowercase
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"B070C9C9-EDAA-4ADC-ADBE-CF1D2D66C1E1"`, // uppercase
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"B070c9C9-eDaa-4adC-adBe-cf1d2d66c1E1"`, // mixed case
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"00000000-0000-0000-0000-000000000000"`, [16]byte{}, false},
		{`"ffffffff-ffff-ffff-ffff-ffffffffffff"`,
			[16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, false},
		{`"{b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1}"`, // braced (fallback)
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"urn:uuid:b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // urn (fallback)
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"URN:UUID:b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // urn, uppercase prefix (fallback)
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"b070c9c9edaa4adcadbecf1d2d66c1e1"`, // no dashes (fallback)
			[16]byte{0xb0, 0x70, 0xc9, 0xc9, 0xed, 0xaa, 0x4a, 0xdc, 0xad, 0xbe, 0xcf, 0x1d, 0x2d, 0x66, 0xc1, 0xe1}, false},
		{`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1eg"`, [16]byte{}, true},   // bad hex
		{`"b070c9c9+edaa-4adc-adbe-cf1d2d66c1e1"`, [16]byte{}, true},   // bad dash
		{`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1"`, [16]byte{}, true},     // short
		{`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1ff"`, [16]byte{}, true}, // long
	}
	for _, c := range cases {
		l := NewLexer([]byte(c.in), false)
		got := l.ReadUUIDBytes()
		gotErr := l.Err() != nil

		if c.wantErr != gotErr {
			t.Errorf("%s: error mismatch: want err=%v got err=%v (%v)", c.in, c.wantErr, gotErr, l.Err())
			continue
		}
		if !c.wantErr && c.want != got {
			t.Errorf("%s: value mismatch: want %v got %v", c.in, c.want, got)
		}
		// The fast path must consume exactly the quoted token.
		if !gotErr && l.pos != len(c.in) {
			t.Errorf("%s: consumed %d of %d bytes", c.in, l.pos, len(c.in))
		}
	}
}

// TestReadTimeMatchesTimeParse validates the fixed-position RFC3339-Z fast
// path and its fallback against time.Parse.
func TestReadTimeMatchesTimeParse(t *testing.T) {
	cases := []string{
		`"2026-07-02T08:30:00Z"`,
		`"2026-07-02T08:30:00.1Z"`,
		`"2026-07-02T08:30:00.170079Z"`,
		`"2026-07-02T08:30:00.123456789Z"`,
		`"2026-07-02T08:30:00.1234567890Z"`, // 10 frac digits
		`"0001-01-01T00:00:00Z"`,
		`"9999-12-31T23:59:59.999999999Z"`,
		`"2024-02-29T00:00:00Z"`,      // leap day
		`"2023-02-29T00:00:00Z"`,      // not a leap year
		`"1900-02-29T00:00:00Z"`,      // century non-leap
		`"2000-02-29T00:00:00Z"`,      // 400-year leap
		`"2026-13-01T00:00:00Z"`,      // month 13
		`"2026-00-01T00:00:00Z"`,      // month 0
		`"2026-07-32T00:00:00Z"`,      // day 32
		`"2026-07-00T00:00:00Z"`,      // day 0
		`"2026-07-02T24:00:00Z"`,      // hour 24
		`"2026-07-02T08:60:00Z"`,      // minute 60
		`"2026-07-02T08:30:60Z"`,      // leap second
		`"2026-07-02T08:30:00+05:30"`, // offset (fallback)
		`"2026-07-02T08:30:00-04:00"`, // negative offset (fallback)
		`"2026-07-02t08:30:00Z"`,      // lowercase t
		`"2026-07-02T08:30:00z"`,      // lowercase z
		`"2026-07-02T08:30:00.Z"`,     // empty fraction
		`"2026-07-02 08:30:00Z"`,      // space separator
		// Pre-1970 dates and further year/leap boundaries, added when
		// ReadTime switched from time.Date to arithmetic days-since-epoch
		// + time.Unix (daysSinceUnixEpoch's Howard Hinnant branch handles
		// year < 1970).
		`"1969-12-31T23:59:59Z"`,   // one second before the epoch
		`"1970-01-01T00:00:00Z"`,   // the epoch itself
		`"1969-01-01T00:00:00Z"`,   // a full year before the epoch
		`"1900-01-01T00:00:00Z"`,   // pre-epoch century boundary
		`"1600-02-29T00:00:00Z"`,   // pre-epoch 400-year leap
		`"0004-02-29T00:00:00Z"`,   // early leap year
		`"1970-02-28T23:59:59.5Z"`, // day before a leap day, just after epoch
		`"1972-02-29T12:00:00Z"`,   // first leap year after the epoch
		`"2000-01-01T00:00:00Z"`,   // century-leap year boundary
		`"2000-12-31T23:59:59Z"`,   // last instant of a leap year
		`"2001-01-01T00:00:00Z"`,   // first instant after a leap year
	}
	for _, c := range cases {
		want, wantErr := time.Parse(time.RFC3339, c[1:len(c)-1])

		l := NewLexer([]byte(c), false)
		got := l.ReadTime()
		gotErr := l.Err()

		if (wantErr != nil) != (gotErr != nil) {
			t.Errorf("%s: error mismatch: time.Parse err=%v zerojson err=%v", c, wantErr, gotErr)
			continue
		}
		if wantErr == nil && !want.Equal(got) {
			t.Errorf("%s: value mismatch: want %v got %v", c, want, got)
		}
		if wantErr == nil && want.Location() != got.Location() && want.Location().String() != got.Location().String() {
			t.Errorf("%s: location mismatch: want %v got %v", c, want.Location(), got.Location())
		}
		// reflect.DeepEqual, not just .Equal: the fast path must produce
		// a time.Time identical in internal representation to time.Date
		// (via time.Parse here, which for a Z-suffixed RFC3339 value
		// with no monotonic reading involved constructs the same way),
		// not merely one that compares equal as an instant.
		if wantErr == nil && !reflect.DeepEqual(want, got) {
			t.Errorf("%s: representation mismatch (Equal but not DeepEqual): want %#v got %#v", c, want, got)
		}
	}
}

// TestParseDigitsBoundedMatchesNaive checks the SWAR digit parser against a
// naive loop across lengths and boundaries — including overflow:
// parseDigitsBounded rejects (ok=false) any value above limit instead of
// truncating it, so the naive oracle enforces the same limit. Exercised
// across several width limits so the high/low split of limit (and the
// batch-vs-tail transition) is covered at every boundary, not just
// MaxUint64.
func TestParseDigitsBoundedMatchesNaive(t *testing.T) {
	naive := func(b []byte, limit uint64) (uint64, bool) {
		if len(b) == 0 {
			return 0, false
		}
		var v uint64
		for _, c := range b {
			if c < '0' || c > '9' {
				return 0, false
			}
			d := uint64(c - '0')
			// v*10 + d <= limit, without overflowing the check itself.
			if v > limit/10 || (v == limit/10 && d > limit%10) {
				return 0, false
			}
			v = v*10 + d
		}
		return v, true
	}
	cases := []string{
		"0", "7", "42", "1234567", "12345678", "123456789",
		"1782981000170079000",  // 19-digit ns timestamp
		"9223372036854775807",  // MaxInt64
		"9223372036854775808",  // MaxInt64 + 1
		"18446744073709551615", // MaxUint64
		"18446744073709551616", // MaxUint64 + 1 (overflows uint64)
		"99999999999999999999", // 20 nines, > MaxUint64
		"255", "256", "65535", "65536", "4294967295", "4294967296",
		"2147483647", "2147483648",
		"00000001", "0000000000000000000042", "12a45678", "1234567x", "", "x",
		// The byte immediately past '9' ('0'+10 == ':', 0x3a): pins the
		// per-byte digit-range upper bound (c > 9) so a widened bound
		// (e.g. c > 10) that would wrongly admit ':' as a digit is caught.
		":", "1:",
	}
	limits := []uint64{
		math.MaxUint64, math.MaxInt64, math.MaxUint32, math.MaxInt32,
		math.MaxUint16, math.MaxUint8, 1 << 63,
	}
	for _, limit := range limits {
		for _, c := range cases {
			wv, wok := naive([]byte(c), limit)
			gv, gok := parseDigitsBounded([]byte(c), limit)
			if wok != gok || (wok && wv != gv) {
				t.Errorf("limit=%d %q: naive=(%d,%v) swar=(%d,%v)", limit, c, wv, wok, gv, gok)
			}
		}
	}
}

// TestReadIntWidthRangeChecks pins the width-aware integer readers: a value
// outside the named width is a decode error (matching encoding/json's range
// rejection), not a silently wrapped cast. Covers int/int32/uint16 overflow
// plus every width's positive and negative boundary, the quoted (,string)
// path, and negative-into-unsigned.
func TestReadIntWidthRangeChecks(t *testing.T) {
	check := func(name, in string, read func(*Lexer), wantErr bool) {
		t.Helper()
		l := NewLexer([]byte(in), false)
		read(&l)
		if (l.Err() != nil) != wantErr {
			t.Errorf("%s(%q): err=%v, wantErr=%v", name, in, l.Err(), wantErr)
		}
	}
	// In range (inclusive boundaries).
	check("Int8", "127", func(l *Lexer) { l.ReadInt8() }, false)
	check("Int8", "-128", func(l *Lexer) { l.ReadInt8() }, false)
	check("Int16", "32767", func(l *Lexer) { l.ReadInt16() }, false)
	check("Int16", "-32768", func(l *Lexer) { l.ReadInt16() }, false)
	check("Int32", "2147483647", func(l *Lexer) { l.ReadInt32() }, false)
	check("Int32", "-2147483648", func(l *Lexer) { l.ReadInt32() }, false)
	check("Int64", "9223372036854775807", func(l *Lexer) { l.ReadInt64() }, false)
	check("Int64", "-9223372036854775808", func(l *Lexer) { l.ReadInt64() }, false)
	// Platform-width int/uint: the bound follows the build's word size. On a
	// 64-bit build the int64/uint64 extremes are accepted and one-past errors;
	// on a 32-bit build the 32-bit extremes are the boundary. Deriving the
	// expectations from math.MaxInt/MaxUint keeps this honest on either.
	if math.MaxInt == math.MaxInt64 {
		check("Int", "9223372036854775807", func(l *Lexer) { l.ReadInt() }, false)
		check("Int", "-9223372036854775808", func(l *Lexer) { l.ReadInt() }, false)
		check("Int", "9223372036854775808", func(l *Lexer) { l.ReadInt() }, true)
		check("Uint", "18446744073709551615", func(l *Lexer) { l.ReadUint() }, false)
		check("Uint", "18446744073709551616", func(l *Lexer) { l.ReadUint() }, true)
	} else { // 32-bit
		check("Int", "2147483647", func(l *Lexer) { l.ReadInt() }, false)
		check("Int", "-2147483648", func(l *Lexer) { l.ReadInt() }, false)
		check("Int", "2147483648", func(l *Lexer) { l.ReadInt() }, true)
		check("Uint", "4294967295", func(l *Lexer) { l.ReadUint() }, false)
		check("Uint", "4294967296", func(l *Lexer) { l.ReadUint() }, true)
	}
	check("Uint", "-1", func(l *Lexer) { l.ReadUint() }, true)
	check("Uint8", "255", func(l *Lexer) { l.ReadUint8() }, false)
	check("Uint16", "65535", func(l *Lexer) { l.ReadUint16() }, false)
	check("Uint32", "4294967295", func(l *Lexer) { l.ReadUint32() }, false)
	check("Uint64", "18446744073709551615", func(l *Lexer) { l.ReadUint64() }, false)
	// Out of range: each width's neighbors.
	check("Int8", "128", func(l *Lexer) { l.ReadInt8() }, true)
	check("Int8", "-129", func(l *Lexer) { l.ReadInt8() }, true)
	check("Int16", "32768", func(l *Lexer) { l.ReadInt16() }, true)
	check("Int32", "2147483648", func(l *Lexer) { l.ReadInt32() }, true)
	check("Int32", "-2147483649", func(l *Lexer) { l.ReadInt32() }, true)
	check("Int64", "9223372036854775808", func(l *Lexer) { l.ReadInt64() }, true)
	check("Int64", "-9223372036854775809", func(l *Lexer) { l.ReadInt64() }, true)
	check("Uint8", "256", func(l *Lexer) { l.ReadUint8() }, true)
	check("Uint16", "65536", func(l *Lexer) { l.ReadUint16() }, true)
	check("Uint32", "4294967296", func(l *Lexer) { l.ReadUint32() }, true)
	check("Uint64", "18446744073709551616", func(l *Lexer) { l.ReadUint64() }, true)
	// Negative into unsigned always errors (stdlib does too).
	check("Uint16", "-1", func(l *Lexer) { l.ReadUint16() }, true)
	// The ordinary readers accept quoted or bare numbers. The strict quoted
	// readers used for json:",string" additionally reject a bare token.
	check("Int32 quoted", `"2147483648"`, func(l *Lexer) { l.ReadInt32() }, true)
	check("Int32 quoted", `"5"`, func(l *Lexer) { l.ReadInt32() }, false)
	check("QuotedInt32", `"5"`, func(l *Lexer) { l.ReadQuotedInt32() }, false)
	check("Int32 quoted plus", `"+00"`, func(l *Lexer) { l.ReadInt32() }, false)
	check("QuotedInt32 plus", `"+00"`, func(l *Lexer) { l.ReadQuotedInt32() }, true)
	check("QuotedUint32 plus", `"+001"`, func(l *Lexer) { l.ReadQuotedUint32() }, true)
	check("QuotedInt32 sign only", `"+"`, func(l *Lexer) { l.ReadQuotedInt32() }, true)
	check("QuotedInt32 bare", `5`, func(l *Lexer) { l.ReadQuotedInt32() }, true)

	// Value correctness at the extremes (not just error-vs-nil).
	if l := NewLexer([]byte("-128"), false); l.ReadInt8() != -128 || l.Err() != nil {
		t.Errorf("ReadInt8(-128) wrong: err=%v", l.Err())
	}
	if l := NewLexer([]byte("-9223372036854775808"), false); l.ReadInt64() != math.MinInt64 || l.Err() != nil {
		t.Errorf("ReadInt64(MinInt64) wrong: err=%v", l.Err())
	}
	if l := NewLexer([]byte("9223372036854775808"), false); l.ReadUint64() != 1<<63 || l.Err() != nil {
		t.Errorf("ReadUint64(1<<63) wrong: err=%v", l.Err())
	}
	if l := NewLexer([]byte(`"+001"`), false); l.ReadUint32() != 1 || l.Err() != nil {
		t.Errorf("ReadUint32(+001) wrong: err=%v", l.Err())
	}
}

// TestLoopProtocol checks EnterObject/NextMember and friends on
// whitespace-laden and empty documents.
func TestLoopProtocol(t *testing.T) {
	l := NewLexer([]byte(` { "a" : 1 , "b" : [ 1 , 2 ] , "c" : { } } `), false)
	v := l.ReadInterface()
	if l.Err() != nil {
		t.Fatal(l.Err())
	}
	m := v.(map[string]any)
	if m["a"] != 1.0 || len(m["b"].([]any)) != 2 || len(m["c"].(map[string]any)) != 0 {
		t.Fatalf("bad decode: %+v", m)
	}

	// Trailing commas error, matching stdlib.
	l2 := NewLexer([]byte(`{"a":1,}`), false)
	l2.ReadInterface()
	if l2.Err() == nil {
		t.Fatal("expected error for trailing comma")
	}
}
