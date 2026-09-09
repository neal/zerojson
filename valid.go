package zerojson

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// SyntaxError reports the first RFC 8259 grammar violation Valid found,
// shaped like encoding/json's error of the same name (an Offset plus an
// Error() string). Valid's parity contract with encoding/json.Valid is
// about the valid/invalid verdict only — Offset and the message text are
// for humans debugging bad input, not part of that contract.
type SyntaxError struct {
	Offset int64
	msg    string
}

func (e *SyntaxError) Error() string { return e.msg }

func syntaxErrorf(offset int, format string, args ...any) error {
	return &SyntaxError{Offset: int64(offset), msg: fmt.Sprintf(format, args...)}
}

// Valid checks that data contains one JSON value and optional whitespace.
// Its acceptance policy matches encoding/json.Valid, including invalid UTF-8
// inside strings, unpaired Unicode escapes, and a maximum nesting depth of
// 10,000 containers. It does not validate a schema or business rules.
//
// Unlike ordinary Lexer value readers, Valid checks complete JSON grammar.
// Generated unmarshal methods and pooled decoders call it automatically.
// Call it explicitly when using low-level Lexer readers or checking JSON
// without decoding it.
//
// Valid returns nil without allocating on success, or a *SyntaxError on
// failure. Error messages and offsets need not match encoding/json.
func Valid(data []byte) error {
	pos, err := validValue(data, 0, 0)
	if err != nil {
		return err
	}
	pos = validSkipWS(data, pos)
	if pos != len(data) {
		return syntaxErrorf(pos, "invalid character %q after top-level value", data[pos])
	}
	return nil
}

