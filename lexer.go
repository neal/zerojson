package zerojson

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Lexer is a pull parser over a complete in-memory JSON document.
// Ordinary string and number readers are intentionally lenient; they are not
// a substitute for Valid. ReadKey, Skip, RawValue, and Finish check grammar
// for keys, skipped/captured values, and trailing content respectively.
type Lexer struct {
	data     []byte
	pos      int
	err      error
	depth    int
	path     []string    // field names the pending error unwound through, innermost first
	wrapped  *FieldError // Err()'s cached annotation of err for the current path
	wrappedN int         // len(path) the cache was built for

	// Borrow makes string reads return views into the input buffer
	// instead of copies. Callers must guarantee the input outlives the
	// decoded struct and remains unmodified while any result refers to it.
	Borrow bool
}

// maxDepth bounds object/array nesting on every decode path — the
// generated decoders (EnterObject/EnterArray and the ordered decoder's
// Enter/Leave), ReadInterface, and Skip/Valid, which thread the current
// depth through so a skipped subtree is measured from where it actually
// sits in the document, not from zero — to prevent stack exhaustion from
// adversarial deeply-nested input. Matches encoding/json's limit exactly
// (a document 10000 levels deep is accepted, 10001 is not), at every
// nesting position, including empty containers. This depth check does not
// make the ordinary value readers grammar-strict; use Valid for that.
const maxDepth = 10000

func NewLexer(data []byte, borrow bool) Lexer {
	return Lexer{data: data, Borrow: borrow}
}

// Err returns the first decode error, annotated with the JSON field path
// it occurred under (see AnnotateField) — e.g.
// `field legs.price: zerojson: expected number at offset 41`. The
// annotation is applied lazily, once, so the nil check on the hot path
// stays a single load.
func (l *Lexer) Err() error {
	if l.err == nil {
		return nil
	}
	if len(l.path) == 0 {
		return l.err
	}
	if l.wrapped != nil && l.wrappedN == len(l.path) {
		return l.wrapped
	}
	// Annotations are recorded innermost-first as the error unwinds; the
	// path is rebuilt (only on the error path) each time a new enclosing
	// field is added, always wrapping the raw error once.
	var b strings.Builder
	for i := len(l.path) - 1; i >= 0; i-- {
		b.WriteString(l.path[i])
		if i > 0 {
			b.WriteByte('.')
		}
	}
	l.wrapped = &FieldError{Path: b.String(), Err: l.err}
	l.wrappedN = len(l.path)
	return l.wrapped
}

// FieldError wraps a decode error with the JSON path of the field being
// decoded when it occurred ("legs.price" — container names are not
// suffixed with [] and element indices are not recorded), for on-call diagnosis
// without the document. Path holds exactly that dotted path, with no
// prefix, so it can be compared or used as a metric label; Error adds
// the "field " prefix. Unwrap yields the underlying error.
type FieldError struct {
	Path string
	Err  error
}

