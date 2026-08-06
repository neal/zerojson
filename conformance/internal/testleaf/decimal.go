// Package testleaf provides a deliberately small custom leaf type used by
// zerojson's conformance tests. It keeps the generic -leaf generator paths
// covered without making the public project depend on a production decimal
// implementation.
package testleaf

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/neal/zerojson"
)

// Decimal stores a decimal as coefficient * 10^exponent. It is intentionally
// only a test fixture, not a general-purpose decimal implementation.
type Decimal struct {
	coefficient int64
	exponent    int32
}

// Zero is the zero Decimal value.
var Zero Decimal

// New constructs a Decimal from coefficient * 10^exponent.
func New(coefficient int64, exponent int32) Decimal {
	return Decimal{coefficient: coefficient, exponent: exponent}
}

// NewFromInt constructs an integer-valued Decimal.
func NewFromInt(value int64) Decimal { return New(value, 0) }

// NewFromString parses a plain decimal string.
func NewFromString(value string) (Decimal, error) {
	coefficient, exponent, err := parse([]byte(value))
	if err != nil {
		return Decimal{}, err
	}
	return New(coefficient, exponent), nil
}

// RequireFromString parses value and panics if it is malformed.
func RequireFromString(value string) Decimal {
	d, err := NewFromString(value)
	if err != nil {
		panic(err)
	}
	return d
}

// IsZero reports whether d represents zero.
func (d Decimal) IsZero() bool { return d.coefficient == 0 }

// String returns the unquoted decimal representation.
func (d Decimal) String() string {
	return string(zerojson.AppendDecimalParts(nil, d.coefficient, d.exponent))
}

// MarshalJSON emits the fixture's quoted decimal representation.
func (d Decimal) MarshalJSON() ([]byte, error) { return Append(nil, &d), nil }

// UnmarshalJSON accepts the same quoted or bare decimal forms as Read.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		data = data[1 : len(data)-1]
	}
	coefficient, exponent, err := parse(data)
	if err != nil {
		return err
	}
	*d = New(coefficient, exponent)
	return nil
}

// Append implements zerojsongen's custom leaf append contract.
func Append(dst []byte, d *Decimal) []byte {
	dst = append(dst, '"')
	dst = zerojson.AppendDecimalParts(dst, d.coefficient, d.exponent)
	return append(dst, '"')
}

// Read implements zerojsongen's custom leaf read contract.
func Read(l *zerojson.Lexer, d *Decimal) {
	b := l.ReadNumericBytes()
	if l.Err() != nil {
		return
	}
	coefficient, exponent, err := parse(b)
	if err != nil {
		l.AddError(fmt.Errorf("test decimal %q: %w", b, err))
		return
	}
	*d = New(coefficient, exponent)
}

// parse accepts the bounded plain-decimal domain used by this fixture. When a
// positive exponent has been expanded into trailing integer zeroes, it peels
// those zeroes until the coefficient fits and restores them as the exponent.
func parse(value []byte) (int64, int32, error) {
	coefficient, exponent, err := zerojson.ParseDecimalParts(value)
	if err == nil || !errors.Is(err, zerojson.ErrDecimalRange) || bytes.IndexByte(value, '.') >= 0 {
		return coefficient, exponent, err
	}

	end := len(value)
	var stripped int32
	for end > 0 && value[end-1] == '0' {
		end--
		stripped++
		coefficient, exponent, err = zerojson.ParseDecimalParts(value[:end])
		if err == nil {
			return coefficient, exponent + stripped, nil
		}
		if !errors.Is(err, zerojson.ErrDecimalRange) {
			return 0, 0, err
		}
	}
	return 0, 0, err
}
