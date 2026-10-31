package zerojson

import (
	"encoding/json"
	"math/bits"
	"math/rand/v2"
	"strings"
	"testing"
)

// TestValidMatchesStdlib pins the grammar corners found while implementing
// Valid — including several that are easy to get wrong (leading zeros,
// bare exponents, lone \u surrogates, raw vs. escaped control bytes,
// invalid UTF-8) — against a live encoding/json.Valid call, not just
// recorded expectations. See conformance's FuzzValidAgainstStdlib for the
// randomized version of this same property.
func TestValidMatchesStdlib(t *testing.T) {
	cases := []string{
		// Numbers: RFC 8259's strict grammar rejects what the trusted-path
		// Lexer's readNumericBytes happily accepts (see numbers.go).
		"0", "-0", "1", "-1", "0.5", "-0.5", "1.0e+10", "1e999999999",
		"123456789012345678901234567890",
		"01", "-01", "1.", ".1", "1.e5", "1e", "1e+", "1e-", "+1", "00", "-",
		// Literals.
		"true", "false", "null", "nul", "tru", "fals", "True", "NaN", "Infinity",
		// Strings: control bytes must be escaped; invalid UTF-8 is
		// accepted (matching stdlib's byte-oriented v1 scanner); \u
		// escapes need exactly 4 hex digits, lone surrogates included.
		`""`, `"ok"`, `"tab\tnl\n"`, `"\uD800"`, `"\uDC00"`, `"𐀀"`,
		"\"\x00\"", "\"\x1f\"", "\"\x20\"", "\"\xff\xfe raw\"", `"unterminated`,
		`"\z"`, `"\u12"`, `"\u12zz"`, `"𐀀"`,
		// Structural.
		"{}", "[]", "  {}  ", `{"a":1}`, `[1,2,3]`, `{"a":[1,{"b":2}]}`,
		`{"a":1,}`, `[1,]`, `{,}`, `{"a":}`, `{:"a"}`, `{"a":"b"`, `[1,2`,
		"", "   ", "\t\n\r ",
		// Trailing content after a complete top-level value.
		`{"id":1}garbage`, `{"id":1}   `, `{"id":1}{"id":2}`, "{}\t\n\r ",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			want := json.Valid([]byte(c))
			got := Valid([]byte(c))
			if (got == nil) != want {
				t.Fatalf("Valid(%q) = %v (valid=%v), want valid=%v", c, got, got == nil, want)
			}
		})
	}
}

// TestValidDepthMatchesStdlib pins the empirically-verified stdlib nesting
// boundary (10000 nested containers is valid, 10001 is not — verified
// directly against encoding/json.Valid, not assumed) for both object and
// array nesting.
func TestValidDepthMatchesStdlib(t *testing.T) {
	nestedArr := func(d int) []byte {
		return []byte(strings.Repeat("[", d) + "1" + strings.Repeat("]", d))
	}
	nestedObj := func(d int) []byte {
		return []byte(strings.Repeat(`{"a":`, d) + "1" + strings.Repeat("}", d))
	}
	for _, d := range []int{1, 9998, 9999, 10000, 10001, 10002} {
		for name, gen := range map[string]func(int) []byte{"array": nestedArr, "object": nestedObj} {
			data := gen(d)
			want := json.Valid(data)
			got := Valid(data)
			if (got == nil) != want {
				t.Fatalf("%s depth=%d: Valid=%v (valid=%v), stdlib valid=%v", name, d, got, got == nil, want)
			}
		}
	}
}

// TestValidNoPanicOnTruncation sweeps every prefix of a representative
// document through Valid, asserting only "no panic" (truncation
// necessarily produces a mix of valid — for a prefix that happens to
// close cleanly — and invalid verdicts, both acceptable; a panic is not).
func TestValidNoPanicOnTruncation(t *testing.T) {
	doc := []byte(`{"a":[1,2.5,"x\ty",{"b":null,"c":true},"🎉"],"d":-1e10}`)
	for i := 0; i <= len(doc); i++ {
		prefix := doc[:i:i]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix [:%d] %q: %v", i, prefix, r)
				}
			}()
			_ = Valid(prefix)
		}()
	}
}

