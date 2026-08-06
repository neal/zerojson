package zerojson

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"testing"
	"time"
)

// api_fuzz_test.go fuzzes the EXPORTED runtime API directly, rather than
// only through generated codecs. The generated-codec fuzzers in
// conformance can only reach an Append*/Read* function with values some
// constructor knows how to build; a function whose bug lives in an input
// no type in the test corpus produces (an out-of-[0,9999] year, a
// FixedZone offset of 25 hours, a float from a raw NaN bit pattern, a
// decimal coefficient at math.MinInt64) stayed invisible. These fuzzers
// feed each exported function its full native input domain and assert the
// three universal contracts:
//
//	(a) it never panics;
//	(b) when it returns a nil error, json.Valid holds on the bytes it
//	    appended (to a nil dst, so exactly the value under test is
//	    checked), i.e. it never silently emits invalid JSON; and
//	(c) where a stdlib analogue exists (strconv, time.Time.MarshalJSON,
//	    json round-trip), the analogue's success/failure and — where the
//	    contract is byte-identity — its bytes agree.
//
// This file lives in the root module and must stay dependency-free: no
// uuid import (AppendUUIDBytes takes a [16]byte), only the standard
// library.

// checkAppendedValue asserts that appending to a non-empty prefix appends
// (never resets) and that the appended-to-nil bytes are a valid JSON
// value — the (a)+(b) contract shared by every infallible Append*.
func checkAppendedValue(t *testing.T, what string, appendFn func(dst []byte) []byte) []byte {
	t.Helper()
	got := appendFn(nil)
	if !json.Valid(got) {
		t.Fatalf("%s: appended invalid JSON: %q", what, got)
	}
	const prefix = "PFX"
	got2 := appendFn([]byte(prefix))
	if !bytes.HasPrefix(got2, []byte(prefix)) || !bytes.Equal(got2[len(prefix):], got) {
		t.Fatalf("%s: did not append to prefix cleanly: %q", what, got2)
	}
	return got
}

