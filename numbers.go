package zerojson

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

// readNumericBytes returns the raw bytes of either a quoted or bare
// number token. This is the LENIENT form: it backs the exported
// ReadNumericBytes (custom leaf codecs whose wire format is a quoted
// number, e.g. a decimal) and ReadQuotedOrBareInt64 (the quoted-int64
// marker convention). The ordinary int/uint/float field readers use
// readBareNumericBytes instead, which rejects a string token the way
// encoding/json does for a plain numeric field.
func (l *Lexer) readNumericBytes() []byte {
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '"' {
		b, plain, _ := l.readStringBytes()
		if !plain {
			return unescapeStringBytes(b)
		}
		return b
	}
	return l.scanNumberRun()
}

// readBareNumericBytes returns the raw bytes of a bare number token,
// recording an error if the next value is a string (or anything else
// that cannot begin a number). A plain int/uint/float field decoding
// `"12"` — or `"NaN"`, which strconv would otherwise happily parse into a
// value AppendFloat64 then refuses to re-encode — is an error here,
// matching encoding/json and easyjson, which both reject a quoted number
// on a field without the `,string` option.
func (l *Lexer) readBareNumericBytes() []byte {
	l.skipWS()
	b := l.scanNumberRun()
	if len(b) == 0 {
		l.setErr("expected number")
	}
	return b
}

// scanNumberRun consumes the greedy [0-9+\-.eE] run at the current
// position. Grammar is checked by the caller's parser (parseDigitsBounded
// for integers, bareFloatOK+strconv.ParseFloat for floats), not here.
func (l *Lexer) scanNumberRun() []byte {
	start := l.pos
	for l.pos < len(l.data) {
		switch c := l.data[l.pos]; {
		case c >= '0' && c <= '9', c == '-', c == '+', c == '.', c == 'e', c == 'E':
			l.pos++
		default:
			return l.data[start:l.pos]
		}
	}
	return l.data[start:l.pos]
}

// bareFloatOK rejects the float spellings strconv.ParseFloat accepts but
// JSON's number grammar does not: a leading '+', a missing integer part
// (`.5`), and a '.' with no digit after it (`1.` / `1.e5`). Anything past
// the first '.' or exponent marker is left to ParseFloat, which already
// rejects a second '.', an empty exponent, and so on. A leading zero
// (`007`) is deliberately still accepted — the documented declared-field
// leniency (see the README's compatibility contract).
func bareFloatOK(b []byte) bool {
	i := 0
	if i < len(b) && b[i] == '-' {
		i++
	}
	if i >= len(b) || b[i] < '0' || b[i] > '9' {
		return false
	}
	for ; i < len(b); i++ {
		switch b[i] {
		case '.':
			return i+1 < len(b) && b[i+1] >= '0' && b[i+1] <= '9'
		case 'e', 'E':
			return true
		}
	}
	return true
}

// readQuotedNumericBytes is the strict counterpart used for json:",string"
// fields. Unlike readNumericBytes it requires the JSON token itself to be a
// string; encoding/json rejects a bare number when the string option is set.
func (l *Lexer) readQuotedNumericBytes() []byte {
	l.skipWS()
	if l.pos >= len(l.data) || l.data[l.pos] != '"' {
		l.setErr("expected quoted number")
		return nil
	}
	b, plain, _ := l.readStringBytes()
	if !plain {
		return unescapeStringBytes(b)
	}
	return b
}

// ReadNumericBytes returns the raw bytes of the next number token (quoted
// or bare), for custom leaf-type read functions to parse themselves. It
// is deliberately lenient about the token kind: a custom leaf owns its
// own wire format, and a quoted decimal (`"2.50"`) is a common one. The
// generated readers for plain int/uint/float fields are stricter (bare
// number only), matching encoding/json.
func (l *Lexer) ReadNumericBytes() []byte {
	return l.readNumericBytes()
}

// ReadQuotedOrBareInt64 parses an int64 from either a quoted string
// token or a bare number. It backs the ZeroJSONQuotedInt64 marker
// convention (a custom int64 type whose canonical wire form is the quoted
// base-10 string, but which should still accept a bare number from a
// producer that never quoted it), so generated code for such a type
// stays as lenient as the type's own UnmarshalJSON would typically be.
func (l *Lexer) ReadQuotedOrBareInt64() int64 {
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '"' {
		b := l.readQuotedNumericBytes()
		// Inside the quotes a leading '+' is tolerated — and only there —
		// because such a type's own UnmarshalJSON is conventionally
		// strconv.ParseInt on the unquoted digits, which accepts it; the
		// generated fast path must not reject a document the type's own
		// method would have taken. A BARE token is JSON grammar, where a
		// leading '+' is simply invalid, so it gets the strict reader.
		if len(b) > 1 && b[0] == '+' && b[1] >= '0' && b[1] <= '9' {
			b = b[1:]
		}
		return l.parseSigned(b, math.MaxInt64)
	}
	return l.parseSigned(l.readBareNumericBytes(), math.MaxInt64)
}

