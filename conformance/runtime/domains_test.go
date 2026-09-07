// Package runtimeconformance's domain-accounting tests convert the probabilistic
// coverage of the fuzz/property suite into EXHAUSTIVE or BOUNDARY-COMPLETE
// coverage for every input domain zerojson's runtime touches. Fuzzers
// sample a domain and, for a 2^32-or-larger space, essentially never hit
// the exact extremes (a math.MinInt negation panic is exactly the kind of
// bug fuzzing misses for this reason). These tests instead walk
// the small domains in full and the large domains at every boundary, each
// against an INDEPENDENT oracle (strconv, encoding/json, time, easyjson,
// google/uuid) — never against zerojson itself.
//
// See ../DOMAINS.md for the full function-by-function accounting: which
// strategy covers each runtime kernel and where its residual risk sits.
//
// The heavy full-2^32 enumerations (int32/uint32/float32 total, the
// 2048x2048 surrogate grid, the 1e8 ParseEightDigits grid) live behind
// the `exhaustive` build tag in domains_exhaustive_test.go; the tests here
// keep meaningful subsets (full 2^16 widths, strided 2^32 sweeps, all
// 65536 \uXXXX code units) so normal `go test` still walks real domains.
package runtimeconformance

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mailru/easyjson/jwriter"

	"github.com/neal/zerojson"
	"github.com/neal/zerojson/conformance/internal/oracle"
)

// --- integer decode: narrow widths, TOTAL domain -------------------------

// intWidth captures one signed integer width's decode entry point plus its
// documented [min,max] so a single sweep can drive every ReadIntN uniformly.
type intWidth struct {
	name     string
	bits     int
	min, max int64
	decode   func(s string) (int64, bool) // (value, ok == no decode error)
}

func signedWidths() []intWidth {
	return []intWidth{
		{"int8", 8, math.MinInt8, math.MaxInt8, func(s string) (int64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadInt8()
			return int64(v), l.Err() == nil
		}},
		{"int16", 16, math.MinInt16, math.MaxInt16, func(s string) (int64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadInt16()
			return int64(v), l.Err() == nil
		}},
		{"int32", 32, math.MinInt32, math.MaxInt32, func(s string) (int64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadInt32()
			return int64(v), l.Err() == nil
		}},
		{"int64", 64, math.MinInt64, math.MaxInt64, func(s string) (int64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadInt64()
			return v, l.Err() == nil
		}},
	}
}

type uintWidth struct {
	name   string
	bits   int
	max    uint64
	decode func(s string) (uint64, bool)
}

func unsignedWidths() []uintWidth {
	return []uintWidth{
		{"uint8", 8, math.MaxUint8, func(s string) (uint64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadUint8()
			return uint64(v), l.Err() == nil
		}},
		{"uint16", 16, math.MaxUint16, func(s string) (uint64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadUint16()
			return uint64(v), l.Err() == nil
		}},
		{"uint32", 32, math.MaxUint32, func(s string) (uint64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadUint32()
			return uint64(v), l.Err() == nil
		}},
		{"uint64", 64, math.MaxUint64, func(s string) (uint64, bool) {
			l := zerojson.NewLexer([]byte(s), false)
			v := l.ReadUint64()
			return v, l.Err() == nil
		}},
	}
}

// decodeQuotedSigned / decodeQuotedUnsigned run the `,string` (quoted-token)
// reader of the given width.
func decodeQuotedSigned(bits int, s string) (int64, bool) {
	l := zerojson.NewLexer([]byte(s), false)
	var v int64
	switch bits {
	case 8:
		v = int64(l.ReadQuotedInt8())
	case 16:
		v = int64(l.ReadQuotedInt16())
	case 32:
		v = int64(l.ReadQuotedInt32())
	default:
		v = l.ReadQuotedInt64()
	}
	return v, l.Err() == nil
}

func decodeQuotedUnsigned(bits int, s string) (uint64, bool) {
	l := zerojson.NewLexer([]byte(s), false)
	var v uint64
	switch bits {
	case 8:
		v = uint64(l.ReadQuotedUint8())
	case 16:
		v = uint64(l.ReadQuotedUint16())
	case 32:
		v = uint64(l.ReadQuotedUint32())
	default:
		v = l.ReadQuotedUint64()
	}
	return v, l.Err() == nil
}

// checkSignedDecode drives one width against strconv.ParseInt, the
// independent range+value oracle: a decode must succeed exactly when
// strconv accepts s at that bit width, and agree on the value when it does.
func checkSignedDecode(t *testing.T, w intWidth, s string) {
	t.Helper()
	checkSignedDecodeOracle(t, w, s, s)
}

