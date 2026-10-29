package zerojson

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLexerFinishRejectsTrailingData(t *testing.T) {
	for _, data := range []string{"1 x", "1 true", "1]"} {
		l := NewLexer([]byte(data), false)
		l.ReadInt()
		l.Finish()
		if l.Err() == nil {
			t.Fatalf("Finish accepted trailing data: %q", data)
		}
	}
}

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

func TestLeafErrorExcerptsAreBounded(t *testing.T) {
	data := []byte(`"` + strings.Repeat("x", 1<<20) + `"`)
	for _, read := range []struct {
		name string
		fn   func(*Lexer)
	}{
		{"time", func(l *Lexer) { l.ReadTime() }},
		{"uuid", func(l *Lexer) { l.ReadUUIDBytes() }},
	} {
		t.Run(read.name, func(t *testing.T) {
			l := NewLexer(data, false)
			read.fn(&l)
			if l.Err() == nil {
				t.Fatal("expected invalid leaf error")
			}
			if text := l.Err().Error(); len(text) > 256 || !strings.Contains(text, "1048576 bytes") {
				t.Fatalf("unbounded or uninformative error: %d bytes", len(text))
			}
		})
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
	// The ordinary readers accept only a bare number token, like
	// encoding/json for a plain numeric field; the strict quoted readers
	// used for json:",string" accept only a string token. Neither accepts
	// a leading '+', which JSON's number grammar does not allow. Only the
	// explicitly lenient ReadQuotedOrBareInt64 (the ZeroJSONQuotedInt64
	// marker convention) takes both token kinds.
	check("Int32 quoted", `"2147483648"`, func(l *Lexer) { l.ReadInt32() }, true)
	check("Int32 quoted", `"5"`, func(l *Lexer) { l.ReadInt32() }, true)
	check("Int32 plus", `+5`, func(l *Lexer) { l.ReadInt32() }, true)
	check("Uint32 plus", `+001`, func(l *Lexer) { l.ReadUint32() }, true)
	check("Float64 quoted", `"5"`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 quoted NaN", `"NaN"`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 quoted Inf", `"Inf"`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 plus", `+5`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 leading dot", `.5`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 trailing dot", `1.`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 dot exponent", `1.e5`, func(l *Lexer) { l.ReadFloat64() }, true)
	check("Float64 leading zero (documented leniency)", `007`, func(l *Lexer) { l.ReadFloat64() }, false)
	check("Float64 ok", `-1.5e-3`, func(l *Lexer) { l.ReadFloat64() }, false)
	check("QuotedInt32", `"5"`, func(l *Lexer) { l.ReadQuotedInt32() }, false)
	check("QuotedInt32 plus", `"+00"`, func(l *Lexer) { l.ReadQuotedInt32() }, true)
	check("QuotedUint32 plus", `"+001"`, func(l *Lexer) { l.ReadQuotedUint32() }, true)
	check("QuotedInt32 sign only", `"+"`, func(l *Lexer) { l.ReadQuotedInt32() }, true)
	check("QuotedInt32 bare", `5`, func(l *Lexer) { l.ReadQuotedInt32() }, true)
	check("QuotedOrBareInt64 quoted", `"5"`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, false)
	check("QuotedOrBareInt64 bare", `5`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, false)
	check("QuotedOrBareInt64 bare plus", `+5`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, true)
	check("Float32 in range", `3.4e38`, func(l *Lexer) { l.ReadFloat32() }, false)
	check("Float32 out of range", `1e39`, func(l *Lexer) { l.ReadFloat32() }, true)
	check("Float32 just over max", `3.4028236e38`, func(l *Lexer) { l.ReadFloat32() }, true)
	check("Float32 quoted", `"1"`, func(l *Lexer) { l.ReadFloat32() }, true)
	check("Float32 underflow is fine (rounds to 0/denormal)", `1e-50`, func(l *Lexer) { l.ReadFloat32() }, false)
	// The marker convention's own UnmarshalJSON is conventionally
	// strconv.ParseInt, which takes a leading '+', so this reader does too.
	check("QuotedOrBareInt64 plus", `"+5"`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, false)
	check("QuotedOrBareInt64 plus-minus", `"+-5"`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, true)
	check("QuotedOrBareInt64 bare plus-minus", `+-5`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, true)
	check("QuotedOrBareInt64 quoted plus", `"+5"`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, false)
	check("QuotedOrBareInt64 sign only", `"+"`, func(l *Lexer) { l.ReadQuotedOrBareInt64() }, true)

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
	if l := NewLexer([]byte(`"001"`), false); l.ReadQuotedUint32() != 1 || l.Err() != nil {
		t.Errorf("ReadQuotedUint32(\"001\") wrong: err=%v", l.Err())
	}
	if l := NewLexer([]byte(`"-12"`), false); l.ReadQuotedOrBareInt64() != -12 || l.Err() != nil {
		t.Errorf("ReadQuotedOrBareInt64(\"-12\") wrong: err=%v", l.Err())
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

// TestJSONNumberAndQuotedNull pins the encoding/json.Number reader/writer
// (literal preserved verbatim; grammar-strict; quoted numeric string
// accepted as classic encoding/json does) and the `,string` quoted-null
// no-op.
func TestJSONNumberAndQuotedNull(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{`12`, "12", true}, {`-0.5e3`, "-0.5e3", true}, {`"12"`, "12", true},
		{`"1.5"`, "1.5", true}, {`01`, "", false}, {`"abc"`, "", false},
		{`"+1"`, "", false}, {`1.`, "", false}, {`true`, "", false}, {`""`, "", false},
		{`123456789012345678901234567890`, "123456789012345678901234567890", true},
	} {
		l := NewLexer([]byte(c.in), false)
		got := l.ReadJSONNumber()
		if (l.Err() == nil) != c.ok || got != c.want {
			t.Errorf("ReadJSONNumber(%s) = %q err=%v; want %q ok=%v", c.in, got, l.Err(), c.want, c.ok)
		}
	}
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"", "0", true}, {"12", "12", true}, {"-1e5", "-1e5", true}, {"01", "", false}, {"abc", "", false}, {"1.", "", false},
	} {
		out, err := AppendJSONNumber(nil, c.in)
		if (err == nil) != c.ok || (err == nil && string(out) != c.want) {
			t.Errorf("AppendJSONNumber(%q) = %q err=%v; want %q ok=%v", c.in, out, err, c.want, c.ok)
		}
	}
	for _, c := range []struct {
		in   string
		null bool
		pos  int
	}{
		{`"null"`, true, 6}, {` "null"`, true, 7}, {`"nul"`, false, 0}, {`null`, false, 0}, {`"null" `, true, 6}, {`"nullx"`, false, 0},
		{`"n\u0075ll"`, true, 11}, {`"\u006eull"`, true, 11}, {`"\u006e\u0075\u006c\u006c"`, true, 26}, {`"n\u0075lx"`, false, 0}, {`"5"`, false, 0},
	} {
		l := NewLexer([]byte(c.in), false)
		if got := l.IsQuotedNull(); got != c.null {
			t.Errorf("IsQuotedNull(%s) = %v want %v", c.in, got, c.null)
		} else if got && l.Pos() != c.pos {
			t.Errorf("IsQuotedNull(%s) left pos %d want %d", c.in, l.Pos(), c.pos)
		}
	}
}

