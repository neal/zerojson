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
		l.skipWS()
		if l.pos < len(l.data) && l.data[l.pos] == '}' {
			// An empty object is still one nesting level (Valid and
			// encoding/json count it), so it must respect the limit even
			// though there is nothing to descend into.
			if !l.DepthOK() {
				return nil
			}
			l.pos++
			return map[string]any{}
		}
		// Enter here; NextMember leaves on the closing '}' — the same
		// protocol the generated decoders use. Decrementing again here
		// would drive the counter negative and let a later sibling exceed
		// the limit.
		if !l.Enter() {
			return nil
		}
		return l.readObject()
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
			l.setErr("number %s: malformed number", errToken(b))
			return nil
		}
		f, err := strconv.ParseFloat(bytesToString(b), 64)
		if err != nil {
			l.setErr("number %s: %v", errToken(b), numErr(err))
		}
		return f
	}
}

// Object sizes for readObject: members held before the first spill, and the
// spill buffer. They mirror the generated map helpers (see
// cmd/zerojsongen/maps.go).
const (
	objectSlots      = 8
	objectSpillSlots = 40
)

// objectMember is one object member buffered before its map is made.
type objectMember struct {
	k string
	v any
}

// readObject reads the members of an object ReadInterface has entered. The
// map is made once, at its final size, because growing it a member at a time
// costs more than decoding the members. Up to objectSlots members wait on the
// stack; one more moves them to readObjectSpill, whose larger buffer lives in
// its own frame so small objects never pay to zero it.
func (l *Lexer) readObject() any {
	var kv [objectSlots]objectMember
	n := 0
	for {
		k := string(l.ReadKey())
		v := l.ReadInterface()
		if l.err != nil {
			return nil
		}
		if n == len(kv) {
			return l.readObjectSpill(&kv, k, v)
		}
		kv[n] = objectMember{k, v}
		n++
		if !l.NextMember() {
			break
		}
	}
	m := make(map[string]any, n)
	for _, e := range kv[:n] {
		m[e.k] = e.v
	}
	return m
}

// readObjectSpill continues readObject after objectSlots members, given
// them and the member just read. Past its own buffer it makes the map with
// room for twice as many members and inserts the rest directly.
func (l *Lexer) readObjectSpill(first *[objectSlots]objectMember, k string, v any) any {
	var kv [objectSpillSlots]objectMember
	n := copy(kv[:], first[:])
	kv[n] = objectMember{k, v}
	n++
	var m map[string]any
	for l.NextMember() {
		k := string(l.ReadKey())
		v := l.ReadInterface()
		if l.err != nil {
			return nil
		}
		if m != nil {
			m[k] = v
		} else if n < len(kv) {
			kv[n] = objectMember{k, v}
			n++
		} else {
			m = make(map[string]any, 2*len(kv))
			for _, e := range kv[:n] {
				m[e.k] = e.v
			}
			m[k] = v
		}
	}
	if m == nil {
		m = make(map[string]any, n)
		for _, e := range kv[:n] {
			m[e.k] = e.v
		}
	}
	return m
}