// checkSignedDecodeOracle decodes input (which may be quoted for the
// ,string variant) but ranges/values against oracle (the bare numeric
// spelling strconv understands). A quoted input goes through the width's
// ReadQuotedInt* reader — the one `,string` fields use — and must decode
// exactly as the bare form does through ReadInt*; the bare reader itself
// must REJECT the quoted token, as encoding/json does for a plain numeric
// field.
func checkSignedDecodeOracle(t *testing.T, w intWidth, input, oracle string) {
	t.Helper()
	want, wErr := strconv.ParseInt(oracle, 10, w.bits)
	var got int64
	var ok bool
	if len(input) > 0 && input[0] == '"' {
		if _, bareOK := w.decode(input); bareOK {
			t.Errorf("%s bare reader accepted quoted %q", w.name, input)
		}
		got, ok = decodeQuotedSigned(w.bits, input)
	} else {
		got, ok = w.decode(input)
	}
	if (wErr == nil) != ok {
		t.Errorf("%s decode %q: strconv ok=%v zerojson ok=%v", w.name, input, wErr == nil, ok)
		return
	}
	if wErr == nil && got != want {
		t.Errorf("%s decode %q: want %d got %d", w.name, input, want, got)
	}
}

func checkUnsignedDecode(t *testing.T, w uintWidth, s string) {
	t.Helper()
	checkUnsignedDecodeOracle(t, w, s, s)
}

func checkUnsignedDecodeOracle(t *testing.T, w uintWidth, input, oracle string) {
	t.Helper()
	want, wErr := strconv.ParseUint(oracle, 10, w.bits)
	var got uint64
	var ok bool
	if len(input) > 0 && input[0] == '"' {
		if _, bareOK := w.decode(input); bareOK {
			t.Errorf("%s bare reader accepted quoted %q", w.name, input)
		}
		got, ok = decodeQuotedUnsigned(w.bits, input)
	} else {
		got, ok = w.decode(input)
	}
	if (wErr == nil) != ok {
		t.Errorf("%s decode %q: strconv ok=%v zerojson ok=%v", w.name, input, wErr == nil, ok)
		return
	}
	if wErr == nil && got != want {
		t.Errorf("%s decode %q: want %d got %d", w.name, input, want, got)
	}
}

// TestNarrowIntTotalDomain enumerates the ENTIRE value domain of int8,
// uint8, int16, and uint16 — every in-range value's decimal string decoded
// and checked against strconv, plus every value in a margin just outside
// each width's range asserting the exact strconv range verdict. This is the
// domain fuzzing samples but never covers in full; here it is walked to the
// last value on both sides of both boundaries.
func TestNarrowIntTotalDomain(t *testing.T) {
	const margin = 4
	for _, w := range signedWidths() {
		if w.bits > 16 {
			continue // int32/int64 handled by the boundary sweeps below
		}
		for v := w.min - margin; v <= w.max+margin; v++ {
			checkSignedDecode(t, w, strconv.FormatInt(v, 10))
		}
		// Encode side: the narrow types widen to AppendInt64; confirm the
		// widened encode matches strconv across the whole in-range domain.
		for v := w.min; v <= w.max; v++ {
			got := zerojson.AppendInt64(nil, v)
			if want := strconv.AppendInt(nil, v, 10); !bytes.Equal(got, want) {
				t.Fatalf("AppendInt64(%d): want %s got %s", v, want, got)
			}
		}
	}
	for _, w := range unsignedWidths() {
		if w.bits > 16 {
			continue
		}
		for v := int64(-margin); v <= int64(w.max)+margin; v++ {
			// Includes negative inputs (must be rejected by an unsigned width).
			checkUnsignedDecode(t, w, strconv.FormatInt(v, 10))
		}
		for u := uint64(0); u <= w.max; u++ {
			got := zerojson.AppendUint64(nil, u)
			if want := strconv.AppendUint(nil, u, 10); !bytes.Equal(got, want) {
				t.Fatalf("AppendUint64(%d): want %s got %s", u, want, got)
			}
		}
	}
}

