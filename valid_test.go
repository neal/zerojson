package zerojson

import (
	"encoding/json"
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