// IsEightDigits reports whether all 8 bytes of v (a little-endian load)
// are ASCII digits. Exported for custom leaf codecs.
func IsEightDigits(v uint64) bool {
	// High nibble of every byte must be 3, and adding 6 to the low
	// nibbles must not carry into the high nibbles (i.e. low nibble <= 9).
	return v&0xF0F0F0F0F0F0F0F0 == 0x3030303030303030 &&
		(v+0x0606060606060606)&0xF0F0F0F0F0F0F0F0 == 0x3030303030303030
}

// ParseEightDigits converts 8 ASCII digits (little-endian load) to their
// numeric value in three multiply-accumulate steps. Exported for custom
// leaf codecs.
func ParseEightDigits(v uint64) uint64 {
	v = (v & 0x0F0F0F0F0F0F0F0F) * 2561 >> 8
	v = (v & 0x00FF00FF00FF00FF) * 6553601 >> 16
	return (v & 0x0000FFFF0000FFFF) * 42949672960001 >> 32
}

// parseDigitsBounded parses b as an unsigned decimal, 8 digits per step,
// returning ok=false if b is empty, contains a non-digit, OR represents a
// value greater than limit. Unlike a plain uint64 accumulator it does not
// wrap: an overflow past limit is reported (ok=false), not silently
// truncated. This is what lets the width-aware readers below reject
// out-of-range input the way encoding/json does, instead of blind-casting
// a wrapped uint64 into a narrower integer type. The high/low split of
// limit is hoisted out of each loop so the per-step overflow test is two
// comparisons rather than a division.
func parseDigitsBounded(b []byte, limit uint64) (uint64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var v uint64
	i := 0
	if len(b) >= 8 {
		hiLim, loLim := limit/100000000, limit%100000000
		for len(b)-i >= 8 {
			w := binary.LittleEndian.Uint64(b[i:])
			if !IsEightDigits(w) {
				break
			}
			d := ParseEightDigits(w)
			// v*1e8 + d <= limit  iff  v < hiLim, or v == hiLim and d <= loLim.
			if v > hiLim || (v == hiLim && d > loLim) {
				return 0, false
			}
			v = v*100000000 + d
			i += 8
		}
	}
	if i < len(b) {
		hiLim, loLim := limit/10, limit%10
		for ; i < len(b); i++ {
			c := b[i] - '0'
			if c > 9 {
				return 0, false
			}
			d := uint64(c)
			if v > hiLim || (v == hiLim && d > loLim) {
				return 0, false
			}
			v = v*10 + d
		}
	}
	return v, true
}

// readSigned reads a bare signed integer whose magnitude must fit the
// target width: a non-negative value may reach maxPos (the width's
// MaxInt), a negative value maxPos+1 (the width's MinInt). Out-of-range or
// malformed input (including a leading '+', which JSON's number grammar
// does not allow) records a decode error and returns 0, matching
// encoding/json's range rejection rather than a silent wrap-and-truncate.
func (l *Lexer) readSigned(maxPos uint64) int64 {
	b := l.readBareNumericBytes()
	return l.parseSigned(b, maxPos)
}

func (l *Lexer) readQuotedSigned(maxPos uint64) int64 {
	b := l.readQuotedNumericBytes()
	return l.parseSigned(b, maxPos)
}

func (l *Lexer) parseSigned(b []byte, maxPos uint64) int64 {
	if l.err != nil {
		return 0
	}
	neg := false
	if len(b) > 0 && b[0] == '-' {
		neg = true
		b = b[1:]
	}
	limit := maxPos
	if neg {
		limit++
	}
	v, ok := parseDigitsBounded(b, limit)
	if !ok {
		l.setErr("integer %s out of range or malformed", errToken(b))
		return 0
	}
	if neg {
		if v == 1<<63 {
			// Only reachable for the int64 minimum (maxPos == MaxInt64);
			// -int64(1<<63) would overflow, so name MinInt64 directly.
			return math.MinInt64
		}
		return -int64(v)
	}
	return int64(v)
}

