package zerojson

import "bytes"

// hexVal maps ASCII to nibble values; non-hex bytes map to 0xFF so a
// single OR-accumulate detects any invalid character.
var hexVal = func() (t [256]byte) {
	for i := range t {
		t[i] = 0xFF
	}
	for c := byte('0'); c <= '9'; c++ {
		t[c] = c - '0'
	}
	for c := byte('a'); c <= 'f'; c++ {
		t[c] = c - 'a' + 10
	}
	for c := byte('A'); c <= 'F'; c++ {
		t[c] = c - 'A' + 10
	}
	return
}()

// uuidPairs holds the content offsets of the 16 hex pairs in the
// canonical 8-4-4-4-12 form.
var uuidPairs = [16]uint8{0, 2, 4, 6, 9, 11, 14, 16, 19, 21, 24, 26, 28, 30, 32, 34}

// ReadUUIDBytes parses a quoted UUID into its raw 16-byte form. The
// canonical 36-char form is decoded at fixed offsets with a hex table — no
// scan, no external UUID library; anything else (uppercase variants also
// take the fast path; urn:/braced/un-hyphenated forms, escapes) falls back
// to parseUUIDBytes. Generated code casts the result to a domain UUID type
// (e.g. uuid.UUID(l.ReadUUIDBytes())) in the caller's own package, which
// keeps the zerojson runtime itself free of any UUID dependency.
func (l *Lexer) ReadUUIDBytes() [16]byte {
	l.skipWS()
	if l.pos+38 <= len(l.data) && l.data[l.pos] == '"' && l.data[l.pos+37] == '"' {
		b := l.data[l.pos+1 : l.pos+37]
		if b[8] == '-' && b[13] == '-' && b[18] == '-' && b[23] == '-' {
			var u [16]byte
			bad := byte(0)
			for j, i := range uuidPairs {
				hi, lo := hexVal[b[i]], hexVal[b[i+1]]
				bad |= hi | lo
				u[j] = hi<<4 | lo
			}
			if bad&0xF0 == 0 {
				l.pos += 38
				return u
			}
		}
	}
	b, plain, _ := l.readStringBytes()
	if !plain {
		b = unescapeStringBytes(b) // exotic but valid: same string, escaped spelling
	}
	u, ok := parseUUIDBytes(b)
	if !ok {
		l.setErr("uuid: invalid UUID %s", errToken(b))
	}
	return u
}

// parseUUIDBytes parses b into its raw 16-byte form, accepting the same
// forms as google/uuid's ParseBytes — canonical 8-4-4-4-12 (36 bytes),
// urn:uuid:-prefixed (45), braced (38), and un-hyphenated 32-hex — without
// depending on that package. This is only reached for non-canonical input;
// ReadUUIDBytes's fixed-offset fast path above handles the common case.
func parseUUIDBytes(b []byte) (u [16]byte, ok bool) {
	switch len(b) {
	case 36:
		// canonical form, handled below
	case 36 + 9:
		if !bytes.EqualFold(b[:9], []byte("urn:uuid:")) {
			return u, false
		}
		b = b[9:]
	case 36 + 2:
		// Braced form {xxxxxxxx-...}: the wrapper bytes are NOT validated,
		// deliberately matching google/uuid's ParseBytes, which for a
		// 38-byte value does `s = s[1:]` — it strips only the leading byte,
		// never checks either brace, and ignores the trailing byte (so it
		// accepts e.g. "x<canonical>y", reading the same 36 middle bytes).
		// Stripping the first and last byte here reads that identical
		// 36-byte window, so accept/reject and value agree with the oracle
		// for every 38-byte input.
		b = b[1 : len(b)-1]
	case 32:
		bad := byte(0)
		for i := range u {
			hi, lo := hexVal[b[i*2]], hexVal[b[i*2+1]]
			bad |= hi | lo
			u[i] = hi<<4 | lo
		}
		if bad&0xF0 != 0 {
			return u, false
		}
		return u, true
	default:
		return u, false
	}
	if b[8] != '-' || b[13] != '-' || b[18] != '-' || b[23] != '-' {
		return u, false
	}
	bad := byte(0)
	for j, i := range uuidPairs {
		hi, lo := hexVal[b[i]], hexVal[b[i+1]]
		bad |= hi | lo
		u[j] = hi<<4 | lo
	}
	if bad&0xF0 != 0 {
		return u, false
	}
	return u, true
}