// TestAddErrorFirstErrorWins hits the assignment branch of AddError (a delegated
// UnmarshalJSON error recorded into a fresh Lexer).
func TestAddErrorFirstErrorWins(t *testing.T) {
	var l Lexer
	l.AddError(errString("boom"))
	if l.Err() == nil || l.Err().Error() != "boom" {
		t.Fatalf("AddError did not record: %v", l.Err())
	}
	// A second error must not overwrite the first.
	l.AddError(errString("second"))
	if l.Err().Error() != "boom" {
		t.Fatalf("AddError overwrote the first error: %v", l.Err())
	}
	// A nil error is a no-op even on a clean lexer.
	var l2 Lexer
	l2.AddError(nil)
	if l2.Err() != nil {
		t.Fatalf("AddError(nil) set an error: %v", l2.Err())
	}
}

// TestLexerDelimiterHelpers exercises the two exported helpers that the
// generated code does not call but that remain public API for
// hand-written decoders.
func TestLexerDelimiterHelpers(t *testing.T) {
	l := NewLexer([]byte("  , x"), false)
	if !l.IsDelim(',') {
		t.Fatal("IsDelim(',') = false, want true (whitespace-skipped)")
	}
	if l.IsDelim('}') {
		t.Fatal("IsDelim('}') = true, want false")
	}
	l.WantComma() // consumes the comma
	if l.IsDelim(',') {
		t.Fatal("WantComma did not consume the comma")
	}
	// WantComma is a no-op when the next byte is not a comma.
	before := l.Pos()
	l.WantComma()
	if l.Pos() != before {
		t.Fatalf("WantComma consumed a non-comma byte: %d -> %d", before, l.Pos())
	}
}