// TestInt32Uint32BoundarySweep covers int32/uint32 at every boundary region
// (0, +/-1, min/max +/-2, and every decimal digit-length transition +/-2)
// in full, plus a strided sweep every 65537th value across the whole 2^32
// domain (a coprime-ish stride so the samples do not alias digit lengths).
// The full 4B enumeration lives behind the exhaustive tag.
func TestInt32Uint32BoundarySweep(t *testing.T) {
	i32 := signedWidths()[2]
	u32 := unsignedWidths()[2]

	var i32points []int64
	for _, base := range []int64{0, math.MinInt32, math.MaxInt32} {
		for d := int64(-2); d <= 2; d++ {
			i32points = append(i32points, base+d)
		}
	}
	for p := int64(1); p <= 10_000_000_000; p *= 10 {
		for _, s := range []int64{p, -p} {
			for d := int64(-2); d <= 2; d++ {
				i32points = append(i32points, s+d)
			}
		}
	}
	for _, v := range i32points {
		checkSignedDecode(t, i32, strconv.FormatInt(v, 10))
	}

	var u32points []uint64
	for _, base := range []uint64{0, math.MaxUint32} {
		for d := -2; d <= 2; d++ {
			u32points = append(u32points, base+uint64(int64(d)))
		}
	}
	for p := uint64(1); p <= 10_000_000_000; p *= 10 {
		for d := -2; d <= 2; d++ {
			u32points = append(u32points, p+uint64(int64(d)))
		}
	}
	for _, v := range u32points {
		checkUnsignedDecode(t, u32, strconv.FormatUint(v, 10))
	}

	// Strided sweep across the whole 2^32 domain.
	const stride = 65537
	for v := int64(math.MinInt32); v <= math.MaxInt32; v += stride {
		checkSignedDecode(t, i32, strconv.FormatInt(v, 10))
	}
	for v := uint64(0); v <= math.MaxUint32; v += stride {
		checkUnsignedDecode(t, u32, strconv.FormatUint(v, 10))
	}
}

// TestInt64Uint64BoundaryComplete walks int64/uint64 at every power-of-ten
// digit-length transition +/-2, the 9-repunit near each, min/max +/-2 as
// out-of-range strings, leading-zero forms, and the quoted (`,string`)
// spelling — all against strconv's range+value contract.
func TestInt64Uint64BoundaryComplete(t *testing.T) {
	i64 := signedWidths()[3]
	u64 := unsignedWidths()[3]

	var strs []string
	addSigned := func(v int64) {
		strs = append(strs, strconv.FormatInt(v, 10))
	}
	// Powers of ten and 9-repunits +/- 2 across the whole magnitude ladder.
	for p := int64(1); p <= 1_000_000_000_000_000_000; p *= 10 {
		for d := int64(-2); d <= 2; d++ {
			addSigned(p + d)
			addSigned(-p + d)
		}
		if r := p*10 - 1; r > 0 { // 9, 99, 999, ...
			for d := int64(-2); d <= 2; d++ {
				addSigned(r + d)
			}
		}
	}
	// Exact int64 min/max +/-2 as decimal strings (strconv is the oracle for
	// which of these overflow).
	for _, s := range []string{
		"9223372036854775805", "9223372036854775806", "9223372036854775807",
		"9223372036854775808", "9223372036854775809",
		"-9223372036854775806", "-9223372036854775807", "-9223372036854775808",
		"-9223372036854775809", "-9223372036854775810",
		"0", "-0", "00", "007", "000000000000000000001",
	} {
		strs = append(strs, s)
	}
	for _, s := range strs {
		checkSignedDecode(t, i64, s)
		checkSignedDecodeOracle(t, i64, `"`+s+`"`, s) // ,string variant
	}

	var ustrs []string
	for p := uint64(1); p <= 10_000_000_000_000_000_000; p *= 10 {
		for d := -2; d <= 2; d++ {
			ustrs = append(ustrs, strconv.FormatUint(p+uint64(int64(d)), 10))
		}
	}
	for _, s := range []string{
		"18446744073709551613", "18446744073709551614", "18446744073709551615",
		"18446744073709551616", "18446744073709551617",
		"0", "007", "0000000000000000000001", "-1",
	} {
		ustrs = append(ustrs, s)
	}
	for _, s := range ustrs {
		checkUnsignedDecode(t, u64, s)
		checkUnsignedDecodeOracle(t, u64, `"`+s+`"`, s)
	}
}

// --- timezone offsets: AppendTime, TOTAL --------------------------------

// safeMarshalJSON runs time.Time.MarshalJSON as the oracle, recovering any
// panic so an oracle crash on an extreme offset is reported as a finding
// rather than taking the test process down.
func safeMarshalJSON(t time.Time) (b []byte, err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	b, err = t.MarshalJSON()
	return
}

// fixedInstant is one instant with a sub-second component and a year safely
// inside [0,9999]; every timezone test re-zones this same wall clock so the
// only variable is the offset.
var fixedInstant = struct{ y, mo, d, h, mi, s, ns int }{2021, 6, 15, 12, 30, 45, 123456789}

func timeAt(offset int) time.Time {
	fi := fixedInstant
	return time.Date(fi.y, time.Month(fi.mo), fi.d, fi.h, fi.mi, fi.s, fi.ns,
		time.FixedZone("x", offset))
}

