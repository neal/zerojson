package zerojson

// Appender is implemented by hand-written types that want to join
// zerojson's append-style encode path directly, without a -leaf generator
// registration. The generator detects this method on a field's type the
// same way it detects MarshalJSON, but with precedence above it: a type
// offering both AppendZJSON/ReadZJSON and MarshalJSON/UnmarshalJSON gets
// the fast append/read path.
//
// AppendZJSON must follow the same contract as every other zerojson
// Append* function: dst is the buffer to append to (never reset), preserving its prefix.
// Buffer growth may allocate and copy. The appended bytes must be one valid
// JSON value; the generator does not validate Appender output. On error, the
// returned buffer holds a partial encoding and must be discarded. This is
// the exact signature the generator emits for every generated type, so a
// generated type already satisfies Appender; the generator still
// dispatches to same-package generated types directly rather than through
// this interface.
type Appender interface {
	AppendZJSON(dst []byte) ([]byte, error)
}

// Reader is Appender's decode counterpart. It must be implemented on a
// pointer receiver (it mutates the receiver) and must report errors the
// same way every other leaf Read function does — via the Lexer's Fail or
// AddError, not a return value — so the generator can emit it as a plain
// decode statement instead of an error-checked one.
type Reader interface {
	ReadZJSON(l *Lexer)
}

// QuotedInt64Marker is an explicit promise used by zerojsongen for the common
// int64-as-quoted-base-10 wire convention. A named int64 type that implements
// MarshalJSON/UnmarshalJSON and this marker gets the direct no-allocation
// integer path; without the marker its JSON methods are delegated normally.
// The marker must be implemented on a value receiver so map values carry the
// same promise even though they are not addressable.
type QuotedInt64Marker interface {
	ZeroJSONQuotedInt64()
}
