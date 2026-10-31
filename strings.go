package zerojson

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// hasByte reports (as a SWAR mask) which bytes of v equal b: bit 7 of
// each matching byte is set. Borrow propagation can set false positives
// above the first true match, so only the lowest set bit is meaningful.
func hasByte(v uint64, b byte) uint64 {
	x := v ^ (uint64(b) * 0x0101010101010101)
	return (x - 0x0101010101010101) &^ x & 0x8080808080808080
}

// readStringBytes returns the raw content between quotes. The second
// return is false when the string contains escapes, in which case the
// caller must unescape via unescapeString. The scan for the closing
// quote runs two 8-byte SWAR lanes (16 bytes) per step where enough
// input remains, falling back to one lane and then a byte loop for the
// tail — this is the hottest loop in the decoder.
func (l *Lexer) readStringBytes() ([]byte, bool, bool) {
	l.skipWS()
	return l.readStringBytesAfterWS()
}

// readStringBytesAfterWS is readStringBytes with the whitespace check already
// performed. Composite readers use it after dispatching on the first token so
// their common string path does not pay for a second skip.
func (l *Lexer) readStringBytesAfterWS() ([]byte, bool, bool) {
	if l.pos >= len(l.data) || l.data[l.pos] != '"' {
		l.setErr("expected string")
		return nil, true, false
	}
	l.pos++
	start := l.pos
	// Local slice lets the compiler hoist the bounds checks out of the
	// hottest loop in the decoder.
	data := l.data
	i := start
	const highBits = uint64(0x8080808080808080)
	var high uint64
	for i+16 <= len(data) {
		v0 := binary.LittleEndian.Uint64(data[i:])
		high |= v0
		if m := hasByte(v0, '"') | hasByte(v0, '\\'); m != 0 {
			j := i + bits.TrailingZeros64(m)>>3
			if data[j] == '"' {
				l.pos = j + 1
				return data[start:j], true, high&highBits != 0
			}
			return l.readStringSlow(start), false, false
		}
		v1 := binary.LittleEndian.Uint64(data[i+8:])
		high |= v1
		if m := hasByte(v1, '"') | hasByte(v1, '\\'); m != 0 {
			j := i + 8 + bits.TrailingZeros64(m)>>3
			if data[j] == '"' {
				l.pos = j + 1
				return data[start:j], true, high&highBits != 0
			}
			return l.readStringSlow(start), false, false
		}
		i += 16
	}
	for i+8 <= len(data) {
		v := binary.LittleEndian.Uint64(data[i:])
		high |= v
		if m := hasByte(v, '"') | hasByte(v, '\\'); m != 0 {
			i += bits.TrailingZeros64(m) >> 3
			if data[i] == '"' {
				l.pos = i + 1
				return data[start:i], true, high&highBits != 0
			}
			return l.readStringSlow(start), false, false
		}
		i += 8
	}
	for ; i < len(data); i++ {
		high |= uint64(data[i])
		switch data[i] {
		case '"':
			l.pos = i + 1
			return data[start:i], true, high&highBits != 0
		case '\\':
			// Slow path: find the real end honoring escapes.
			return l.readStringSlow(start), false, false
		}
	}
	l.setErr("unterminated string")
	return nil, true, false
}

func (l *Lexer) readStringSlow(start int) []byte {
	for i := start; i < len(l.data); i++ {
		switch l.data[i] {
		case '\\':
			i++ // skip escaped char (\uXXXX handled by unescape)
		case '"':
			l.pos = i + 1
			return l.data[start:i]
		}
	}
	l.setErr("unterminated string")
	return nil
}

func unescapeStringBytes(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		i++
		if i >= len(b) {
			break
		}
		switch b[i] {
		case '"', '\\', '/':
			out = append(out, b[i])
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'u':
			if i+4 < len(b) {
				r := parseHex4(b[i+1 : i+5])
				i += 4
				// UTF-16 surrogate pair: a high surrogate followed by an
				// escaped low surrogate combines into one code point.
				if r >= 0xD800 && r <= 0xDBFF && i+6 < len(b) && b[i+1] == '\\' && b[i+2] == 'u' {
					if r2 := parseHex4(b[i+3 : i+7]); r2 >= 0xDC00 && r2 <= 0xDFFF {
						r = 0x10000 + (r-0xD800)<<10 + (r2 - 0xDC00)
						i += 6
					}
				}
				// Lone surrogates are invalid: replace like encoding/json.
				if r >= 0xD800 && r <= 0xDFFF {
					r = 0xFFFD
				}
				out = utf8AppendRune(out, r)
			}
		}
	}
	return normalizeUTF8Bytes(out)
}