func checkTimeOffset(t *testing.T, offset int) {
	t.Helper()
	tm := timeAt(offset)
	want, wErr, panicked := safeMarshalJSON(tm)
	if panicked {
		t.Errorf("offset %d: time.MarshalJSON panicked: %v", offset, wErr)
		return
	}
	got, gErr := zerojson.AppendTime(nil, tm)
	if (wErr == nil) != (gErr == nil) {
		t.Errorf("offset %d: MarshalJSON err=%v zerojson err=%v", offset, wErr, gErr)
		return
	}
	if wErr == nil && !bytes.Equal(want, got) {
		t.Errorf("offset %d: want %s got %s", offset, want, got)
	}
}

// TestAppendTimeOffsetTotal encodes the fixed instant at EVERY whole-second
// offset in [-24h,+24h] (172,801 values) asserting byte-identity and error
// parity with time.Time.MarshalJSON, then the out-of-range classes:
// +/-24h..+/-25h exhaustive, the int32/int64 extremes, and an exponential
// stride across the full int range. Sub-minute offsets (seconds not
// divisible by 60) are enumerated in full over +/-10min. This is the domain
// where the math.MinInt negation panic hid: the extremes are named here, not
// sampled.
func TestAppendTimeOffsetTotal(t *testing.T) {
	// Every whole-second offset across the representable RFC3339 range.
	for off := -24 * 3600; off <= 24*3600; off++ {
		checkTimeOffset(t, off)
	}
	// Just outside the range, both signs, whole hour+ region.
	for off := 24 * 3600; off <= 25*3600; off++ {
		checkTimeOffset(t, off)
		checkTimeOffset(t, -off)
	}
	// Named integer extremes — the class fuzzing cannot hit.
	extremes := []int{
		math.MaxInt32, math.MinInt32, math.MaxInt32 - 1, math.MinInt32 + 1,
		1 << 20, -(1 << 20), 100 * 3600, -100 * 3600,
	}
	if strconv.IntSize == 64 {
		max64, min64 := int64(math.MaxInt64), int64(math.MinInt64)
		extremes = append(extremes, int(max64), int(min64), int(max64-1), int(min64+1))
	}
	for _, off := range extremes {
		checkTimeOffset(t, off)
	}
	// Exponential stride across the full positive and negative int range.
	for m := 1; m > 0 && m <= math.MaxInt/2; m *= 2 {
		checkTimeOffset(t, m)
		checkTimeOffset(t, -m)
	}
}

// TestAppendTimeSubMinuteOffsets enumerates every sub-minute offset in
// +/-10min: RFC3339 has no zone-seconds field, so stdlib truncates the
// offset to whole minutes, and a small negative offset renders "+00:00",
// not "-00:00" (a real divergence the value-space fuzzer found once). Every
// second in the window is checked for stdlib byte-parity.
func TestAppendTimeSubMinuteOffsets(t *testing.T) {
	for off := -600; off <= 600; off++ {
		checkTimeOffset(t, off)
	}
}

// --- dates: parseRFC3339Z via ReadTime, TOTAL ---------------------------

// yearClasses spans the four leap-year regimes plus the RFC3339 year
// boundaries so the (month,day) grid below is checked under every calendar
// rule parseRFC3339Z encodes.
var yearClasses = []int{
	2001, // common non-leap
	2004, // ordinary leap (div by 4, not 100)
	1900, // non-leap century (div by 100, not 400)
	2000, // leap century (div by 400)
	0,    // year 0000 (proleptic Gregorian leap; below-epoch Hinnant path)
	1,    // year 0001
	9998, // non-leap near the RFC3339 upper bound
	9999, // year 9999 upper bound
}

func readTimeZerojson(s string) (time.Time, bool) {
	l := zerojson.NewLexer([]byte(`"`+s+`"`), false)
	tm := l.ReadTime()
	return tm, l.Err() == nil
}

// TestReadTimeDateGridTotal walks every (month 0..13, day 0..32) pair under
// every year class and checks accept/reject AND value against time.Parse —
// the independent oracle. parseRFC3339Z's fast path and its time.Parse
// fallback must agree with the stdlib parser on every one of these cells.
func TestReadTimeDateGridTotal(t *testing.T) {
	for _, y := range yearClasses {
		for mo := 0; mo <= 13; mo++ {
			for d := 0; d <= 32; d++ {
				s := fmt.Sprintf("%04d-%02d-%02dT00:00:00Z", y, mo, d)
				want, wErr := time.Parse(time.RFC3339, s)
				got, ok := readTimeZerojson(s)
				if (wErr == nil) != ok {
					t.Errorf("%s: time.Parse ok=%v zerojson ok=%v", s, wErr == nil, ok)
					continue
				}
				if wErr == nil && !got.Equal(want) {
					t.Errorf("%s: want %v got %v", s, want, got)
				}
			}
		}
	}
}

