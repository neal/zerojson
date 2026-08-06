package zerojson

import (
	"encoding/binary"
	"fmt"
)

// Lexer is a minimal pull-parser over a complete JSON document held in
// memory. Known/declared field values are read by lenient, fast leaf
// readers (deliberately not full RFC 8259 validation: a bare number may
// have a leading zero, a string may contain a raw control byte) — the
// trusted-path design this package is built around. Content the Lexer
// doesn't otherwise examine is not exempt from grammar checking, though:
// Skip (unknown fields, Raw passthrough spans) and Finish (trailing data
// after the top-level value) ARE fully grammar-strict, reusing Valid's
// own grammar walk (valid.go) — see the README's "Validation" section
// for the resulting decode-implies-valid composition story, and
// top-level zerojson.Valid for a standalone full-payload validator.
type Lexer struct {
	data  []byte
	pos   int
	err   error
	depth int

	// Borrow makes string reads return views into the input buffer
	// instead of copies. Callers must guarantee the input outlives the
	// decoded struct.
	Borrow bool
}

// maxDepth bounds nesting in ReadInterface to prevent stack exhaustion
// from adversarial deeply-nested input. Matches encoding/json's limit.
const maxDepth = 10000

func NewLexer(data []byte, borrow bool) Lexer {
	return Lexer{data: data, Borrow: borrow}
}

func (l *Lexer) Err() error { return l.err }

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

// Delim consumes an expected delimiter.
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

// EnterObject consumes '{' and reports whether the object has members; an
// empty object's '}' is consumed too. Malformed input records an error and
// returns false.
func (l *Lexer) EnterObject() bool {
	l.Delim('{')
	if l.err != nil {
		return false
	}
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '}' {
		l.pos++
		return false
	}
	return true
}

// NextMember consumes the ',' between object members (returning true) or
// the closing '}' (returning false). Unlike the WantComma/IsDelim pair it
// replaces, it costs a single whitespace skip per member.
func (l *Lexer) NextMember() bool {
	l.skipWS()
	if l.pos < len(l.data) {
		switch l.data[l.pos] {
		case ',':
			l.pos++
			return true
		case '}':
			l.pos++
			return false
		}
	}
	l.setErr("expected ',' or '}'")
	return false
}

// EnterArray consumes '[' and reports whether the array has elements; an
// empty array's ']' is consumed too.
func (l *Lexer) EnterArray() bool {
	l.Delim('[')
	if l.err != nil {
		return false
	}
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == ']' {
		l.pos++
		return false
	}
	return true
}

// NextElem consumes the ',' between array elements (returning true) or the
// closing ']' (returning false).
func (l *Lexer) NextElem() bool {
	l.skipWS()
	if l.pos < len(l.data) {
		switch l.data[l.pos] {
		case ',':
			l.pos++
			return true
		case ']':
			l.pos++
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
// maxDepth cap Valid enforces) rather than merely brace/bracket-counted.
//
// This is what makes a successful decode imply the whole payload was
// grammar-valid JSON (see README's composition story): every byte is
// either consumed by a known field's own decode statement or
// grammar-checked here — the two together, plus Finish's trailing-data
// check, cover the entire document, at every level of nesting a decode
// walks (a genuinely unknown field's value, and the ordered decoder's
// inline skip of one, both go through this same method). The one thing
// this deliberately does not touch is the known-field decode primitives
// (readStringBytes, readNumericBytes) — those stay lenient and fast; only
// content the caller never otherwise inspects gets the stricter treatment.
func (l *Lexer) Skip() {
	if l.err != nil {
		return
	}
	l.skipWS()
	pos, err := validValue(l.data, l.pos, 0)
	if err != nil {
		l.err = err
		return
	}
	l.pos = pos
}
