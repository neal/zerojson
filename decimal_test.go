package zerojson

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestAppendDecimalPartsVectors(t *testing.T) {
	cases := []struct {
		coeff int64
		exp   int32
		want  string
	}{
		{18550, -2, "185.50"},
		{0, -2, "0.00"},
		{0, 0, "0"},
		{1, 0, "1"},
		{-1, 0, "-1"},
		{100, 2, "10000"},
		{-18550, -2, "-185.50"},
		{5, -3, "0.005"},
		{123, -5, "0.00123"},
		{math.MaxInt64, 0, "9223372036854775807"},
		{math.MinInt64, 0, "-9223372036854775808"},
		{math.MinInt64, -19, "-0.9223372036854775808"},
	}
	for _, c := range cases {
		got := string(AppendDecimalParts(nil, c.coeff, c.exp))
		if got != c.want {
			t.Errorf("AppendDecimalParts(%d, %d): want %q got %q", c.coeff, c.exp, c.want, got)
		}
	}
}

// TestAppendDecimalPartsZeroCoefficient guards against a zero coefficient
// with a positive exponent emitting "0" followed by exp trailing zeros
// ("000"), which is not a valid JSON number: it must emit "0" for every
// exponent.
func TestAppendDecimalPartsZeroCoefficient(t *testing.T) {
	for exp := int32(-maxDecimalExponent); exp <= maxDecimalExponent; exp++ {
		got := string(AppendDecimalParts(nil, 0, exp))
		if !json.Valid([]byte(got)) {
			t.Errorf("AppendDecimalParts(0, %d) = %q is not valid JSON", exp, got)
		}
		// The value is 0 at every scale; it must parse back to 0.
		coeff, _, err := ParseDecimalParts([]byte(got))
		if err != nil || coeff != 0 {
			t.Errorf("AppendDecimalParts(0, %d) = %q: reparse coeff=%d err=%v", exp, got, coeff, err)
		}
		if exp >= 0 && got != "0" {
			t.Errorf("AppendDecimalParts(0, %d) = %q, want %q", exp, got, "0")
		}
	}
}

// TestAppendDecimalPartsEdgeGrid sweeps the coefficient/exponent grid the
// finding asks for — coeff in {0, +/-1, +/-max}, exp across the full
// [-bound, +bound] range — and requires every output to be valid JSON and
// to round-trip through ParseDecimalParts with value equality (allowing the
// documented scale normalization: a positive exponent widens the integer
// part, so the reparsed value has exp 0 but the same numeric magnitude).
func TestAppendDecimalPartsEdgeGrid(t *testing.T) {
	coeffs := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	for _, coeff := range coeffs {
		for exp := int32(-maxDecimalExponent); exp <= maxDecimalExponent; exp++ {
			out := string(AppendDecimalParts(nil, coeff, exp))
			if !json.Valid([]byte(out)) {
				t.Errorf("AppendDecimalParts(%d, %d) = %q is not valid JSON", coeff, exp, out)
				continue
			}
			// A positive exponent multiplies the coefficient by 10^exp,
			// producing a value that may exceed int64 (e.g. MaxInt64 * 100),
			// which ParseDecimalParts legitimately rejects as out of range —
			// so only assert round-trip where the value stays representable.
			rc, re, err := ParseDecimalParts([]byte(out))
			if err != nil {
				if errors.Is(err, ErrDecimalRange) {
					continue
				}
				t.Errorf("AppendDecimalParts(%d, %d) = %q: reparse error %v", coeff, exp, out, err)
				continue
			}
			// Re-append the reparsed parts; must be byte-identical (canonical
			// form is stable under a round trip).
			back := string(AppendDecimalParts(nil, rc, re))
			if back != out {
				t.Errorf("AppendDecimalParts(%d, %d) = %q, round-trip via (%d,%d) = %q", coeff, exp, out, rc, re, back)
			}
		}
	}
}

func TestAppendDecimalPartsExponentBoundPanics(t *testing.T) {
	for _, exp := range []int32{maxDecimalExponent + 1, -(maxDecimalExponent + 1)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("exp %d: expected panic", exp)
				}
			}()
			AppendDecimalParts(nil, 1, exp)
		}()
	}
	// Boundary values must not panic.
	AppendDecimalParts(nil, 1, maxDecimalExponent)
	AppendDecimalParts(nil, 1, -maxDecimalExponent)
}

func TestParseDecimalPartsRoundTrip(t *testing.T) {
	cases := []string{
		"185.50", "0.00", "0", "1", "-1", "10000", "-185.50",
		"0.005", "0.00123", "9223372036854775807", "-9223372036854775808",
		"00.10", "-0.0", "000123",
	}
	for _, c := range cases {
		coeff, exp, err := ParseDecimalParts([]byte(c))
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c, err)
			continue
		}
		back := string(AppendDecimalParts(nil, coeff, exp))
		coeff2, exp2, err2 := ParseDecimalParts([]byte(back))
		if err2 != nil {
			t.Errorf("%q: round-trip reparse error: %v (intermediate %q)", c, err2, back)
			continue
		}
		if coeff2 != coeff || exp2 != exp {
			t.Errorf("%q: round-trip mismatch: parsed (%d,%d), reparsed (%d,%d) via %q", c, coeff, exp, coeff2, exp2, back)
		}
	}
}

func TestParseDecimalPartsMalformed(t *testing.T) {
	cases := []string{
		"", "-", "+", "+1", ".", "1.", ".1", "1.2.3", "1a", "a", "1-1", "1.-1",
		"--1", "1..", "-",
	}
	for _, c := range cases {
		_, _, err := ParseDecimalParts([]byte(c))
		if !errors.Is(err, ErrMalformedDecimal) {
			t.Errorf("%q: want ErrMalformedDecimal, got %v", c, err)
		}
	}
}

func TestParseDecimalPartsRange(t *testing.T) {
	// Fractions of all zeros but a trailing '1' keep the coefficient tiny
	// (well within int64) so these exercise only the fracDigits/exponent
	// bound, not coefficient overflow.
	zeros := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = '0'
		}
		return string(b)
	}

	cases := []string{
		"99999999999999999999",                 // coefficient overflow
		"-99999999999999999999",                // coefficient overflow, negative
		"9223372036854775808",                  // MaxInt64 + 1
		"0." + zeros(maxDecimalExponent) + "1", // one past the exponent bound
	}

	for _, c := range cases {
		_, _, err := ParseDecimalParts([]byte(c))
		if !errors.Is(err, ErrDecimalRange) {
			t.Errorf("%q: want ErrDecimalRange, got %v", c, err)
		}
	}

	// Exactly maxDecimalExponent fractional digits is fine.
	ok := "0." + zeros(maxDecimalExponent-1) + "1"
	if _, _, err := ParseDecimalParts([]byte(ok)); err != nil {
		t.Errorf("boundary fraction length (%q): unexpected error: %v", ok, err)
	}
}

func TestParseDecimalPartsMinInt64(t *testing.T) {
	coeff, exp, err := ParseDecimalParts([]byte("-9223372036854775808"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coeff != math.MinInt64 || exp != 0 {
		t.Errorf("want (%d, 0) got (%d, %d)", int64(math.MinInt64), coeff, exp)
	}
}