// TestReadTimeDigitPositionMutation is the enumerated form of the bad|=hi|lo
// digit-detection bug class: at every digit position of a canonical
// timestamp, substitute all 256 byte values and require zerojson to agree
// with time.Parse on the accept/reject verdict (and the value when both
// accept). A non-digit in a digit slot must drop the fast path and reach the
// identical time.Parse rejection; a digit produces some other date that must
// still match the oracle.
func TestReadTimeDigitPositionMutation(t *testing.T) {
	const base = "2021-06-15T12:30:45Z"
	// Digit positions in "YYYY-MM-DDTHH:MM:SS": everything that is not a
	// structural separator.
	sep := map[int]bool{4: true, 7: true, 10: true, 13: true, 16: true, 19: true}
	for pos := 0; pos < len(base); pos++ {
		if sep[pos] {
			continue
		}
		for x := 0; x < 256; x++ {
			b := []byte(base)
			b[pos] = byte(x)
			s := string(b)
			want, wErr := time.Parse(time.RFC3339, s)
			got, ok := readTimeZerojson(s)
			if (wErr == nil) != ok {
				t.Errorf("pos %d byte %#02x (%q): time.Parse ok=%v zerojson ok=%v",
					pos, x, s, wErr == nil, ok)
				continue
			}
			if wErr == nil && !got.Equal(want) {
				t.Errorf("pos %d byte %#02x (%q): want %v got %v", pos, x, s, want, got)
			}
		}
	}
}

// TestReadTimeFractionAndSeparators enumerates the fractional-second digit
// counts (1..10, where 10 is out of range) and each fixed separator position
// mutated, all against time.Parse.
func TestReadTimeFractionAndSeparators(t *testing.T) {
	for n := 1; n <= 10; n++ {
		frac := ""
		for i := 0; i < n; i++ {
			frac += "1"
		}
		s := "2021-06-15T12:30:45." + frac + "Z"
		want, wErr := time.Parse(time.RFC3339, s)
		got, ok := readTimeZerojson(s)
		if (wErr == nil) != ok {
			t.Errorf("%s: time.Parse ok=%v zerojson ok=%v", s, wErr == nil, ok)
			continue
		}
		if wErr == nil && !got.Equal(want) {
			t.Errorf("%s: want %v got %v", s, want, got)
		}
	}
	const base = "2021-06-15T12:30:45Z"
	for _, pos := range []int{4, 7, 10, 13, 16} {
		for x := 0; x < 256; x++ {
			b := []byte(base)
			b[pos] = byte(x)
			s := string(b)
			want, wErr := time.Parse(time.RFC3339, s)
			got, ok := readTimeZerojson(s)
			if (wErr == nil) != ok {
				t.Errorf("sep pos %d byte %#02x (%q): time.Parse ok=%v zerojson ok=%v",
					pos, x, s, wErr == nil, ok)
			} else if wErr == nil && !got.Equal(want) {
				t.Errorf("sep pos %d byte %#02x (%q): want %v got %v", pos, x, s, want, got)
			}
		}
	}
}

// --- escapes: encode & decode/validate ----------------------------------

// TestAppendStringByteTotal encodes every 1-byte and every 2-byte string
// (all 256 and all 65536 sequences) and requires byte-identity with
// easyjson — AppendString's documented byte-identity target (writer.go).
// The 2-byte space includes every invalid-UTF8 leading/continuation
// combination, exhaustively exercising the replacementEscape slow path and
// the ASCII escape table.
//
// easyjson, not encoding/json, is the authority here: the two oracles
// legitimately differ on the short control escapes — easyjson (and zerojson)
// emit / for backspace/form-feed where stdlib emits \b/\f — so a
// two-oracle agreement check would be testing that divergence, not zerojson.
// The stdlib cross-check is applied only to bytes where the encoders agree
// (everything except 0x08 and 0x0c).
func TestAppendStringByteTotal(t *testing.T) {
	check := func(s string) {
		w := jwriter.Writer{}
		w.String(s)
		wantEJ, ejErr := w.Buffer.BuildBytes(), w.Error
		if ejErr != nil {
			t.Fatalf("%q: easyjson errored: %v", s, ejErr)
		}
		got := zerojson.AppendString(nil, s)
		if !bytes.Equal(wantEJ, got) {
			t.Fatalf("%q: want %s got %s", s, wantEJ, got)
		}
		// Cross-check against stdlib except for the documented short-escape
		// divergence, so the second independent oracle still guards the whole
		// domain where the two encoders are defined to agree.
		// The v2-backed encoding/json (Go 1.27+) writes an invalid UTF-8
		// byte as the raw U+FFFD rune where classic v1, easyjson, and
		// zerojson write the \ufffd escape; normalize so the cross-check
		// still covers every other byte of the output.
		if !strings.ContainsAny(s, "\b\f") {
			wantStd, _ := json.Marshal(s)
			if wantStd = oracle.ClassicUTF8(wantStd); !bytes.Equal(wantStd, got) {
				t.Fatalf("%q: stdlib cross-check want %s got %s", s, wantStd, got)
			}
		}
	}
	for b := 0; b < 256; b++ {
		check(string([]byte{byte(b)}))
	}
	for hi := 0; hi < 256; hi++ {
		for lo := 0; lo < 256; lo++ {
			check(string([]byte{byte(hi), byte(lo)}))
		}
	}
}

