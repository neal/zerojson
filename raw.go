package zerojson

import (
	"bytes"
	"errors"
)

// Raw holds a JSON value without materializing its contents. Decoding
// grammar-checks and captures the exact span; generated encoding validates
// non-empty values and appends them verbatim. Nil and empty Raw values encode
// as null, unlike an empty non-nil encoding/json.RawMessage.
//
// In borrow mode, the bytes alias the input and must remain unmodified while
// the result is used. Copy mode detaches them. JSON null stores the literal
// bytes in a Raw field and clears a *Raw field.
//
// Unknown fields in a projection are still grammar-checked and skipped;
// Raw is for fields whose bytes must also be retained and re-encoded.
type Raw []byte

// AppendRaw appends *r verbatim, or null if *r is nil or empty.
// It does not validate non-empty values; use AppendRawChecked for unchecked
// input. Generated codecs use the checked form.
func AppendRaw(dst []byte, r *Raw) []byte {
	if len(*r) == 0 {
		return append(dst, "null"...)
	}
	return append(dst, *r...)
}

// ReadRawNull stores the literal null in r, for a value-typed Raw field
// whose member is null: json.RawMessage's UnmarshalJSON is invoked with
// "null" by encoding/json in that position and ends up holding those four
// bytes, and Raw matches it (a *Raw is cleared instead, like any pointer
// field). Aliases the input in Borrow mode, copies otherwise. Called by
// generated code right after IsNull consumed the literal.
func ReadRawNull(l *Lexer, r *Raw) {
	b := l.NullBytes()
	if l.Borrow {
		*r = b
		return
	}
	*r = bytes.Clone(b)
}

// ReadRaw captures the next JSON value's exact bytes (object, array,
// string, number, bool, or null — Skip's usual repertoire) into r: a
// copy, unless the Lexer is in Borrow mode, in which case r aliases the
// input buffer. It satisfies the zerojson leaf read contract and is
// registered as a built-in leaf for Raw.
func ReadRaw(l *Lexer, r *Raw) {
	b := l.RawValue()
	if l.Err() != nil {
		return
	}
	if l.Borrow {
		*r = b
		return
	}
	*r = bytes.Clone(b)
}

// MarshalJSON returns null for nil/empty Raw, or the stored bytes otherwise.
// When called through encoding/json, that package validates and reformats
// the returned JSON according to its own encoding policy.
func (r Raw) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON stores a copy of data, as json.RawMessage does (so a
// null member leaves the bytes null in it).
func (r *Raw) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("zerojson.Raw: UnmarshalJSON on nil pointer")
	}
	// The old slice may be borrowed from somebody else's input buffer.
	// Reusing its capacity would mutate that buffer during a later stdlib
	// decode. Detach even when the old capacity is sufficient.
	*r = bytes.Clone(data)
	return nil
}

// AppendRawChecked is AppendRaw with the validation encoding/json applies
// to a RawMessage: a non-empty Raw that is not a single valid JSON value
// is an encode error rather than invalid output. Generated code uses this
// for every Raw field; the check is skipped for nil/empty (null) values.
func AppendRawChecked(dst []byte, r *Raw) ([]byte, error) {
	if len(*r) == 0 {
		return append(dst, "null"...), nil
	}
	if err := Valid(*r); err != nil {
		return dst, err
	}
	return append(dst, *r...), nil
}
