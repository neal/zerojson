package zerojson

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// maxDecimalExponent bounds the exponent ParseDecimalParts accepts (either
// direction: at most 32 significant zero digits of left-padding, or 32
// fractional digits), chosen generously above any realistic real-world
// price/quantity scale while still being finite. Without a cap, a parser
// reading attacker- or bug-supplied JSON into an unbounded-exponent
// decimal type has no limit on how many zero digits it will materialize —
// a latent allocation/CPU DoS surface. This bound turns that failure mode
// into an ordinary decode error.
const maxDecimalExponent = 32

// ErrMalformedDecimal reports that ParseDecimalParts was given text that is
// not a plain decimal number (optional '-', digits, optional '.', digits).
var ErrMalformedDecimal = errors.New("zerojson: malformed decimal")

// ErrDecimalRange reports that ParseDecimalParts was given a decimal whose
// coefficient overflows int64 or whose exponent exceeds maxDecimalExponent.
var ErrDecimalRange = errors.New("zerojson: decimal out of range")

// AppendDecimalParts appends coeff * 10^exp in plain decimal notation —
// dependency-free, no big.Int, no external decimal library. The
// coefficient's scale is preserved: AppendDecimalParts(dst, 18550, -2)
// appends "185.50", and AppendDecimalParts(dst, 0, -2) appends "0.00".
// This is an infallible append-style leaf helper (like every other
// zerojson Append* function): exp outside [-maxDecimalExponent,
// maxDecimalExponent] panics, since producing it would mean a caller bug
// (an out-of-range value being encoded), not bad input.
func AppendDecimalParts(dst []byte, coeff int64, exp int32) []byte {
	if exp < -maxDecimalExponent || exp > maxDecimalExponent {
		panic(fmt.Sprintf("zerojson.AppendDecimalParts: exponent %d out of range", exp))
	}

	abs, neg := absInt64(coeff)
	if neg {
		dst = append(dst, '-')
	}

	var tmp [20]byte
	digits := strconv.AppendUint(tmp[:0], abs, 10)
	if exp >= 0 {
		dst = append(dst, digits...)
		// A zero coefficient is the value 0 at any scale: emit just "0",
		// never "0" followed by exp trailing zeros ("000"), which is not a
		// valid JSON number. Only a non-zero coefficient carries its scale
		// into the integer part.
		if abs != 0 {
			for range exp {
				dst = append(dst, '0')
			}
		}
		return dst
	}

	scale := int(-exp)
	if scale < len(digits) {
		intDigits := len(digits) - scale
		dst = append(dst, digits[:intDigits]...)
		dst = append(dst, '.')
		return append(dst, digits[intDigits:]...)
	}

	dst = append(dst, '0', '.')
	for range scale - len(digits) {
		dst = append(dst, '0')
	}
	return append(dst, digits...)
}

// ParseDecimalParts parses a plain decimal value (optional leading '-', an
// integer part, and an optional '.'-prefixed fractional part — no
// exponent notation) into coeff * 10^exp, preserving fractional scale:
// "185.50" returns coeff=18550, exp=-2. It never allocates and never
// panics — malformed input or an out-of-range coefficient/exponent
// returns ErrMalformedDecimal/ErrDecimalRange, matching the Lexer's
// contract of reporting errors rather than panicking on untrusted input.
// Use a decimal library's own parser directly when values may exceed
// int64 coefficient precision.
func ParseDecimalParts(b []byte) (coeff int64, exp int32, err error) {
	if len(b) == 0 {
		return 0, 0, ErrMalformedDecimal
	}

	i := 0
	neg := false
	switch b[0] {
	case '-':
		neg = true
		i++
		if i == len(b) {
			return 0, 0, ErrMalformedDecimal
		}
	case '+':
		return 0, 0, ErrMalformedDecimal
	}

	var n uint64
	var digitCount, wholeDigits, fracDigits int
	seenDot := false
	limit := uint64(math.MaxInt64)
	if neg {
		limit++
	}

	for ; i < len(b); i++ {
		c := b[i]
		if c == '.' {
			if seenDot {
				return 0, 0, ErrMalformedDecimal
			}
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, 0, ErrMalformedDecimal
		}

		digit := uint64(c - '0')
		if n > (limit-digit)/10 {
			return 0, 0, fmt.Errorf("%w: coefficient", ErrDecimalRange)
		}
		n = n*10 + digit
		digitCount++
		if seenDot {
			fracDigits++
			if fracDigits > maxDecimalExponent {
				return 0, 0, fmt.Errorf("%w: exponent", ErrDecimalRange)
			}
		} else {
			wholeDigits++
		}
	}

	if digitCount == 0 || wholeDigits == 0 || (seenDot && fracDigits == 0) {
		return 0, 0, ErrMalformedDecimal
	}

	if neg {
		if n == uint64(math.MaxInt64)+1 {
			coeff = math.MinInt64
		} else {
			coeff = -int64(n)
		}
	} else {
		coeff = int64(n)
	}
	return coeff, -int32(fracDigits), nil
}

func absInt64(v int64) (uint64, bool) {
	if v >= 0 {
		return uint64(v), false
	}
	if v == math.MinInt64 {
		return 1 << 63, true
	}
	return uint64(-v), true
}