// TestUnescapeAllCodeUnits decodes "\uXXXX" for ALL 65536 code units and
// checks the decoded string value byte-for-byte against encoding/json — the
// well-formed-escape decode contract. Lone surrogates must render U+FFFD in
// both (the value-level replacement neither Valid nor the grammar performs).
func TestUnescapeAllCodeUnits(t *testing.T) {
	for r := 0; r < 65536; r++ {
		s := fmt.Sprintf(`"\u%04x"`, r)
		var want string
		if err := json.Unmarshal([]byte(s), &want); err != nil {
			t.Fatalf("%s: stdlib unmarshal errored: %v", s, err)
		}
		l := zerojson.NewLexer([]byte(s), false)
		got := l.ReadString()
		if err := l.Err(); err != nil {
			t.Fatalf("%s: zerojson errored: %v", s, err)
		}
		if got != want {
			t.Errorf("%s: want %q got %q", s, want, got)
		}
	}
}

// lowSurrogateClasses is a representative-complete set of second-\u values
// for the surrogate-pair decode enumeration: the low-surrogate range
// boundaries and interior, a non-surrogate BMP escape, and a second high
// surrogate. The COMPLETENESS argument (see DOMAINS.md): unescapeString
// branches on the second code unit only via `r2 >= 0xDC00 && r2 <= 0xDFFF`,
// so every second value falls into one of exactly two classes — inside
// [DC00,DFFF] (pair combines) or outside (first surrogate replaced alone).
// These representatives cover both classes and both edges of the deciding
// interval; the full 2048x2048 grid runs behind the exhaustive tag.
var lowSurrogateClasses = []int{0xDC00, 0xDBFF, 0xDFFF, 0xE000, 0x0041, 0xD800, 0xDD00}

// TestUnescapeSurrogatePairs enumerates every first surrogate code unit in
// [D800,DFFF] against the representative low classes, comparing the decoded
// value with encoding/json.
func TestUnescapeSurrogatePairs(t *testing.T) {
	for hi := 0xD800; hi <= 0xDFFF; hi++ {
		for _, lo := range lowSurrogateClasses {
			s := fmt.Sprintf(`"\u%04x\u%04x"`, hi, lo)
			var want string
			if err := json.Unmarshal([]byte(s), &want); err != nil {
				t.Fatalf("%s: stdlib unmarshal errored: %v", s, err)
			}
			l := zerojson.NewLexer([]byte(s), false)
			got := l.ReadString()
			if err := l.Err(); err != nil {
				t.Fatalf("%s: zerojson errored: %v", s, err)
			}
			if got != want {
				t.Errorf("%s: want %q got %q", s, want, got)
			}
		}
	}
}

// TestValidEscapeAcceptReject enumerates the STRICT accept/reject side of
// escapes via zerojson.Valid (the grammar surface, matching json.Valid): all
// 256 single-character escapes "\X", and every one of the four \uXXXX hex
// positions mutated to all 256 byte values. Valid's verdict must equal
// encoding/json.Valid's on every case.
func TestValidEscapeAcceptReject(t *testing.T) {
	for x := 0; x < 256; x++ {
		s := []byte{'"', '\\', byte(x), '"'}
		got := zerojson.Valid(s) == nil
		want := json.Valid(s)
		if got != want {
			t.Errorf(`Valid("\%s"): zerojson=%v stdlib=%v`, string([]byte{byte(x)}), got, want)
		}
	}
	// The 8-byte escape form "A" (quote, backslash, u, 0,0,4,1, quote);
	// the four hex digits sit at indices 3..6. Built from bytes so no source
	// literal can silently interpret the backslash-u.
	base := []byte{'"', '\\', 'u', '0', '0', '4', '1', '"'}
	for pos := 3; pos <= 6; pos++ { // the four hex digit positions
		for x := 0; x < 256; x++ {
			b := append([]byte(nil), base...)
			b[pos] = byte(x)
			got := zerojson.Valid(b) == nil
			want := json.Valid(b)
			if got != want {
				t.Errorf("Valid(%q): zerojson=%v stdlib=%v", b, got, want)
			}
		}
	}
}

// --- UUID: POSITION-COMPLETE --------------------------------------------

