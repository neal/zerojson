package zerojson

import "bytes"

// Raw holds an unprocessed JSON value: it decodes by capturing the exact
// byte span of the next value and encodes by appending those bytes back
// verbatim — the caller guarantees they are valid JSON, matching
// encoding/json.RawMessage's contract exactly (including its "nil
// encodes as null, everything else is appended as-is, even if empty or
// invalid" behavior).
//
// This enables passthrough/projection patterns: a struct that declares
// only the fields a reader actually needs, routing the rest through
// unexamined —
//
//	type Route struct {
//		Table  string       `json:"table"`
//		Set    zerojson.Raw `json:"set"`
//	}
//
// decodes only Table and Set (the rest of the wire object is skipped by
// the ordered decoder's inline-skip, or the fallback's default case)
// without materializing or even parsing the skipped fields, and
// re-encodes Set byte-for-byte unchanged — the passthrough/routing case
// this was built for.
//
// In borrow-mode decodes the bytes alias the input buffer (same rule as
// borrowed strings — the caller must guarantee the input outlives the
// decoded struct); in copy-mode decodes they're copied.
type Raw []byte

// AppendRaw appends r to dst verbatim, or the literal null if r is nil
// (matching json.RawMessage.MarshalJSON). It satisfies the zerojson leaf
// append contract and is registered as a built-in leaf for Raw.
func AppendRaw(dst []byte, r *Raw) []byte {
	if *r == nil {
		return append(dst, "null"...)
	}
	return append(dst, *r...)
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