// readUnsigned reads a bare unsigned integer that must not exceed limit
// (the target width's MaxUint). A sign, non-digit, or out-of-range value
// records a decode error and returns 0.
func (l *Lexer) readUnsigned(limit uint64) uint64 {
	b := l.readBareNumericBytes()
	return l.parseUnsigned(b, limit)
}

func (l *Lexer) readQuotedUnsigned(limit uint64) uint64 {
	b := l.readQuotedNumericBytes()
	return l.parseUnsigned(b, limit)
}

func (l *Lexer) parseUnsigned(b []byte, limit uint64) uint64 {
	if l.err != nil {
		return 0
	}
	v, ok := parseDigitsBounded(b, limit)
	if !ok {
		l.setErr("unsigned integer %s out of range or malformed", errToken(b))
		return 0
	}
	return v
}

// ReadInt8/16/32/64 parse a bare signed integer, range-checked against
// the named width: a value outside [MinIntN, MaxIntN] is a decode error,
// not a silently wrapped cast (see readSigned). A quoted number is an
// error too (use the ReadQuoted* variants for `,string` fields).
func (l *Lexer) ReadInt8() int8   { return int8(l.readSigned(math.MaxInt8)) }
func (l *Lexer) ReadInt16() int16 { return int16(l.readSigned(math.MaxInt16)) }
func (l *Lexer) ReadInt32() int32 { return int32(l.readSigned(math.MaxInt32)) }
func (l *Lexer) ReadInt64() int64 { return l.readSigned(math.MaxInt64) }

func (l *Lexer) ReadQuotedInt8() int8   { return int8(l.readQuotedSigned(math.MaxInt8)) }
func (l *Lexer) ReadQuotedInt16() int16 { return int16(l.readQuotedSigned(math.MaxInt16)) }
func (l *Lexer) ReadQuotedInt32() int32 { return int32(l.readQuotedSigned(math.MaxInt32)) }
func (l *Lexer) ReadQuotedInt64() int64 { return l.readQuotedSigned(math.MaxInt64) }

// ReadUint8/16/32/64 parse a bare unsigned integer, range-checked against
// the named width (see readUnsigned).
func (l *Lexer) ReadUint8() uint8   { return uint8(l.readUnsigned(math.MaxUint8)) }
func (l *Lexer) ReadUint16() uint16 { return uint16(l.readUnsigned(math.MaxUint16)) }
func (l *Lexer) ReadUint32() uint32 { return uint32(l.readUnsigned(math.MaxUint32)) }
func (l *Lexer) ReadUint64() uint64 { return l.readUnsigned(math.MaxUint64) }

func (l *Lexer) ReadQuotedUint8() uint8   { return uint8(l.readQuotedUnsigned(math.MaxUint8)) }
func (l *Lexer) ReadQuotedUint16() uint16 { return uint16(l.readQuotedUnsigned(math.MaxUint16)) }
func (l *Lexer) ReadQuotedUint32() uint32 { return uint32(l.readQuotedUnsigned(math.MaxUint32)) }
func (l *Lexer) ReadQuotedUint64() uint64 { return l.readQuotedUnsigned(math.MaxUint64) }

// ReadInt/ReadUint parse a bare integer range-checked against the build
// platform's `int`/`uint` width, so generated code for an `int` or
// `uint` field never blind-casts a 64-bit read into a narrower word. The
// bounds are the untyped constants math.MaxInt/math.MinInt/math.MaxUint:
// on a 64-bit target they equal the int64/uint64 limits, so these are the
// exact same reads as ReadInt64/ReadUint64 (the width check the callee
// already performs is the whole check — zero added cost); on a 32-bit
// target they tighten to the 32-bit range, turning what would otherwise be
// a silent wrap into a decode error, matching encoding/json.
func (l *Lexer) ReadInt() int   { return int(l.readSigned(math.MaxInt)) }
func (l *Lexer) ReadUint() uint { return uint(l.readUnsigned(math.MaxUint)) }

func (l *Lexer) ReadQuotedInt() int   { return int(l.readQuotedSigned(math.MaxInt)) }
func (l *Lexer) ReadQuotedUint() uint { return uint(l.readQuotedUnsigned(math.MaxUint)) }

// ReadFloat32 is ReadFloat64 for a float32 target: the same grammar, but
// parsed at 32-bit precision so a value outside float32's range (1e39)
// is a decode error — as encoding/json reports it — rather than a silent
// ±Inf that AppendFloat32 would then refuse to re-encode.
func (l *Lexer) ReadFloat32() float32 {
	b := l.readBareNumericBytes()
	if l.err != nil {
		return 0
	}
	if !bareFloatOK(b) {
		l.setErr("float %s: malformed number", errToken(b))
		return 0
	}
	f, err := strconv.ParseFloat(bytesToString(b), 32)
	if err != nil {
		l.setErr("float %s: %v", errToken(b), numErr(err))
		return 0
	}
	return float32(f)
}