func (e *FieldError) Error() string { return "field " + e.Path + ": " + e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// AnnotateField records that the pending error occurred while decoding
// the member called name; generated decoders call it as the error unwinds
// through each enclosing object, building the path Err reports. name is
// copied (it may alias the input).
func (l *Lexer) AnnotateField(name []byte) {
	if l.err != nil {
		l.path = append(l.path, string(name))
	}
}

// AnnotateFieldString is AnnotateField for a compile-time field name.
func (l *Lexer) AnnotateFieldString(name string) {
	if l.err != nil {
		l.path = append(l.path, name)
	}
}

// AddError records an error from a delegated UnmarshalJSON call.
func (l *Lexer) AddError(err error) {
	if l.err == nil && err != nil {
		l.err = err
	}
}

func (l *Lexer) setErr(format string, args ...any) {
	if l.err == nil {
		l.err = fmt.Errorf("zerojson: "+format+" at offset %d", append(args, l.pos)...)
	}
}

// Fail records a decode error with source-offset context, for custom leaf
// read functions.
func (l *Lexer) Fail(format string, args ...any) {
	l.setErr(format, args...)
}

func (l *Lexer) skipWS() {
	// Fast path: compact JSON has no whitespace between tokens, and every
	// non-whitespace JSON byte is > 0x20.
	if l.pos < len(l.data) && l.data[l.pos] > ' ' {
		return
	}
	l.skipWSSlow()
}

func (l *Lexer) skipWSSlow() {
	for l.pos < len(l.data) {
		switch l.data[l.pos] {
		case ' ', '\t', '\n', '\r':
			l.pos++
		default:
			return
		}
	}
}

// Delim consumes an expected delimiter. It does no depth accounting: a
// hand-written Reader that walks an object or array must use
// EnterObject/NextMember or EnterArray/NextElem (which count the level
// and enforce the nesting limit), not Delim('{') paired with NextMember —
// that pairing would leave NextMember's decrement unmatched.
func (l *Lexer) Delim(c byte) {
	l.skipWS()
	if l.pos >= len(l.data) || l.data[l.pos] != c {
		l.setErr("expected %q", string(c))
		return
	}
	l.pos++
}

// IsDelim reports whether the next non-space byte is c, without consuming.
func (l *Lexer) IsDelim(c byte) bool {
	l.skipWS()
	return l.pos < len(l.data) && l.data[l.pos] == c
}

// WantComma consumes an optional comma between object members.
func (l *Lexer) WantComma() {
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == ',' {
		l.pos++
	}
}

const (
	wordNull = uint32('n') | uint32('u')<<8 | uint32('l')<<16 | uint32('l')<<24
	wordTrue = uint32('t') | uint32('r')<<8 | uint32('u')<<16 | uint32('e')<<24
	wordAlse = uint32('a') | uint32('l')<<8 | uint32('s')<<16 | uint32('e')<<24
)

// IsNull consumes a null literal if present and reports whether it did.
// It is called before every field dispatch, so the non-null case exits on
// the first byte.
func (l *Lexer) IsNull() bool {
	l.skipWS()
	if l.pos >= len(l.data) || l.data[l.pos] != 'n' {
		return false
	}
	if l.pos+4 <= len(l.data) && binary.LittleEndian.Uint32(l.data[l.pos:]) == wordNull {
		l.pos += 4
		return true
	}
	return false
}

// NullBytes returns the four bytes of the null literal IsNull just
// consumed (aliasing the input), for delivering the literal to a
// value-typed json.Unmarshaler exactly as encoding/json does. Valid only
// immediately after IsNull returned true.
func (l *Lexer) NullBytes() []byte { return l.data[l.pos-4 : l.pos] }

// EnterObject consumes '{' and reports whether the object has members; an
// empty object's '}' is consumed too. Malformed input, or nesting beyond
// maxDepth, records an error and returns false. A non-empty object counts
// one level of depth until NextMember consumes its closing '}'.
func (l *Lexer) EnterObject() bool {
	l.Delim('{')
	if l.err != nil {
		return false
	}
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '}' {
		// An empty object is still a nesting level for the limit's
		// purposes (Valid and encoding/json count it), so `{}` at the
		// limit is rejected even though nothing is entered.
		if !l.DepthOK() {
			return false
		}
		l.pos++
		return false
	}
	return l.Enter()
}

// NextMember consumes the ',' between object members (returning true) or
// the closing '}' (returning false, and leaving the object's depth level).
// Unlike the WantComma/IsDelim pair it replaces, it costs a single
// whitespace skip per member.
func (l *Lexer) NextMember() bool {
	l.skipWS()
	if l.pos < len(l.data) {
		switch l.data[l.pos] {
		case ',':
			l.pos++
			return true
		case '}':
			l.pos++
			l.depth--
			return false
		}
	}
	l.setErr("expected ',' or '}'")
	return false
}

// EnterArray consumes '[' and reports whether the array has elements; an
// empty array's ']' is consumed too. Depth accounting is as for
// EnterObject.
func (l *Lexer) EnterArray() bool {
	l.Delim('[')
	if l.err != nil {
		return false
	}
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == ']' {
		if !l.DepthOK() {
			return false
		}
		l.pos++
		return false
	}
	return l.Enter()
}

// NextElem consumes the ',' between array elements (returning true) or the
// closing ']' (returning false, and leaving the array's depth level).
func (l *Lexer) NextElem() bool {
	l.skipWS()
	if l.pos < len(l.data) {
		switch l.data[l.pos] {
		case ',':
			l.pos++
			return true
		case ']':
			l.pos++
			l.depth--
			return false
		}
	}
	l.setErr("expected ',' or ']'")
	return false
}

// ReadBool parses a boolean literal with single word compares.
func (l *Lexer) ReadBool() bool {
	l.skipWS()
	if l.pos+4 <= len(l.data) && binary.LittleEndian.Uint32(l.data[l.pos:]) == wordTrue {
		l.pos += 4
		return true
	}
	if l.pos+5 <= len(l.data) && l.data[l.pos] == 'f' && binary.LittleEndian.Uint32(l.data[l.pos+1:]) == wordAlse {
		l.pos += 5
		return false
	}
	l.setErr("expected bool")
	return false
}

// Finish verifies nothing but optional whitespace remains at the Lexer's
// current position, and records an error otherwise — "invalid character
// %q after top-level value", the same condition (and message) as
// encoding/json.Unmarshal's trailing-garbage rejection, and matching
// easyjson's jlexer.Lexer.Consumed. Generated top-level decode entry
// points (UnmarshalZJSON(Borrow), a pooled Decoder's Decode(Borrow)) call
// this exactly once, after the top-level value has been decoded; it must
// never be called from a nested/recursive decode, which has no business
// opining on what comes after the value it was asked to parse.
func (l *Lexer) Finish() {
	if l.err != nil {
		return
	}
	l.skipWS()
	if l.pos < len(l.data) {
		l.setErr("invalid character %q after top-level value", l.data[l.pos])
	}
}

// SkipObject requires the next value to be a JSON object and skips it
// (grammar-validated, with any members ignored), recording an error if the
// next value is anything else. This is the decode contract of a struct{}
// (set-map) value: encoding/json and easyjson both accept an object of any
// contents (unknown members ignored) or null for such a value, but reject a
// number, array, or string. null is consumed by the caller's IsNull check
// before this is reached, so only the object-or-error decision remains here.
func (l *Lexer) SkipObject() {
	if l.err != nil {
		return
	}
	if !l.IsDelim('{') {
		l.setErr("expected object")
		return
	}
	l.Skip()
}

// RawValue returns the raw bytes of the next value (quotes included for
// strings), for delegation to a type's own UnmarshalJSON.
func (l *Lexer) RawValue() []byte {
	l.skipWS()
	start := l.pos
	l.Skip()
	return l.data[start:l.pos]
}

// Skip discards the next value (used for unknown keys and, via RawValue,
// captured Raw spans). Unlike the known-field decode primitives, Skip
// grammar-checks what it discards, by delegating to Valid's own grammar
// walk (validValue, valid.go) rather than duplicating it: numbers must
// satisfy RFC 8259's number grammar, literals must match exactly, strings
// must have valid escape sequences and no raw unescaped control bytes, and
// nested object/array content is fully walked (recursively, to the same
// maxDepth cap Valid enforces — measured from the Lexer's CURRENT depth,
// so a subtree skipped 3 levels down has 3 fewer levels to spend, exactly
// as Valid would count it) rather than merely brace/bracket-counted.
//
// So every byte a decode does NOT hand to a declared field's own reader
// is grammar-checked: unknown members (a genuinely unknown field's value,
// and the ordered decoder's inline skip of one, both go through this
// method), plus Finish's trailing-data check. Declared fields' readers
// are deliberately more lenient (leading zeros in a known int, a raw
// control byte in a known string — see the compatibility notes), which is
// why a successful decode still does not certify the payload as strict
// JSON; Valid does that, in one pass, when it matters. The one thing
// this deliberately does not touch is the known-field decode primitives
// (readStringBytes, readNumericBytes) — those stay lenient and fast; only
// content the caller never otherwise inspects gets the stricter treatment.
func (l *Lexer) Skip() {
	if l.err != nil {
		return
	}
	l.skipWS()
	pos, err := validValue(l.data, l.pos, l.depth)
	if err != nil {
		l.err = err
		return
	}
	l.pos = pos
}