func validSkipWS(data []byte, pos int) int {
	// Fast path: compact JSON (what zerojson's own encoder emits, and the
	// common case for real traffic) has no whitespace between tokens.
	if pos < len(data) && data[pos] > ' ' {
		return pos
	}
	for pos < len(data) {
		switch data[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// validValue validates and consumes one JSON value starting at or after
// pos (leading whitespace is skipped), returning the position just past
// it. depth is the current object/array nesting depth, threaded through
// so validObject/validArray can enforce maxDepth.
func validValue(data []byte, pos, depth int) (int, error) {
	pos = validSkipWS(data, pos)
	if pos >= len(data) {
		return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
	}
	switch data[pos] {
	case '{':
		return validObject(data, pos, depth)
	case '[':
		return validArray(data, pos, depth)
	case '"':
		return validString(data, pos)
	case 't':
		return validLiteral(data, pos, "true")
	case 'f':
		return validLiteral(data, pos, "false")
	case 'n':
		return validLiteral(data, pos, "null")
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return validNumber(data, pos)
	default:
		return pos, syntaxErrorf(pos, "invalid character %q looking for beginning of value", data[pos])
	}
}

func validObject(data []byte, pos, depth int) (int, error) {
	depth++
	if depth > maxDepth {
		return pos, syntaxErrorf(pos, "exceeded max depth")
	}
	pos++ // consume '{'
	pos = validSkipWS(data, pos)
	if pos < len(data) && data[pos] == '}' {
		return pos + 1, nil
	}
	for {
		pos = validSkipWS(data, pos)
		if pos >= len(data) {
			return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		if data[pos] != '"' {
			return pos, syntaxErrorf(pos, "invalid character %q looking for beginning of object key string", data[pos])
		}
		var err error
		pos, err = validString(data, pos)
		if err != nil {
			return pos, err
		}
		pos = validSkipWS(data, pos)
		if pos >= len(data) {
			return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		if data[pos] != ':' {
			return pos, syntaxErrorf(pos, "invalid character %q after object key", data[pos])
		}
		pos++
		pos, err = validValue(data, pos, depth)
		if err != nil {
			return pos, err
		}
		pos = validSkipWS(data, pos)
		if pos >= len(data) {
			return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		switch data[pos] {
		case ',':
			pos++
		case '}':
			return pos + 1, nil
		default:
			return pos, syntaxErrorf(pos, "invalid character %q after object key:value pair", data[pos])
		}
	}
}

func validArray(data []byte, pos, depth int) (int, error) {
	depth++
	if depth > maxDepth {
		return pos, syntaxErrorf(pos, "exceeded max depth")
	}
	pos++ // consume '['
	pos = validSkipWS(data, pos)
	if pos < len(data) && data[pos] == ']' {
		return pos + 1, nil
	}
	for {
		var err error
		pos, err = validValue(data, pos, depth)
		if err != nil {
			return pos, err
		}
		pos = validSkipWS(data, pos)
		if pos >= len(data) {
			return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		switch data[pos] {
		case ',':
			pos++
		case ']':
			return pos + 1, nil
		default:
			return pos, syntaxErrorf(pos, "invalid character %q after array element", data[pos])
		}
	}
}

func validLiteral(data []byte, pos int, lit string) (int, error) {
	n := len(lit)
	if pos+n > len(data) {
		return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
	}
	for i := range n {
		if data[pos+i] != lit[i] {
			return pos, syntaxErrorf(pos, "invalid character %q looking for beginning of value", data[pos])
		}
	}
	return pos + n, nil
}

// skipDigitRun advances pos past a run of ASCII digits, batching 8 at a
// time via the existing IsEightDigits digit-detection machinery (numbers.go)
// — reused here purely for its fast is-digit test, since Valid never needs
// the parsed numeric value, only the grammar. Returns the new position;
// pos is unchanged if data[pos] is not a digit.
func skipDigitRun(data []byte, pos int) int {
	for pos+8 <= len(data) {
		w := binary.LittleEndian.Uint64(data[pos:])
		if !IsEightDigits(w) {
			break
		}
		pos += 8
	}
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	return pos
}

// validNumber validates and consumes one JSON number per RFC 8259's
// strict grammar: [ "-" ] int [ frac ] [ exp ], int = "0" / 1-9 *DIGIT,
// frac = "." 1*DIGIT, exp = ("e"|"E") ["-"|"+"] 1*DIGIT. Unlike the
// trusted-path Lexer's bare-number readers (which still accept a leading
// zero — the one documented number leniency — see numbers.go), this
// rejects exactly what encoding/json.Valid rejects: no leading zeros
// (01), no bare/trailing sign (-, 1e+), no missing fraction/exponent
// digits (1., 1e). It does
// not evaluate the number's magnitude, so pathological exponents
// (1e999999999) are grammar-valid and validated in O(len(exponent)), same
// as stdlib.
func validNumber(data []byte, pos int) (int, error) {
	start := pos
	if pos < len(data) && data[pos] == '-' {
		pos++
	}
	if pos >= len(data) || data[pos] < '0' || data[pos] > '9' {
		return start, syntaxErrorf(start, "invalid number literal")
	}
	if data[pos] == '0' {
		pos++
	} else {
		pos = skipDigitRun(data, pos)
	}
	if pos < len(data) && data[pos] == '.' {
		fracStart := pos
		pos++
		next := skipDigitRun(data, pos)
		if next == pos {
			return fracStart, syntaxErrorf(fracStart, "invalid number literal (no digits after '.')")
		}
		pos = next
	}
	if pos < len(data) && (data[pos] == 'e' || data[pos] == 'E') {
		expStart := pos
		pos++
		if pos < len(data) && (data[pos] == '+' || data[pos] == '-') {
			pos++
		}
		next := skipDigitRun(data, pos)
		if next == pos {
			return expStart, syntaxErrorf(expStart, "invalid number literal (no digits in exponent)")
		}
		pos = next
	}
	return pos, nil
}

// hasControlByte reports (as a SWAR mask, same bit-7-per-lane convention
// as strings.go's hasByte) which bytes of v are JSON control characters
// (0x00-0x1F, which RFC 8259 requires a string to escape rather than
// contain literally). Masking to each byte's top 3 bits turns "byte <
// 0x20" into "masked byte == 0", which the standard SWAR haszero test
// answers exactly.
func hasControlByte(v uint64) uint64 {
	x := v & 0xE0E0E0E0E0E0E0E0
	return (x - 0x0101010101010101) &^ x & 0x8080808080808080
}

// scanStringSpecial returns the index of the next '"', '\\', or JSON
// control byte in data at or after pos, using the same two-lane (16
// bytes/step) then one-lane (8 bytes/step) then byte-loop SWAR scan as
// the trusted-path readStringBytes (strings.go) — the one piece of
// machinery Valid shares with the decoder, applied here to a different
// exact-match set (readStringBytes never needs to find control bytes,
// since the trusted-path decoder doesn't reject them). Returns len(data)
// if none is found before the end of input.
func scanStringSpecial(data []byte, pos int) int {
	i := pos
	for i+16 <= len(data) {
		v0 := binary.LittleEndian.Uint64(data[i:])
		if m := hasByte(v0, '"') | hasByte(v0, '\\') | hasControlByte(v0); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		v1 := binary.LittleEndian.Uint64(data[i+8:])
		if m := hasByte(v1, '"') | hasByte(v1, '\\') | hasControlByte(v1); m != 0 {
			return i + 8 + bits.TrailingZeros64(m)>>3
		}
		i += 16
	}
	for i+8 <= len(data) {
		v := binary.LittleEndian.Uint64(data[i:])
		if m := hasByte(v, '"') | hasByte(v, '\\') | hasControlByte(v); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for ; i < len(data); i++ {
		if data[i] == '"' || data[i] == '\\' || data[i] < 0x20 {
			return i
		}
	}
	return len(data)
}

// validString validates and consumes one JSON string starting at
// data[pos] == '"' (the caller has already checked this), returning the
// position just past the closing quote. Every escape sequence is
// validated by validEscape; raw control bytes (unescaped) are rejected;
// everything else — including invalid UTF-8, matching stdlib's
// byte-oriented v1 scanner — is accepted.
func validString(data []byte, pos int) (int, error) {
	pos++ // consume opening '"'
	for {
		j := scanStringSpecial(data, pos)
		if j >= len(data) {
			return len(data), syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		switch {
		case data[j] == '"':
			return j + 1, nil
		case data[j] == '\\':
			next, err := validEscape(data, j)
			if err != nil {
				return j, err
			}
			pos = next
		default: // control byte
			return j, syntaxErrorf(j, "invalid control character in string literal")
		}
	}
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// validEscape validates one backslash escape sequence starting at
// data[pos] == '\\' (the caller has already checked this) and returns the
// position just past it: a single-character escape (\" \\ \/ \b \f \n \r
// \t) is 2 bytes; \uXXXX is 6 bytes and requires exactly 4 hex digits. A
// lone or invalid surrogate is still grammar-valid — the escape's four
// hex digits are the whole requirement; stdlib never checks surrogate
// pairing at the grammar level either (see the package doc).
func validEscape(data []byte, pos int) (int, error) {
	if pos+1 >= len(data) {
		return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
	}
	switch data[pos+1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return pos + 2, nil
	case 'u':
		if pos+6 > len(data) {
			return pos, syntaxErrorf(len(data), "unexpected end of JSON input")
		}
		for k := pos + 2; k < pos+6; k++ {
			if !isHexDigit(data[k]) {
				return k, syntaxErrorf(k, "invalid character %q in \\u hexadecimal character escape", data[k])
			}
		}
		return pos + 6, nil
	default:
		return pos, syntaxErrorf(pos+1, "invalid character %q in string escape code", data[pos+1])
	}
}