// TestReadUUIDPositionComplete substitutes all 256 byte values at each
// position of the canonical 36-byte form and the braced 38-byte form,
// checking accept/reject AND value against google/uuid.ParseBytes — the
// pinned oracle both the fast path and the fallback must match.
func TestReadUUIDPositionComplete(t *testing.T) {
	forms := []string{
		"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1",   // canonical 36
		"{b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1}", // braced 38
	}
	for _, form := range forms {
		for pos := 0; pos < len(form); pos++ {
			for x := 0; x < 256; x++ {
				// 0x22 (") and 0x5c (\) cannot appear as a LITERAL content
				// byte of a JSON string — they reframe or start an escape — so
				// they are outside the domain that can reach ReadUUIDBytes as
				// string content (an escaped UUID is a separate, deliberately
				// rejected path). The oracle parses raw bytes, so including
				// them would compare two different inputs. The other 254 byte
				// values at every position are enumerated in full.
				if x == '"' || x == '\\' {
					continue
				}
				b := []byte(form)
				b[pos] = byte(x)
				wantU, wErr := uuid.ParseBytes(b)
				l := zerojson.NewLexer(append(append([]byte{'"'}, b...), '"'), false)
				gotU := l.ReadUUIDBytes()
				gErr := l.Err()
				if (wErr == nil) != (gErr == nil) {
					t.Errorf("form %q pos %d byte %#02x (%q): uuid err=%v zerojson err=%v",
						form, pos, x, b, wErr, gErr)
					continue
				}
				if wErr == nil && [16]byte(wantU) != gotU {
					t.Errorf("form %q pos %d byte %#02x (%q): want %x got %x",
						form, pos, x, b, wantU, gotU)
				}
			}
		}
	}
}

// --- float32 strided / float64 boundary ---------------------------------

// TestAppendFloat32StridedSweep sweeps 16M float32 bit patterns at a fixed
// stride across the whole 2^32 domain, checking AppendFloat32 against
// strconv (the documented byte-identity target). The full 2^32 enumeration
// runs behind the exhaustive tag; this strided tier plus the existing
// property fuzz (root float_test.go) keeps continuous coverage.
func TestAppendFloat32StridedSweep(t *testing.T) {
	const stride = 269 // odd, coprime with 2^32, ~16M samples
	for bitsU := uint64(0); bitsU <= math.MaxUint32; bitsU += stride {
		f := math.Float32frombits(uint32(bitsU))
		got, err := zerojson.AppendFloat32(nil, f)
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			if err == nil {
				t.Fatalf("bits %#08x (%v): expected NaN/Inf error", bitsU, f)
			}
			continue
		}
		want := strconv.AppendFloat(nil, float64(f), 'g', -1, 32)
		if err != nil {
			t.Fatalf("bits %#08x: unexpected error: %v", bitsU, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#08x: want %s got %s", bitsU, want, got)
		}
	}
}

// float64BoundaryDomain curates the boundary classes a random sweep cannot
// reliably hit: exponent-field edges, denormal boundaries, the shortest-repr
// classics, and the e+/-NN notation-transition values around 1e20/1e21.
func float64BoundaryDomain() []float64 {
	vals := []float64{
		0, math.Copysign(0, -1),
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		2.2250738585072014e-308, // smallest normal
		2.225073858507201e-308,  // largest denormal
		math.MaxFloat64, -math.MaxFloat64,
		1, -1, math.Nextafter(1, 2), math.Nextafter(1, 0),
		9007199254740992, 9007199254740993, // 2^53, 2^53+1
		1e20, 1e21, math.Nextafter(1e21, 0), math.Nextafter(1e21, 2e21),
		9.999999999999998e19, 1.0000000000000002e21,
		4.962676536460209e+09,
	}
	// Every power-of-ten notation transition e-05..e+21 and its neighbours.
	for e := -25; e <= 25; e++ {
		v := math.Pow(10, float64(e))
		vals = append(vals, v, math.Nextafter(v, 0), math.Nextafter(v, 2*v))
	}
	// Exponent-field boundaries: 2^k for every representable k.
	for k := -1074; k <= 1023; k += 1 {
		vals = append(vals, math.Ldexp(1, k))
	}
	return vals
}

// TestAppendFloat64BoundaryComplete checks AppendFloat64 byte-identity with
// strconv across every boundary class (exponent field, denormal, notation
// transition, shortest-repr classic).
func TestAppendFloat64BoundaryComplete(t *testing.T) {
	for _, f := range float64BoundaryDomain() {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		want := strconv.AppendFloat(nil, f, 'g', -1, 64)
		got, err := zerojson.AppendFloat64(nil, f)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", f, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%v: want %s got %s", f, want, got)
		}
	}
}

// --- DecimalParts: GRID-TOTAL -------------------------------------------

