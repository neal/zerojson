package zerojson

import (
	"encoding/binary"
	"math"
	"strconv"
)

// readNumericBytes returns the raw bytes of either a quoted or bare
// number token.
func (l *Lexer) readNumericBytes() []byte {
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '"' {
		b, plain, _ := l.readStringBytes()
		if !plain {
			return unescapeStringBytes(b)
		}
		return b
	}
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
// or bare), for custom leaf-type read functions to parse themselves.
func (l *Lexer) ReadNumericBytes() []byte {
	return l.readNumericBytes()
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

// readSigned reads a quoted-or-bare signed integer whose magnitude must fit
// the target width: a non-negative value may reach maxPos (the width's
// MaxInt), a negative value maxPos+1 (the width's MinInt). Out-of-range or
// malformed input records a decode error and returns 0, matching
// encoding/json's range rejection rather than a silent wrap-and-truncate.
func (l *Lexer) readSigned(maxPos uint64) int64 {
	b := l.readNumericBytes()
	return l.parseSigned(b, maxPos, true)
}

func (l *Lexer) readQuotedSigned(maxPos uint64) int64 {
	b := l.readQuotedNumericBytes()
	return l.parseSigned(b, maxPos, false)
}

func (l *Lexer) parseSigned(b []byte, maxPos uint64, allowPlus bool) int64 {
	if l.err != nil {
		return 0
	}
	neg := false
	if len(b) > 0 {
		switch b[0] {
		case '-':
			neg = true
			b = b[1:]
		case '+':
			if allowPlus {
				b = b[1:]
			}
		}
	}
	limit := maxPos
	if neg {
		limit++
	}
	v, ok := parseDigitsBounded(b, limit)
	if !ok {
		l.setErr("integer %q out of range or malformed", b)
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

// readUnsigned reads a quoted-or-bare unsigned integer that must not exceed
// limit (the target width's MaxUint). A negative sign, non-digit, or
// out-of-range value records a decode error and returns 0.
func (l *Lexer) readUnsigned(limit uint64) uint64 {
	b := l.readNumericBytes()
	return l.parseUnsigned(b, limit, true)
}

func (l *Lexer) readQuotedUnsigned(limit uint64) uint64 {
	b := l.readQuotedNumericBytes()
	return l.parseUnsigned(b, limit, false)
}

func (l *Lexer) parseUnsigned(b []byte, limit uint64, allowPlus bool) uint64 {
	if l.err != nil {
		return 0
	}
	if allowPlus && len(b) > 0 && b[0] == '+' {
		b = b[1:]
	}
	v, ok := parseDigitsBounded(b, limit)
	if !ok {
		l.setErr("unsigned integer %q out of range or malformed", b)
		return 0
	}
	return v
}

// ReadInt8/16/32/64 parse a quoted-or-bare signed integer, range-checked
// against the named width: a value outside [MinIntN, MaxIntN] is a decode
// error, not a silently wrapped cast (see readSigned).
func (l *Lexer) ReadInt8() int8   { return int8(l.readSigned(math.MaxInt8)) }
func (l *Lexer) ReadInt16() int16 { return int16(l.readSigned(math.MaxInt16)) }
func (l *Lexer) ReadInt32() int32 { return int32(l.readSigned(math.MaxInt32)) }
func (l *Lexer) ReadInt64() int64 { return l.readSigned(math.MaxInt64) }

func (l *Lexer) ReadQuotedInt8() int8   { return int8(l.readQuotedSigned(math.MaxInt8)) }
func (l *Lexer) ReadQuotedInt16() int16 { return int16(l.readQuotedSigned(math.MaxInt16)) }
func (l *Lexer) ReadQuotedInt32() int32 { return int32(l.readQuotedSigned(math.MaxInt32)) }
func (l *Lexer) ReadQuotedInt64() int64 { return l.readQuotedSigned(math.MaxInt64) }

// ReadUint8/16/32/64 parse a quoted-or-bare unsigned integer, range-checked
// against the named width (see readUnsigned).
func (l *Lexer) ReadUint8() uint8   { return uint8(l.readUnsigned(math.MaxUint8)) }
func (l *Lexer) ReadUint16() uint16 { return uint16(l.readUnsigned(math.MaxUint16)) }
func (l *Lexer) ReadUint32() uint32 { return uint32(l.readUnsigned(math.MaxUint32)) }
func (l *Lexer) ReadUint64() uint64 { return l.readUnsigned(math.MaxUint64) }

func (l *Lexer) ReadQuotedUint8() uint8   { return uint8(l.readQuotedUnsigned(math.MaxUint8)) }
func (l *Lexer) ReadQuotedUint16() uint16 { return uint16(l.readQuotedUnsigned(math.MaxUint16)) }
func (l *Lexer) ReadQuotedUint32() uint32 { return uint32(l.readQuotedUnsigned(math.MaxUint32)) }
func (l *Lexer) ReadQuotedUint64() uint64 { return l.readQuotedUnsigned(math.MaxUint64) }

// ReadInt/ReadUint parse a quoted-or-bare integer range-checked against the
// build platform's `int`/`uint` width, so generated code for an `int` or
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

// ReadFloat64 parses a quoted-or-bare floating point number.
func (l *Lexer) ReadFloat64() float64 {
	b := l.readNumericBytes()
	if l.err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(bytesToString(b), 64)
	if err != nil {
		l.setErr("float %q: %v", b, err)
	}
	return f
}