func unescapeString(b []byte) string {
	return bytesToString(unescapeStringBytes(b))
}

// normalizeUTF8Bytes returns b unchanged when it is valid UTF-8 and otherwise
// replaces each invalid byte with U+FFFD, matching encoding/json's unquote
// semantics. It only runs for strings whose hot scan observed a high-bit byte;
// clean ASCII strings pay no second validation pass.
func normalizeUTF8Bytes(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	out := make([]byte, 0, len(b)+utf8.UTFMax)
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			out = utf8.AppendRune(out, utf8.RuneError)
			b = b[1:]
			continue
		}
		out = append(out, b[:size]...)
		b = b[size:]
	}
	return out
}

func parseHex4(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		}
	}
	return r
}

func utf8AppendRune(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, byte(0xC0|r>>6), byte(0x80|r&0x3F))
	case r < 0x10000:
		return append(dst, byte(0xE0|r>>12), byte(0x80|r>>6&0x3F), byte(0x80|r&0x3F))
	default:
		return append(dst, byte(0xF0|r>>18), byte(0x80|r>>12&0x3F), byte(0x80|r>>6&0x3F), byte(0x80|r&0x3F))
	}
}

// bytesToString and stringToBytes live in unsafe_on.go / unsafe_off.go so
// the `nounsafe` build tag can select a copying implementation.

// scanKeyStringSpecial is ReadKey's strict counterpart to valid.go's string
// scanner. It also accumulates high bits so decoded keys can normalize invalid
// UTF-8 without a second pass over every ordinary ASCII key. Keeping this loop
// separate leaves zerojson.Valid's standalone hot path unchanged.
func scanKeyStringSpecial(data []byte, pos int) (int, uint64) {
	i := pos
	var high uint64
	for i+16 <= len(data) {
		v0 := binary.LittleEndian.Uint64(data[i:])
		high |= v0
		if m := hasByte(v0, '"') | hasByte(v0, '\\') | hasControlByte(v0); m != 0 {
			return i + bits.TrailingZeros64(m)>>3, high
		}
		v1 := binary.LittleEndian.Uint64(data[i+8:])
		high |= v1
		if m := hasByte(v1, '"') | hasByte(v1, '\\') | hasControlByte(v1); m != 0 {
			return i + 8 + bits.TrailingZeros64(m)>>3, high
		}
		i += 16
	}
	for i+8 <= len(data) {
		v := binary.LittleEndian.Uint64(data[i:])
		high |= v
		if m := hasByte(v, '"') | hasByte(v, '\\') | hasControlByte(v); m != 0 {
			return i + bits.TrailingZeros64(m)>>3, high
		}
		i += 8
	}
	for ; i < len(data); i++ {
		high |= uint64(data[i])
		if data[i] == '"' || data[i] == '\\' || data[i] < 0x20 {
			return i, high
		}
	}
	return len(data), high
}