// FuzzAPIAppendString feeds arbitrary bytes (as a string, so invalid
// UTF-8 and every control byte are in-domain) and requires valid JSON out
// plus semantic agreement with encoding/json. Byte-identity is NOT
// asserted: zerojson matches easyjson's HTML/JSONP escaping, a deliberate
// documented divergence from encoding/json. The escaping-independent
// contract is that both encoders DECODE to the same value, checked
// against stdlib's own round-trip.
func FuzzAPIAppendString(f *testing.F) {
	for _, s := range []string{
		"", "a", `"`, `\`, "\x00", "\x1f", "<>&", "  ",
		"héllo", "\xff\xfe", "\xed\xa0\x80", "emoji 😀", "tab\tnl\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		s := string(b)
		got := checkAppendedValue(t, "AppendString", func(dst []byte) []byte {
			return AppendString(dst, s)
		})
		// AppendStringBytes must agree byte-for-byte with AppendString.
		if gb := AppendStringBytes(nil, b); !bytes.Equal(gb, got) {
			t.Fatalf("AppendStringBytes != AppendString for %q: %q vs %q", s, gb, got)
		}
		var gotBack string
		if err := json.Unmarshal(got, &gotBack); err != nil {
			t.Fatalf("stdlib rejected AppendString output %q for input %q: %v", got, s, err)
		}
		// Compare against stdlib's OWN round-trip: for valid UTF-8 this is s;
		// for invalid UTF-8 both encoders replace with U+FFFD identically, so
		// asserting gotBack==s would wrongly demand zerojson preserve bytes
		// encoding/json also refuses to.
		std, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal(%q) errored: %v", s, err)
		}
		var wantBack string
		if err := json.Unmarshal(std, &wantBack); err != nil {
			t.Fatalf("json.Unmarshal of stdlib output %q errored: %v", std, err)
		}
		if gotBack != wantBack {
			t.Fatalf("AppendString decodes differently from encoding/json: input %q -> zerojson %q (%q) vs stdlib %q (%q)", s, got, gotBack, std, wantBack)
		}
	})
}

// FuzzAPIAppendIntegers covers AppendInt64, AppendUint64, and their
// quoted variants over the full 64-bit domain. The bare forms round-trip
// through json.Unmarshal into the same integer type; the quoted forms
// must be valid JSON strings whose contents strconv-parse back to the
// input (the ",string" convention).
func FuzzAPIAppendIntegers(f *testing.F) {
	f.Add(int64(0), uint64(0))
	f.Add(int64(math.MinInt64), uint64(math.MaxUint64))
	f.Add(int64(math.MaxInt64), uint64(0))
	f.Add(int64(-1), uint64(1))
	f.Fuzz(func(t *testing.T, iv int64, uv uint64) {
		bi := checkAppendedValue(t, "AppendInt64", func(dst []byte) []byte { return AppendInt64(dst, iv) })
		if got, err := strconv.ParseInt(string(bi), 10, 64); err != nil || got != iv {
			t.Fatalf("AppendInt64(%d) = %q, parse got %d err %v", iv, bi, got, err)
		}
		bu := checkAppendedValue(t, "AppendUint64", func(dst []byte) []byte { return AppendUint64(dst, uv) })
		if got, err := strconv.ParseUint(string(bu), 10, 64); err != nil || got != uv {
			t.Fatalf("AppendUint64(%d) = %q, parse got %d err %v", uv, bu, got, err)
		}

		qi := checkAppendedValue(t, "AppendQuotedInt64", func(dst []byte) []byte { return AppendQuotedInt64(dst, iv) })
		var qis string
		if err := json.Unmarshal(qi, &qis); err != nil {
			t.Fatalf("AppendQuotedInt64(%d) = %q not a JSON string: %v", iv, qi, err)
		}
		if got, err := strconv.ParseInt(qis, 10, 64); err != nil || got != iv {
			t.Fatalf("AppendQuotedInt64(%d) inner = %q, parse got %d err %v", iv, qis, got, err)
		}

		qu := checkAppendedValue(t, "AppendQuotedUint64", func(dst []byte) []byte { return AppendQuotedUint64(dst, uv) })
		var qus string
		if err := json.Unmarshal(qu, &qus); err != nil {
			t.Fatalf("AppendQuotedUint64(%d) = %q not a JSON string: %v", uv, qu, err)
		}
		if got, err := strconv.ParseUint(qus, 10, 64); err != nil || got != uv {
			t.Fatalf("AppendQuotedUint64(%d) inner = %q, parse got %d err %v", uv, qus, got, err)
		}
	})
}

// FuzzAPIAppendTime constructs a time.Time from an arbitrary Unix second,
// nanosecond, and FixedZone offset, and holds AppendTime to strict
// byte-identity with time.Time.MarshalJSON — including error parity: both
// must reject (or both accept) the out-of-[0,9999] years and the
// >= 24h zone offsets that RFC 3339 cannot represent. AppendTime returns
// an error rather than panicking on those, but a panic on ANY input is
// still a bug (checked by the recover below).
func FuzzAPIAppendTime(f *testing.F) {
	type seed struct {
		sec, nsec, off int64
	}
	for _, s := range []seed{
		{0, 0, 0}, {0, 0, 3600}, {0, 0, -3600}, {0, 0, 30}, {0, 0, -30},
		{0, 0, 86340}, {0, 0, 86400}, {0, 0, 90000}, {0, 999999999, 0},
		{-62167219200, 0, 0}, {-62167219201, 0, 0},
		{253402300799, 0, 0}, {253402300800, 0, 0},
		{math.MaxInt64 / 2, 123, 3600}, {math.MinInt64 / 2, 456, -3600},
		// math.MinInt zone offset: negating its magnitude overflows back to
		// math.MinInt, the input a magnitude-then-compare bounds check waves
		// through before panicking in append2. The offset axis is int64 (not
		// int32) precisely so this full-width value is reachable. The first
		// seed hits the year-guard short-circuit; the second uses an absolute
		// instant near 2^63s so the ~-2^63s offset lands the local year back in
		// [0,9999] and the offset bounds-check itself is exercised.
		{0, 0, math.MinInt}, {9223371986460624000, 0, math.MinInt},
		{0, 0, math.MaxInt},
	} {
		f.Add(s.sec, s.nsec, s.off)
	}
	f.Fuzz(func(t *testing.T, sec, nsec, off int64) {
		// Keep nsec in time.Unix's documented sane band so the constructed
		// instant is well-defined; the interesting adversarial axes here are
		// the year (via sec) and the zone offset.
		nsec %= 1_000_000_000
		loc := time.FixedZone("fuzz", int(off))
		tm := time.Unix(sec, nsec).In(loc)

		var panicked bool
		var got []byte
		var gotErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Fatalf("AppendTime PANICKED (sec=%d nsec=%d off=%d): %v", sec, nsec, off, r)
				}
			}()
			got, gotErr = AppendTime(nil, tm)
		}()
		if panicked {
			return
		}

		want, wantErr := tm.MarshalJSON()
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("AppendTime error parity broken (sec=%d nsec=%d off=%d): zerojson err=%v, MarshalJSON err=%v", sec, nsec, off, gotErr, wantErr)
		}
		if gotErr == nil {
			if !bytes.Equal(got, want) {
				t.Fatalf("AppendTime bytes differ (sec=%d nsec=%d off=%d): zerojson=%q MarshalJSON=%q", sec, nsec, off, got, want)
			}
			if !json.Valid(got) {
				t.Fatalf("AppendTime emitted invalid JSON: %q", got)
			}
		}
	})
}

// FuzzAPIAppendUUIDBytes feeds all 2^128 raw UUID values (as 16 bytes)
// and requires a valid, canonically-shaped JSON string out. No stdlib
// analogue exists without the uuid dependency, so the check is structural:
// json.Valid, a 36-character 8-4-4-4-12 body, and hex/​dash placement.
func FuzzAPIAppendUUIDBytes(f *testing.F) {
	f.Add(bytes.Repeat([]byte{0x00}, 16))
	f.Add(bytes.Repeat([]byte{0xff}, 16))
	f.Add([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10})
	f.Fuzz(func(t *testing.T, b []byte) {
		var u [16]byte
		copy(u[:], b) // short inputs zero-pad; extra bytes ignored — every [16]byte is in-domain
		got := checkAppendedValue(t, "AppendUUIDBytes", func(dst []byte) []byte {
			return AppendUUIDBytes(dst, u)
		})
		var s string
		if err := json.Unmarshal(got, &s); err != nil {
			t.Fatalf("AppendUUIDBytes output not a JSON string: %q (%v)", got, err)
		}
		if len(s) != 36 {
			t.Fatalf("AppendUUIDBytes body length = %d, want 36: %q", len(s), s)
		}
		for i, c := range []byte(s) {
			switch i {
			case 8, 13, 18, 23:
				if c != '-' {
					t.Fatalf("AppendUUIDBytes: expected '-' at %d, got %q in %q", i, c, s)
				}
			default:
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Fatalf("AppendUUIDBytes: non-lowercase-hex %q at %d in %q", c, i, s)
				}
			}
		}
	})
}

// FuzzAPIAppendDecimalParts sweeps the full coefficient (int64) x exponent
// space. The exponent is mapped into AppendDecimalParts's documented
// accepted range [-maxDecimalExponent, maxDecimalExponent] — outside it a
// panic is the documented contract (a caller bug, not bad input), pinned
// separately by TestAppendDecimalPartsPanicsOutOfRange. Inside the range
// the value must be valid JSON whose exact numeric value equals
// coeff * 10^exp, checked with math/big (no float rounding).
func FuzzAPIAppendDecimalParts(f *testing.F) {
	f.Add(int64(0), int32(0))
	f.Add(int64(0), int32(-2))
	f.Add(int64(18550), int32(-2))
	f.Add(int64(math.MinInt64), int32(maxDecimalExponent))
	f.Add(int64(math.MaxInt64), int32(-maxDecimalExponent))
	f.Add(int64(-1), int32(5))
	f.Fuzz(func(t *testing.T, coeff int64, exp int32) {
		// Map any int32 into [-maxDecimalExponent, maxDecimalExponent].
		span := int32(2*maxDecimalExponent + 1)
		e := exp % span
		if e < 0 {
			e += span
		}
		e -= maxDecimalExponent

		got := checkAppendedValue(t, "AppendDecimalParts", func(dst []byte) []byte {
			return AppendDecimalParts(dst, coeff, e)
		})
		// Exact value check: got (a plain decimal) must equal coeff * 10^e.
		gotRat, ok := new(big.Rat).SetString(string(got))
		if !ok {
			t.Fatalf("AppendDecimalParts(%d, %d) = %q not parseable as a rational", coeff, e, got)
		}
		want := new(big.Rat).SetInt64(coeff)
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absExp(e))), nil)
		if e >= 0 {
			want.Mul(want, new(big.Rat).SetInt(pow))
		} else {
			want.Quo(want, new(big.Rat).SetInt(pow))
		}
		if gotRat.Cmp(want) != 0 {
			t.Fatalf("AppendDecimalParts(%d, %d) = %q, value %s != want %s", coeff, e, got, gotRat, want)
		}
	})
}

func absExp(e int32) int32 {
	if e < 0 {
		return -e
	}
	return e
}

// TestAppendDecimalPartsPanicsOutOfRange pins the documented panic
// contract for the exponent values FuzzAPIAppendDecimalParts maps away
// from: an out-of-range exp is a caller bug and panics rather than
// emitting a value.
func TestAppendDecimalPartsPanicsOutOfRange(t *testing.T) {
	for _, exp := range []int32{maxDecimalExponent + 1, -maxDecimalExponent - 1, math.MaxInt32, math.MinInt32} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("AppendDecimalParts with exp=%d did not panic", exp)
				}
			}()
			AppendDecimalParts(nil, 1, exp)
		}()
	}
}

// FuzzAPIAppendBool is trivial but closes the enumeration: both literals
// are valid JSON and round-trip.
func FuzzAPIAppendBool(f *testing.F) {
	f.Add(true)
	f.Add(false)
	f.Fuzz(func(t *testing.T, v bool) {
		got := checkAppendedValue(t, "AppendBool", func(dst []byte) []byte { return AppendBool(dst, v) })
		var back bool
		if err := json.Unmarshal(got, &back); err != nil || back != v {
			t.Fatalf("AppendBool(%v) = %q, round-trip got %v err %v", v, got, back, err)
		}
	})
}

// FuzzAPIAppendInterface builds a variety of any values out of the fuzz
// bytes and asserts AppendInterface never panics and, on a nil error,
// emits valid JSON. AppendInterface delegates to json.Marshal, so its
// error parity is json.Marshal's by construction; the value here is the
// panic-freedom sweep and the invariant that a nil error implies valid
// output.
func FuzzAPIAppendInterface(f *testing.F) {
	f.Add([]byte("hello"), uint8(0))
	f.Add([]byte{0xff, 0x00, 0x7f}, uint8(3))
	f.Fuzz(func(t *testing.T, b []byte, shape uint8) {
		values := []any{
			nil,
			string(b),
			len(b),
			float64(len(b)),
			b != nil && len(b) > 0 && b[0] > 127,
			map[string]any{"k": string(b), "n": len(b)},
			[]any{string(b), len(b), nil},
			json.RawMessage(b), // may be invalid JSON -> json.Marshal errors, must not panic
		}
		v := values[int(shape)%len(values)]
		var got []byte
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("AppendInterface PANICKED on %#v: %v", v, r)
				}
			}()
			got, err = AppendInterface(nil, v)
		}()
		if err == nil && !json.Valid(got) {
			t.Fatalf("AppendInterface returned nil error but invalid JSON %q for %#v", got, v)
		}
	})
}

// FuzzLexerReadersNeverPanic drives every exported Lexer Read* method with
// arbitrary input bytes (open-domain, unlike the corpus-based truncation
// sweep) in both borrow and copy mode. The sole contract asserted here is
// panic-freedom / no out-of-bounds access — the -race run of this fuzzer
// is the practical bounds check; correctness of the parsed values against
// oracles lives in the conformance module. Each reader gets its own fresh
// Lexer so one reader's position advance can't mask another's bug.
func FuzzLexerReadersNeverPanic(f *testing.F) {
	for _, s := range []string{
		"", " ", "null", "true", "false", `""`, `"x"`, "0", "-1", "1.5e3",
		`"1970-01-01T00:00:00Z"`, `"01234567-89ab-cdef-0123-456789abcdef"`,
		"123.45", `"escA"`, "{", "[", `{"a":1}`, `[1,2,3]`,
		"\"\xff\xfe\"", `"\uD800"`, "1e999999999", strings0x1f,
	} {
		f.Add([]byte(s))
	}
	readers := []struct {
		name string
		call func(l *Lexer)
	}{
		{"ReadBool", func(l *Lexer) { l.ReadBool() }},
		{"ReadString", func(l *Lexer) { l.ReadString() }},
		{"ReadInternedString", func(l *Lexer) { l.ReadInternedString() }},
		{"ReadEnumBytes", func(l *Lexer) { l.ReadEnumBytes() }},
		{"ReadTextBytes", func(l *Lexer) { l.ReadTextBytes() }},
		{"ReadKey", func(l *Lexer) { l.ReadKey() }},
		{"ReadInt8", func(l *Lexer) { l.ReadInt8() }},
		{"ReadInt16", func(l *Lexer) { l.ReadInt16() }},
		{"ReadInt32", func(l *Lexer) { l.ReadInt32() }},
		{"ReadInt64", func(l *Lexer) { l.ReadInt64() }},
		{"ReadUint8", func(l *Lexer) { l.ReadUint8() }},
		{"ReadUint16", func(l *Lexer) { l.ReadUint16() }},
		{"ReadUint32", func(l *Lexer) { l.ReadUint32() }},
		{"ReadUint64", func(l *Lexer) { l.ReadUint64() }},
		{"ReadQuotedInt", func(l *Lexer) { l.ReadQuotedInt() }},
		{"ReadQuotedInt8", func(l *Lexer) { l.ReadQuotedInt8() }},
		{"ReadQuotedInt16", func(l *Lexer) { l.ReadQuotedInt16() }},
		{"ReadQuotedInt32", func(l *Lexer) { l.ReadQuotedInt32() }},
		{"ReadQuotedInt64", func(l *Lexer) { l.ReadQuotedInt64() }},
		{"ReadQuotedUint", func(l *Lexer) { l.ReadQuotedUint() }},
		{"ReadQuotedUint8", func(l *Lexer) { l.ReadQuotedUint8() }},
		{"ReadQuotedUint16", func(l *Lexer) { l.ReadQuotedUint16() }},
		{"ReadQuotedUint32", func(l *Lexer) { l.ReadQuotedUint32() }},
		{"ReadQuotedUint64", func(l *Lexer) { l.ReadQuotedUint64() }},
		{"ReadFloat64", func(l *Lexer) { l.ReadFloat64() }},
		{"ReadNumericBytes", func(l *Lexer) { l.ReadNumericBytes() }},
		{"ReadTime", func(l *Lexer) { l.ReadTime() }},
		{"ReadUUIDBytes", func(l *Lexer) { l.ReadUUIDBytes() }},
		{"ReadInterface", func(l *Lexer) { l.ReadInterface() }},
		{"RawValue", func(l *Lexer) { l.RawValue() }},
		{"Skip", func(l *Lexer) { l.Skip() }},
		{"Finish", func(l *Lexer) { l.Finish() }},
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, borrow := range []bool{false, true} {
			for _, r := range readers {
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							t.Fatalf("%s PANICKED (borrow=%v) on %q: %v", r.name, borrow, data, rec)
						}
					}()
					l := NewLexer(data, borrow)
					r.call(&l)
				}()
			}
			// A read followed by Finish (the real top-level decode shape)
			// must also never panic.
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("ReadInterface+Finish PANICKED (borrow=%v) on %q: %v", borrow, data, rec)
					}
				}()
				l := NewLexer(data, borrow)
				_ = l.ReadInterface()
				l.Finish()
			}()
		}
	})
}

const strings0x1f = "\"\x1f\""