// TestDecimalPartsGridTotal drives AppendDecimalParts/ParseDecimalParts over
// a coefficient set spanning every int64 digit-length transition plus the
// extremes, crossed with EVERY exponent in the legal range and +/-5 beyond.
// In range: the output must be valid JSON (json.Valid oracle) and reparse to
// a value that re-appends identically. Beyond range: AppendDecimalParts must
// panic (the documented infallible-append contract) — enumerated, not
// sampled.
func TestDecimalPartsGridTotal(t *testing.T) {
	var coeffs []int64
	for p := int64(1); p <= 1_000_000_000_000_000_000; p *= 10 {
		coeffs = append(coeffs, p-1, p, p+1, -(p - 1), -p, -(p + 1))
	}
	coeffs = append(coeffs, 0, 1, -1, math.MinInt64, math.MinInt64+1, math.MaxInt64)

	const bound = 32 // maxDecimalExponent
	for _, coeff := range coeffs {
		for exp := int32(-bound - 5); exp <= bound+5; exp++ {
			inRange := exp >= -bound && exp <= bound
			var out string
			func() {
				defer func() {
					r := recover()
					if inRange && r != nil {
						t.Errorf("AppendDecimalParts(%d,%d): unexpected panic %v", coeff, exp, r)
					}
					if !inRange && r == nil {
						t.Errorf("AppendDecimalParts(%d,%d): expected panic (exp out of range)", coeff, exp)
					}
				}()
				out = string(zerojson.AppendDecimalParts(nil, coeff, exp))
			}()
			if !inRange {
				continue
			}
			if !json.Valid([]byte(out)) {
				t.Errorf("AppendDecimalParts(%d,%d)=%q not valid JSON", coeff, exp, out)
				continue
			}
			rc, re, err := zerojson.ParseDecimalParts([]byte(out))
			if err != nil {
				// A positive exponent multiplies the coefficient by 10^exp,
				// which can exceed int64 (e.g. MaxInt64 * 10) — ParseDecimalParts
				// rejects that as ErrDecimalRange by contract, so the round-trip
				// is only asserted where the value stays representable.
				if errors.Is(err, zerojson.ErrDecimalRange) {
					continue
				}
				t.Errorf("AppendDecimalParts(%d,%d)=%q: reparse err %v", coeff, exp, out, err)
				continue
			}
			if back := string(zerojson.AppendDecimalParts(nil, rc, re)); back != out {
				t.Errorf("AppendDecimalParts(%d,%d)=%q roundtrips to %q", coeff, exp, out, back)
			}
		}
	}
}

// --- SWAR digit kernels: IsEightDigits / ParseEightDigits ---------------

// naiveEightDigits is the byte-scan reference for the SWAR digit detector.
func naiveEightDigits(w uint64) bool {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], w)
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// TestEightDigitKernelsBoundary walks each of the 8 byte lanes through all
// 256 values (rest held at '0') for IsEightDigits — every single-lane
// boundary — plus every all-same-digit word, checking against the naive
// scan and, where all-digit, against the decimal value ParseEightDigits
// must produce. The full 1e8 valid-word grid runs behind the exhaustive tag.
func TestEightDigitKernelsBoundary(t *testing.T) {
	base := []byte("00000000")
	for lane := 0; lane < 8; lane++ {
		for x := 0; x < 256; x++ {
			b := append([]byte(nil), base...)
			b[lane] = byte(x)
			w := binary.LittleEndian.Uint64(b)
			if got, want := zerojson.IsEightDigits(w), naiveEightDigits(w); got != want {
				t.Errorf("IsEightDigits(lane %d = %#02x): got %v want %v", lane, x, got, want)
			}
		}
	}
	// Every all-same-digit word and a decimal-value spot check.
	for d := 0; d <= 9; d++ {
		s := fmt.Sprintf("%d%d%d%d%d%d%d%d", d, d, d, d, d, d, d, d)
		w := binary.LittleEndian.Uint64([]byte(s))
		if !zerojson.IsEightDigits(w) {
			t.Fatalf("IsEightDigits(%q) false", s)
		}
		// The 8 bytes are little-endian; ParseEightDigits reads them as the
		// decimal number spelled left-to-right in memory order.
		want, _ := strconv.ParseUint(s, 10, 64)
		if got := zerojson.ParseEightDigits(w); got != want {
			t.Errorf("ParseEightDigits(%q): got %d want %d", s, got, want)
		}
	}
	// A strided walk of the 1e8 valid-word space (full grid is exhaustive).
	for n := uint64(0); n < 100_000_000; n += 4999 {
		s := fmt.Sprintf("%08d", n)
		w := binary.LittleEndian.Uint64([]byte(s))
		if got := zerojson.ParseEightDigits(w); got != n {
			t.Errorf("ParseEightDigits(%q): got %d want %d", s, got, n)
		}
	}
}
