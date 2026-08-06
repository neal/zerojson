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
		if l.depth++; l.depth > maxDepth {
			l.setErr("exceeded max nesting depth")
			return nil
		}
		l.pos++
		m := make(map[string]any)
		l.skipWS()
		if l.pos < len(l.data) && l.data[l.pos] == '}' {
			l.pos++
			l.depth--
			return m
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
		l.depth--
		return m
	case '[':
		if l.depth++; l.depth > maxDepth {
			l.setErr("exceeded max nesting depth")
			return nil
		}
		l.pos++
		// Non-nil empty slice so [] round-trips as [], matching
		// encoding/json (a nil slice would re-encode as null).
		s := []any{}
		l.skipWS()
		if l.pos < len(l.data) && l.data[l.pos] == ']' {
			l.pos++
			l.depth--
			return s
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
		l.depth--
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
		b := l.readNumericBytes()
		f, err := strconv.ParseFloat(bytesToString(b), 64)
		if err != nil {
			l.setErr("number %q: %v", b, err)
		}
		return f
	}
}