// TestValidAllocs asserts Valid performs zero allocations on valid input —
// the contract documented on Valid's doc comment — as a plain Go test,
// not just a measured-and-noted benchmark number (a sibling codec
// project's codec/allocation_test.go pattern, already used throughout this
// repo's own perf assertions).
func TestValidAllocs(t *testing.T) {
	data := []byte(`{"id":1,"name":"widget","tags":["a","b","c"],"nested":{"x":1.5,"y":[true,false,null]},"note":"tab\tnl\n"}`)
	if err := Valid(data); err != nil {
		t.Fatalf("setup: %v", err)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		if err := Valid(data); err != nil {
			t.Fatalf("Valid: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Valid (valid input) allocations=%v, want 0", allocs)
	}
}

// validDepth is the container nesting depth of valid JSON data.
func validDepth(data []byte) int {
	depth, deepest, inString := 0, 0, false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '{' || c == '[':
			depth++
			deepest = max(deepest, depth)
		case c == '}' || c == ']':
			depth--
		}
	}
	return deepest
}

// checkValidFast holds validFast to its contract on data: it never accepts
// what encoding/json rejects, and it accepts every valid document nested
// at most validFastDepth deep, so Valid does not quietly fall back to
// validExact. validExact must agree with encoding/json.
func checkValidFast(t *testing.T, data []byte) {
	t.Helper()
	want := json.Valid(data)
	if exact := validExact(data) == nil; exact != want {
		t.Fatalf("validExact(%q) valid=%v, encoding/json valid=%v", data, exact, want)
	}
	fast := validFast(data)
	if fast && !want {
		t.Fatalf("validFast accepted invalid %q", data)
	}
	if want && !fast && validDepth(data) <= validFastDepth {
		t.Fatalf("validFast rejected valid %q (depth %d)", data, validDepth(data))
	}
}

// validFastCases put each grammar corner where validFast scans it a word
// at a time as well as within a word of the end.
var validFastCases = []string{
	"0", "-0", "1.5e-3", "01", "1.", "-", "1e+", "true", "false", "null", "nul", "tru e", "nulll", "fals",
	`""`, `"ok"`, `"0123456789abcdef"`, "\"0123456789\x01abcdef\"", "\"0123\x1f\"", "\"\xff\xfe raw\"",
	`"0123456789\n0123456"`, `"0123456789\q0123456"`, `"0123456789\u12G4abcdef"`, `"0123\u00e9"`, `"\"`,
	`"unterminated`, `"012345678901234567`,
	"{}", "[]", "{ }", "[ ]", `{"a":1}`, `{"0123456789abcdef":1}`, `[1,2,3]`, `{"a":[1,{"b":2}]}`,
	`{"a":1,}`, `[1,]`, `[,1]`, `{,}`, `{"a":}`, `{:"a"}`, `{"a" 1}`, `{"a":1 "b":2}`, `[1 2]`, `"a" "b"`,
	`[1}`, `{"a":1]`, `[{]}`, `{"a":[}]}`, `[[]`, `[]]`, `{}}`, `]`, `}`, `[`, `{`, `1,2`, `1]`, `1}`,
	`{1:2}`, `{"a":1,"b"}`, `[true,fals]`, `[-]`, `[01]`, `[1.]`,
	" { \"a\" : [ true , false , null ] , \"b\" : { } } \t", "", "   ", "\t\n\r ", `{"id":1}garbage`,
}

func TestValidFastMatchesExact(t *testing.T) {
	pad := strings.Repeat(" ", 16)
	for _, c := range validFastCases {
		for _, doc := range []string{c, "[" + c + "]", `{"k":` + c + `}`, c + pad, "[" + c + "," + pad + "0]", `{"0123456789abcdef":` + c + `,"z":0}`} {
			checkValidFast(t, []byte(doc))
		}
	}
}

// TestValidFastDepthBoundary nests alternating objects and arrays across
// the stack's word boundaries up to one past validFastDepth, with matched
// and swapped closers.
func TestValidFastDepthBoundary(t *testing.T) {
	nest := func(d int, swap bool) []byte {
		var open, closers strings.Builder
		for i := range d {
			if i%2 == 0 {
				open.WriteString(`{"a":`)
			} else {
				open.WriteString("[")
			}
		}
		for i := d - 1; i >= 0; i-- {
			obj := i%2 == 0
			if swap && i == 0 {
				obj = !obj
			}
			if obj {
				closers.WriteString("}")
			} else {
				closers.WriteString("]")
			}
		}
		return []byte(open.String() + "1" + closers.String())
	}
	for _, d := range []int{1, 63, 64, 65, 127, 128, 129, 255, 256, 257} {
		for _, swap := range []bool{false, true} {
			data := nest(d, swap)
			checkValidFast(t, data)
			if !swap && d > validFastDepth && validFast(data) {
				t.Fatalf("validFast accepted depth %d, beyond its %d-level stack", d, validFastDepth)
			}
		}
	}
}