// ReadFloat64 parses a bare floating point number. A quoted token is an
// error (so `"NaN"` and `"Inf"` can never sneak in as values that would
// then fail to re-encode), as are the non-JSON spellings strconv accepts
// (`+1`, `.5`, `1.`); see bareFloatOK.
func (l *Lexer) ReadFloat64() float64 {
	b := l.readBareNumericBytes()
	if l.err != nil {
		return 0
	}
	if !bareFloatOK(b) {
		l.setErr("float %s: malformed number", errToken(b))
		return 0
	}
	f, err := strconv.ParseFloat(bytesToString(b), 64)
	if err != nil {
		l.setErr("float %s: %v", errToken(b), numErr(err))
	}
	return f
}

// ReadJSONNumber reads a value for an encoding/json.Number field: a bare
// number token, or (as classic encoding/json also accepts for Number) a
// string token whose contents are a valid JSON number. The literal is
// returned verbatim (copied), never parsed, so precision is preserved
// exactly as with encoding/json. A string that is not a number literal,
// or a bare token that fails the RFC 8259 grammar, is a decode error —
// this reader is grammar-strict even for the bare form (no leading zero),
// because the literal is stored, not parsed, and would otherwise be
// re-encoded as invalid JSON.
func (l *Lexer) ReadJSONNumber() string {
	l.skipWS()
	var b []byte
	if l.pos < len(l.data) && l.data[l.pos] == '"' {
		b = l.readQuotedNumericBytes()
	} else {
		b = l.readBareNumericBytes()
	}
	if l.err != nil {
		return ""
	}
	if end, err := validNumber(b, 0); err != nil || end != len(b) {
		l.setErr("json.Number %s: invalid number literal", errToken(b))
		return ""
	}
	return string(b)
}

// AppendJSONNumber appends an encoding/json.Number literal verbatim, as
// encoding/json does: an empty Number encodes as 0, and anything that is
// not a valid JSON number literal is an error rather than invalid output.
func AppendJSONNumber(dst []byte, n string) ([]byte, error) {
	if n == "" {
		return append(dst, '0'), nil
	}
	if end, err := validNumber([]byte(n), 0); err != nil || end != len(n) {
		return dst, fmt.Errorf("json: invalid number literal %s", errToken([]byte(n)))
	}
	return append(dst, n...), nil
}

// IsQuotedNull consumes the string token whose VALUE is null — the exact
// spelling "null" or any escaped equivalent ("n\u0075ll") — if it is
// next, and reports whether it did. A `,string` field accepts it as a
// no-op (encoding/json treats the quoted spelling of null exactly like
// the bare literal there: the field is left untouched, a pointer is
// cleared). The common case costs one byte compare: only a token that
// begins with `"n` or `"\` can be a spelling of null, so anything else
// returns immediately without scanning.
func (l *Lexer) IsQuotedNull() bool {
	l.skipWS()
	if l.pos+1 >= len(l.data) || l.data[l.pos] != '"' {
		return false
	}
	if c := l.data[l.pos+1]; c != 'n' && c != '\\' {
		return false
	}
	start := l.pos
	b, plain, _ := l.readStringBytes()
	if l.err != nil {
		return false
	}
	if !plain {
		b = unescapeStringBytes(b)
	}
	if string(b) == "null" {
		return true
	}
	l.pos = start
	return false
}

// errTokenMax bounds how much of an offending token a decode error quotes.
// The whole token would make the error as large as the input (twice as
// large, with strconv's own copy): a service logging decode errors would
// then amplify a hostile 10 MB number into 20 MB of log per request.
const errTokenMax = 32

// errToken renders a token for an error message: quoted whole when short,
// otherwise its first errTokenMax bytes, an ellipsis, and the length.
func errToken(b []byte) string {
	if len(b) <= errTokenMax {
		return strconv.Quote(string(b))
	}
	return strconv.Quote(string(b[:errTokenMax])) + fmt.Sprintf("... (%d bytes)", len(b))
}

// numErr strips strconv's copy of the token from a NumError, leaving the
// reason ("value out of range", "invalid syntax") — the token is already
// in the message, bounded, via errToken.
func numErr(err error) error {
	if ne, ok := err.(*strconv.NumError); ok {
		return ne.Err
	}
	return err
}