// TestSkipPreservesExistingErrorPosition hits Skip's guard that returns immediately when
// the Lexer already carries an error.
func TestSkipPreservesExistingErrorPosition(t *testing.T) {
	l := NewLexer([]byte("123"), false)
	l.AddError(errString("pre-existing"))
	posBefore := l.Pos()
	l.Skip()
	if l.Pos() != posBefore {
		t.Fatalf("Skip advanced past an existing error: %d -> %d", posBefore, l.Pos())
	}
}

// TestLexerDataAndPosition exercises the Data/Pos accessors.
func TestLexerDataAndPosition(t *testing.T) {
	in := []byte(`{"a":1}`)
	l := NewLexer(in, false)
	if got := l.Data(); string(got) != string(in) {
		t.Fatalf("Data() = %q, want %q", got, in)
	}
	if l.Pos() != 0 {
		t.Fatalf("Pos() = %d, want 0", l.Pos())
	}
}

// plainKeyReference is PlainKey's contract written out byte by byte.
func plainKeyReference(data []byte, pos int) (key []byte, next int, ok bool) {
	if pos >= len(data) || data[pos] != '"' {
		return nil, 0, false
	}
	for i := pos + 1; i < len(data); i++ {
		switch c := data[i]; {
		case c == '"':
			if i+1 < len(data) && data[i+1] == ':' {
				return data[pos+1 : i], i + 2, true
			}
			return nil, 0, false
		case c < 0x20 || c == '\\' || c >= utf8.RuneSelf:
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// checkPlainKey compares PlainKey at pos with plainKeyReference and, for a
// plain key, with ReadKey, which must return the same bytes and stop at
// next without an error.
func checkPlainKey(t *testing.T, data []byte, pos int) {
	t.Helper()
	l := NewLexer(data, true)
	l.SetPos(pos)
	key, next, ok := l.PlainKey()
	if l.Pos() != pos {
		t.Fatalf("PlainKey(%q at %d) moved the position to %d", data, pos, l.Pos())
	}
	wantKey, wantNext, wantOK := plainKeyReference(data, pos)
	if ok != wantOK || next != wantNext || string(key) != string(wantKey) {
		t.Fatalf("PlainKey(%q at %d) = %q, %d, %v; want %q, %d, %v", data, pos, key, next, ok, wantKey, wantNext, wantOK)
	}
	if !ok {
		return
	}
	read := l.ReadKey()
	if err := l.Err(); err != nil || string(read) != string(key) || l.Pos() != next {
		t.Fatalf("ReadKey(%q at %d) = %q at %d, err %v; PlainKey gave %q at %d", data, pos, read, l.Pos(), err, key, next)
	}
}

var plainKeySeeds = []string{
	`"a":1`, `"":1`, `"a" :1`, `"a"`, `"a`, `a":1`, ` "a":1`, `"a\"b":1`, `"a\u0062":1`,
	"\"a\tb\":1", "\"caf\xc3\xa9\":1", "\"\xff\":1", "\"\x7f\":1",
	`"a_key_longer_than_sixteen_bytes":1`,
	"\"ab\":\"\xc3\xa9\xc3\xa9\xc3\xa9\xc3\xa9\xc3\xa9\"", // non-ASCII after the quote, in the same word
	"\"a_key_longer_than_sixteen_bytes\xc3\xa9\":1",
	`{"a":1,"bb":2}`,
}

func TestPlainKey(t *testing.T) {
	for _, s := range plainKeySeeds {
		for pos := range len(s) + 1 {
			checkPlainKey(t, []byte(s), pos)
		}
	}
}

func FuzzPlainKey(f *testing.F) {
	for _, s := range plainKeySeeds {
		f.Add([]byte(s), uint8(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, pos uint8) {
		checkPlainKey(t, data, min(int(pos), len(data)))
	})
}

type errString string

func (e errString) Error() string { return string(e) }