func FuzzValidFast(f *testing.F) {
	for _, c := range validFastCases {
		f.Add([]byte(c))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkValidFast(t, data)
	})
}

// TestStringSpecialFindsFirstSpecialByte checks stringSpecial's lowest
// marked lane against a byte-by-byte search: for every lane, every byte
// value there, plain bytes below it, and random bytes above it, plus random
// words drawn mostly from the bytes near the thresholds.
func TestStringSpecialFindsFirstSpecialByte(t *testing.T) {
	special := func(c byte) bool { return c == '"' || c == '\\' || c < 0x20 }
	check := func(v uint64) {
		want := 8
		for i := range 8 {
			if special(byte(v >> (8 * i))) {
				want = i
				break
			}
		}
		if got := bits.TrailingZeros64(stringSpecial(v)) >> 3; got != want {
			t.Fatalf("stringSpecial(%#016x): first special lane %d, want %d", v, got, want)
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	plain := func() uint64 {
		for {
			if c := byte(rng.Uint32()); !special(c) {
				return uint64(c)
			}
		}
	}
	near := []byte{0x00, 0x01, 0x1e, 0x1f, 0x20, 0x21, 0x22, 0x23, 0x5b, 0x5c, 0x5d, 0x7f, 0x80, 0xa2, 0xdc, 0xff}
	for lane := range 8 {
		for c := range 256 {
			for range 64 {
				var v uint64
				for i := range lane {
					v |= plain() << (8 * i)
				}
				v |= uint64(c) << (8 * lane)
				for i := lane + 1; i < 8; i++ {
					v |= uint64(near[rng.IntN(len(near))]) << (8 * i)
				}
				check(v)
			}
		}
	}
	for range 1 << 20 {
		var v uint64
		for i := range 8 {
			c := byte(rng.Uint32())
			if rng.IntN(2) == 0 {
				c = near[rng.IntN(len(near))]
			}
			v |= uint64(c) << (8 * i)
		}
		check(v)
	}
}

func BenchmarkValid(b *testing.B) {
	data := []byte(`{"id":1,"name":"AAPL","tags":["a","b","c"],"nested":{"x":1.5,"y":[true,false,null]},"note":"tab\tnl\n"}`)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := Valid(data); err != nil {
			b.Fatal(err)
		}
	}
}

// Keep validation costs visible across token mixes and whitespace layouts.
// Both implementations see identical inputs; setup is outside the timer.
func BenchmarkValidShapes(b *testing.B) {
	for _, tc := range []struct{ name, input string }{
		{"literal", `true`},
		{"literals", `[` + strings.Repeat(`true,false,null,`, 31) + `null]`},
		{"short_keys", `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}`},
		{"whitespace", " { \n\"a\" : [ true , false , null ] , \"b\" : { } } \t"},
		{"long_string", `"` + strings.Repeat("abcdefgh", 512) + `"`},
		{"escaped_string", `"` + strings.Repeat(`a\n\u0041`, 128) + `"`},
		{"numbers", `[` + strings.Repeat(`12345.6789e-12,`, 63) + `0]`},
		{"nested", strings.Repeat(`{"a":[`, 64) + `null` + strings.Repeat(`]}`, 64)},
	} {
		data := []byte(tc.input)
		if !json.Valid(data) || Valid(data) != nil {
			b.Fatalf("invalid benchmark fixture %s", tc.name)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.Run("zerojson", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					if err := Valid(data); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("stdlib", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					if !json.Valid(data) {
						b.Fatal("invalid JSON")
					}
				}
			})
		})
	}
}

// TestSyntaxErrorError calls SyntaxError.Error via a real Valid
// failure.
func TestSyntaxErrorError(t *testing.T) {
	err := Valid([]byte("{bad"))
	if err == nil {
		t.Fatal("Valid accepted malformed input")
	}
	if err.Error() == "" {
		t.Fatal("SyntaxError.Error() returned empty string")
	}
	if _, ok := err.(*SyntaxError); !ok {
		t.Fatalf("Valid returned %T, want *SyntaxError", err)
	}
}
