package zerojson

import "unicode/utf8"

// These accessors support generated ordered decoders. Key matching uses
// encoding/binary over bounds-checked slices in the generated code; no unsafe
// pointer arithmetic is required. Hand-written codecs normally do not need
// these accessors.
// Data returns the Lexer's input buffer.
func (l *Lexer) Data() []byte { return l.data }

// Pos returns the current byte offset into Data().
func (l *Lexer) Pos() int { return l.pos }

// SetPos sets the current byte offset. Generated ordered decoders use it
// to advance past a matched literal directly — compact JSON (what
// zerojson's own encoder emits) never has whitespace to skip there.
func (l *Lexer) SetPos(pos int) { l.pos = pos }

// DataAndPos returns Data() and Pos() together, since generated ordered
// decoders read both on nearly every candidate key check.
func (l *Lexer) DataAndPos() ([]byte, int) { return l.data, l.pos }

// PlainKey returns the object key at the current position, and the
// position just past its colon, when the key is plain: ASCII other than
// control bytes, '"' and '\\' between the quotes, and ':' right after the
// closing quote. For a plain key ReadKey returns the same bytes and stops
// at next. Anything else (whitespace, an escape, a control or non-ASCII
// byte, or no key at all) reports ok false. PlainKey does not move the
// position; key aliases Data().
func (l *Lexer) PlainKey() (key []byte, next int, ok bool) {
	data, pos := l.data, l.pos
	if pos >= len(data) || data[pos] != '"' {
		return nil, 0, false
	}
	end, high := scanKeyStringSpecial(data, pos+1)
	if end+1 >= len(data) || data[end] != '"' || data[end+1] != ':' {
		return nil, 0, false
	}
	key = data[pos+1 : end]
	// high ORs whole words, which can reach past the closing quote.
	if high&0x8080808080808080 != 0 {
		for _, c := range key {
			if c >= utf8.RuneSelf {
				return nil, 0, false
			}
		}
	}
	return key, end + 2, true
}

// Mark is a snapshot of the Lexer's position and nesting depth, taken by
// a generated dispatcher before a speculative ordered attempt so that
// OrderedBail can restore both. Depth matters because the ordered decoder
// consumes braces itself (via Enter/Leave) and a bail can happen at any
// nesting level below the mark.
type Mark struct {
	pos, depth int
}

// Mark returns the current position and depth for a later OrderedBail.
func (l *Lexer) Mark() Mark { return Mark{pos: l.pos, depth: l.depth} }

// OrderedBail restores the lexer position and depth to m and clears error
// annotations. It does not undo writes to the target or user side effects.
// Generated dispatchers use it for structural fallback after ruling out
// value errors and unsafe-to-replay targets.
func (l *Lexer) OrderedBail(m Mark) {
	l.pos = m.pos
	l.depth = m.depth
	l.err = nil
	l.path = l.path[:0]
	l.wrapped = nil
}

// Enter records entry into a nested object or array whose delimiters the
// caller consumes itself (the generated ordered decoder does this for the
// object it is matching), enforcing the same maxDepth every other path
// enforces. It records an error and returns false at the limit.
// EnterObject/EnterArray call this on the caller's behalf; hand-written
// code using those never needs Enter/Leave.
func (l *Lexer) Enter() bool {
	if l.depth++; l.depth > maxDepth {
		l.setErr("exceeded max depth")
		return false
	}
	return true
}

// Leave is Enter's counterpart, called after the closing delimiter.
func (l *Lexer) Leave() { l.depth-- }

// DepthOK reports whether one more nesting level would be within
// maxDepth, recording an error if not. It is the check for an EMPTY
// object or array, which counts as a level (Valid and encoding/json
// both count it) but is consumed without an Enter/Leave pair.
func (l *Lexer) DepthOK() bool {
	if l.depth >= maxDepth {
		l.setErr("exceeded max depth")
		return false
	}
	return true
}