func validKeyString(data []byte, pos int) (int, bool, error) {
	pos++
	var high uint64
	for {
		j, h := scanKeyStringSpecial(data, pos)
		high |= h
		if j >= len(data) {
			return len(data), high&0x8080808080808080 != 0, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		switch data[j] {
		case '"':
			return j + 1, high&0x8080808080808080 != 0, nil
		case '\\':
			next, err := validEscape(data, j)
			if err != nil {
				return j, high&0x8080808080808080 != 0, err
			}
			pos = next
		default:
			return j, high&0x8080808080808080 != 0, syntaxErrorf(j, "invalid control character in string literal")
		}
	}
}

// ReadKey returns the next object key, usually as a view into the input.
// Escaped keys (rare: only keys containing quotes, control chars, or the
// HTML set '<' '>' '&') take an allocating unescape path.
//
// This low-level reader is grammar-strict: a raw control byte or an invalid
// escape sequence in a key is
// rejected, matching json.Valid / encoding/json. Keys are validated on
// every path: the general keyed fallback and map decode read them here, and
// the ordered decoder reads a key itself only when it is plain — an exact
// declared-name literal, or a key PlainKey accepts, which this reader would
// return unchanged — so an escaped or control-bearing key always falls
// through to this strict reader. Generated public decodes also validate the
// complete document up front.
func (l *Lexer) ReadKey() []byte {
	l.skipWS()
	var b []byte
	if l.pos < len(l.data) && l.data[l.pos] == '"' {
		// One strict pass validates the key AND yields the closing-quote
		// position, so there's no second readStringBytes scan: the content
		// span is taken directly, and the (rare) escape path is entered only
		// when a backslash is actually present.
		end, nonASCII, err := validKeyString(l.data, l.pos)
		if err != nil {
			l.err = err
			return nil
		}
		content := l.data[l.pos+1 : end-1]
		l.pos = end
		if bytes.IndexByte(content, '\\') >= 0 {
			b = unescapeStringBytes(content)
		} else if nonASCII {
			b = normalizeUTF8Bytes(content)
		} else {
			b = content
		}
	} else {
		// Not a string: let readStringBytes emit the standard error.
		var plain bool
		b, plain, _ = l.readStringBytes()
		if !plain {
			b = []byte(unescapeString(b))
		}
	}
	// Compact JSON: ':' immediately follows the closing quote.
	if l.pos < len(l.data) && l.data[l.pos] == ':' {
		l.pos++
		return b
	}
	l.Delim(':')
	return b
}

// ReadString returns the string value, copying unless Borrow is set.
func (l *Lexer) ReadString() string {
	b, plain, nonASCII := l.readStringBytes()
	if !plain {
		return unescapeString(b)
	}
	if nonASCII {
		b = normalizeUTF8Bytes(b)
	}
	if l.Borrow {
		return bytesToString(b)
	}
	return string(b)
}

// ReadInternedString returns the string value from the bounded process-wide
// intern table. Retained short values (identifiers, currency codes, and other
// low-cardinality strings) reuse one allocation; a shard that reaches its
// cap starts a new generation (see Intern), so values keep being retained
// after a high-cardinality burst rather than being copied forever.
func (l *Lexer) ReadInternedString() string {
	b, plain, nonASCII := l.readStringBytes()
	if !plain {
		// An escaped spelling still names the same (low-cardinality) value
		// the field was tagged for, so it goes through the same bounded
		// table; the unescape's fresh buffer is what gets interned.
		return Intern(unescapeStringBytes(b))
	}
	if nonASCII {
		b = normalizeUTF8Bytes(b)
	}
	return Intern(b)
}

// ReadEnumBytes returns the raw bytes of a string value for switch
// dispatch against known enum constants. The returned slice usually
// aliases the input buffer and must not be retained; assign a constant
// or copy. Escaped values (possible for unknown enum values containing
// the HTML set, which the encoder escapes) take an allocating unescape
// path — declared enum constants never need it.
func (l *Lexer) ReadEnumBytes() []byte {
	b, plain, nonASCII := l.readStringBytes()
	if !plain {
		return unescapeStringBytes(b)
	}
	if nonASCII {
		return normalizeUTF8Bytes(b)
	}
	return b
}

// ReadTextBytes returns the content of a string value for delegation to
// UnmarshalText. The slice may alias the input buffer; UnmarshalText
// implementations must copy what they keep (they do, per its contract).
func (l *Lexer) ReadTextBytes() []byte {
	l.skipWS()
	if l.err != nil {
		return nil
	}
	if l.pos >= len(l.data) || l.data[l.pos] != '"' {
		l.setErr("expected string")
		return nil
	}
	// Reuse the strict single-pass string scanner. Unlike ordinary known
	// string fields, delegated text must not call user code on bad grammar.
	end, nonASCII, err := validKeyString(l.data, l.pos)
	if err != nil {
		l.err = err
		return nil
	}
	b := l.data[l.pos+1 : end-1]
	l.pos = end
	if bytes.IndexByte(b, '\\') >= 0 {
		return unescapeStringBytes(b)
	}
	if nonASCII {
		return normalizeUTF8Bytes(b)
	}
	return b
}
