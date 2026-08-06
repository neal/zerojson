package zerojson

import "encoding/base64"

// AppendBytesBase64 appends a byte slice as a JSON value, matching
// encoding/json and easyjson: a nil slice appends the literal null, and any
// non-nil slice (including an empty one, which appends "") appends its
// standard-base64 encoding as a quoted string. This is the encoding both
// oracles use for a []byte (or named byte-slice) field/element/value; the
// element type is uint8 and does not carry its own Marshaler.
func AppendBytesBase64(dst, src []byte) []byte {
	if src == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '"')
	dst = base64.StdEncoding.AppendEncode(dst, src)
	return append(dst, '"')
}

// ReadBytesBase64 reads either the usual base64 JSON string or
// encoding/json's additional array-of-byte-values input form into a fresh
// byte slice. The string path is the common fast path. null is handled by the
// caller's IsNull check before this is reached; null elements inside a numeric
// array decode as zero, matching encoding/json's scalar null behavior.
func (l *Lexer) ReadBytesBase64() []byte {
	l.skipWS()
	if l.pos < len(l.data) && l.data[l.pos] == '[' {
		out := make([]byte, 0)
		if !l.EnterArray() {
			return out
		}
		for {
			if l.IsNull() {
				out = append(out, 0)
			} else {
				l.skipWS()
				start := l.pos
				for l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '9' {
					l.pos++
				}
				v, ok := parseDigitsBounded(l.data[start:l.pos], 255)
				if !ok {
					l.setErr("byte array element out of range or malformed")
					return nil
				}
				out = append(out, byte(v))
			}
			if !l.NextElem() {
				return out
			}
		}
	}
	b, plain, _ := l.readStringBytesAfterWS()
	if l.err != nil {
		return nil
	}
	if !plain {
		b = unescapeStringBytes(b)
	}
	out := make([]byte, base64.StdEncoding.DecodedLen(len(b)))
	n, err := base64.StdEncoding.Decode(out, b)
	if err != nil {
		l.setErr("invalid base64 in byte-slice value: %v", err)
		return nil
	}
	return out[:n]
}
