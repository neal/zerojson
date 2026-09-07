package zerojson

import "strconv"

// ReadInterface decodes an arbitrary JSON value into the same shapes
// easyjson's jlexer.Interface produces: map[string]interface{},
// []interface{}, string, float64, bool, nil. Object keys are copied because
// this generic surface accepts arbitrary, potentially high-cardinality keys;
// string values honor the Borrow flag.
func (l *Lexer) ReadInterface() any {
	l.skipWS()
	if l.pos >= len(l.data) {
		l.setErr("unexpected end of input")
		return nil
	}
	switch l.data[l.pos] {
	case '"':
		return l.ReadString()
	case '{':
		l.pos++
		m := make(map[string]any)
		l.skipWS()
		if l.pos < len(l.data) && l.data[l.pos] == '}' {
			// An empty object is still one nesting level (Valid and
			// encoding/json count it), so it must respect the limit even
			// though there is nothing to descend into.
			if !l.DepthOK() {
				return nil
			}
			l.pos++
			return m
		}
		// Enter here; NextMember leaves on the closing '}' — the same
		// protocol the generated decoders use. Decrementing again here
		// would drive the counter negative and let a later sibling exceed
		// the limit.
		if !l.Enter() {
			return nil
		}
		for {
			m[string(l.ReadKey())] = l.ReadInterface()
			if l.err != nil {
				return nil
			}
			if !l.NextMember() {
				break
			}
		}
		return m
	case '[':
		l.pos++
		// Non-nil empty slice so [] round-trips as [], matching
		// encoding/json (a nil slice would re-encode as null).
		s := []any{}
		l.skipWS()
		if l.pos < len(l.data) && l.data[l.pos] == ']' {
			if !l.DepthOK() {
				return nil
			}
			l.pos++
			return s
		}
		if !l.Enter() {
			return nil
		}
		for {
			s = append(s, l.ReadInterface())
			if l.err != nil {
				return nil
			}
			if !l.NextElem() {
				break
			}
		}
		return s
	case 't', 'f':
		return l.ReadBool()
	case 'n':
		if l.IsNull() {
			return nil
		}
		l.setErr("bad literal")
		return nil
	default:
		b := l.readBareNumericBytes()
		if l.err != nil {
			return nil
		}
		if !bareFloatOK(b) {
			l.setErr("number %q: malformed number", b)
			return nil
		}
		f, err := strconv.ParseFloat(bytesToString(b), 64)
		if err != nil {
			l.setErr("number %q: %v", b, err)
		}
		return f
	}
}
