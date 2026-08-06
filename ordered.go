package zerojson

// This file exposes the small set of low-level Lexer accessors that
// generated ordered decoders need (see cmd/zerojsongen/ordered.go). The
// unsafe pointer arithmetic for word-compare key literal matching lives
// in the generated code itself, where the generator can embed
// compile-time literal constants per field; these methods just give that
// code controlled access to the Lexer's position and backing buffer.
// Hand-written code should not need them.

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

// OrderedBail rewinds the lexer to pos (the position where a generated
// ordered decoder began a speculative attempt) and clears any error
// recorded since, so the caller can retry with the fallback keyed decoder
// with zero observable trace of the abandoned attempt.
func (l *Lexer) OrderedBail(pos int) {
	l.pos = pos
	l.err = nil
}
