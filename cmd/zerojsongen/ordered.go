package main

import (
	"fmt"
	"strings"
)

// ordered.go emits the speculative ordered-decode fast path: for a struct
// type whose JSON field names are all safe to embed as literal byte
// constants (see canOrderType), the generator additionally emits
//
//	zjDecodeOrdered<T>(l, v, a) bool       — the member loop
//	zjDecodeOrderedField<T>(l, v, a, k) bool — per-field value dispatch
//	zjTryOrderedKey<T>(l, k) bool           — per-field literal key match
//
// zjDecode<T> (decode.go) tries zjDecodeOrdered<T> first and falls back to
// zjDecodeFallback<T> — the unchanged keyed decoder — on structural
// deviations. If the ordered attempt records a real value/grammar error, that
// error is final: retrying the same failing value would only duplicate work.
//
// The matcher exploits the fact that zerojson's own encoder (and
// easyjson, whose bytes fill the existing wire format) emits fields in
// declaration order, compact, with omitempty fields skipped: it walks an
// `expected` cursor forward through the declared fields, so an object
// missing some omitempty fields still matches the fast path (this is what
// makes projection structs — decoding only a few of the wire fields —
// fast too: unmatched keys are skipped inline rather than causing a
// bail). Any structural deviation (out-of-order key, duplicate key, escaped
// key, or non-compact syntax) bails: the caller rewinds via Lexer.OrderedBail
// and retries with the fallback. A real value/grammar error is retained
// without retrying the same work.

func orderedDecodeName(t string) string    { return "zjDecodeOrdered" + t }
func orderedFieldFuncName(t string) string { return "zjDecodeOrderedField" + t }
func orderedKeyFuncName(t string) string   { return "zjTryOrderedKey" + t }
func declaredKeyFuncName(t string) string  { return "zjIsDeclaredKey" + t }
func fallbackDecodeName(t string) string   { return "zjDecodeFallback" + t }

// jsonNameNeedsEscape reports whether name would be encoded with any
// escape by zerojson.AppendString (control chars, '"', '\\', and the
// HTML set — plus, conservatively, anything non-ASCII). The ordered
// decoder's key literals are raw bytes, so a field whose name needs
// escaping can't be represented as a plain literal; canOrderType refuses
// the whole type rather than risk it.
func jsonNameNeedsEscape(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' || c > 0x7E {
			return true
		}
	}
	return false
}

// canOrderType reports whether a type is eligible for the ordered fast
// path: every declared field's JSON name must be a plain-ASCII literal and
// no reachable field may invoke a user callback that speculation could replay.
func (g *generator) canOrderType(ti typeInfo) bool {
	for _, f := range ti.fields {
		if jsonNameNeedsEscape(f.jsonName) {
			return false
		}
	}
	return !g.typeHasReplayCallbacks(ti.name, map[string]bool{})
}

// typeHasReplayCallbacks reports whether speculative decoding could invoke a
// user callback before a later key forces fallback. Such callbacks may have
// receiver or external side effects that cannot be rolled back, so schemas
// containing them use the keyed decoder directly. Built-in/custom leaf
// functions retain their extension contract; JSON/Text/Appender interfaces
// have ordinary Go callback semantics and must be invoked exactly once.
func (g *generator) typeHasReplayCallbacks(name string, visiting map[string]bool) bool {
	if visiting[name] {
		return false
	}
	visiting[name] = true
	defer delete(visiting, name)
	var ti *typeInfo
	for i := range g.types {
		if g.types[i].name == name {
			ti = &g.types[i]
			break
		}
	}
	if ti == nil || ti.rootField != nil {
		return false
	}
	for _, f := range ti.fields {
		if g.fieldHasReplayCallbacks(f, visiting) {
			return true
		}
	}
	return false
}

func (g *generator) fieldHasReplayCallbacks(f fieldInfo, visiting map[string]bool) bool {
	switch f.kind {
	case kMarshaler, kTextMarshaler, kAppender:
		return true
	case kStruct, kSlice:
		return g.typeHasReplayCallbacks(f.elem, visiting)
	case kLeafSlice:
		return f.valInfo != nil && (f.valInfo.kind == kMarshaler || f.valInfo.kind == kTextMarshaler || f.valInfo.kind == kAppender)
	case kMap:
		if f.valInfo == nil {
			return false
		}
		if f.valInfo.kind == kStruct {
			return g.typeHasReplayCallbacks(f.valInfo.elem, visiting)
		}
		return f.valInfo.kind == kMarshaler || f.valInfo.kind == kTextMarshaler || f.valInfo.kind == kAppender
	}
	return false
}

// leLiteral packs n bytes of s starting at off into a little-endian
// integer constant, for word-compare key literal checks: a handful of
// integer compares fold the quote+key+quote+colon sequence into one
// operation instead of a byte-by-byte scan.
func leLiteral(s string, off, n int) uint64 {
	var v uint64
	for j := range n {
		v |= uint64(s[off+j]) << (8 * j)
	}
	return v
}

// emitLiteralMismatchChecks returns one "!=" condition per 8/4/2/1-byte
// chunk of lit, each comparing the bytes at posExpr+offset against a
// compile-time little-endian constant. The loads use binary.LittleEndian
// (not a native-endian unsafe.Add load) so the compare is correct on any
// target: the constants are little-endian-packed (leLiteral), so a
// big-endian target must byte-swap the load to match — a native load there
// would always mismatch and silently disable the fast path. On a
// little-endian target binary.LittleEndian.UintN compiles to the identical
// single unaligned load, and the enclosing `if n-pos < len(lit)` guard lets
// the bounds check on each chunk be eliminated.
func emitLiteralMismatchChecks(lit, posExpr string) []string {
	var conds []string
	off := 0
	for len(lit)-off >= 8 {
		conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint64(data[%s+%d:]) != %#x", posExpr, off, leLiteral(lit, off, 8)))
		off += 8
	}
	if len(lit)-off >= 4 {
		conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint32(data[%s+%d:]) != %#x", posExpr, off, uint32(leLiteral(lit, off, 4))))
		off += 4
	}
	if len(lit)-off >= 2 {
		conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint16(data[%s+%d:]) != %#x", posExpr, off, uint16(leLiteral(lit, off, 2))))
		off += 2
	}
	if len(lit)-off == 1 {
		conds = append(conds, fmt.Sprintf("data[%s+%d] != %#x", posExpr, off, lit[off]))
	}
	return conds
}

// emitOrderedTryKey emits zjTryOrderedKey<T>(l, k): attempts to match and
// consume the literal `"name":` for declared field index k at l's current
// position, leaving the position unchanged on any mismatch (including any
// whitespace, which never appears in the literal so any comparison
// touching it simply fails).
func (g *generator) emitOrderedTryKey(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("// %s matches and consumes the literal `\"name\":` for declared", orderedKeyFuncName(n))
	g.p("// field index k at l's current position (compact JSON only — any")
	g.p("// whitespace simply fails the comparison). Leaves the position")
	g.p("// unchanged on mismatch.")
	g.p("func %s(l *zerojson.Lexer, k int) bool {", orderedKeyFuncName(n))
	if len(ti.fields) == 0 {
		g.p("\treturn false")
		g.p("}")
		return
	}
	g.imports["encoding/binary"] = "binary"
	g.p("\tdata, pos := l.DataAndPos()")
	g.p("\tn := len(data)")
	g.p("\tswitch k {")
	for i, f := range ti.fields {
		lit := fmt.Sprintf("%q:", f.jsonName)
		g.p("\tcase %d:", i)
		g.p("\t\tif n-pos < %d {", len(lit))
		g.p("\t\t\treturn false")
		g.p("\t\t}")
		conds := emitLiteralMismatchChecks(lit, "pos")
		if len(conds) > 0 {
			g.p("\t\tif %s {", joinOr(conds))
			g.p("\t\t\treturn false")
			g.p("\t\t}")
		}
		g.p("\t\tl.SetPos(pos + %d)", len(lit))
		g.p("\t\treturn true")
	}
	g.p("\t}")
	g.p("\treturn false")
	g.p("}")
}

func joinOr(conds []string) string {
	var s strings.Builder
	s.WriteString(conds[0])
	for _, c := range conds[1:] {
		s.WriteString(" || " + c)
	}
	return s.String()
}

// emitOrderedDeclaredKey emits zjIsDeclaredKey<T>(name string) bool,
// reporting whether name is any of T's declared JSON field names
// (regardless of index). The ordered decoder's member loop uses this —
// not a position-limited word-compare — to classify an unmatched key: a
// declared name here means duplicate, out-of-order, non-compact syntax
// around an otherwise-known key (e.g. whitespace before the colon, which
// defeats the literal word-compare but not ReadKey), or an escaped key
// that decodes to a declared name; all of those must bail. Only a name
// matching no declared field is genuinely unknown and safe to skip
// inline — the case that keeps projection structs (declaring only a few
// of the wire fields) on the fast path.
func (g *generator) emitOrderedDeclaredKey(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("func %s(name string) bool {", declaredKeyFuncName(n))
	if len(ti.fields) == 0 {
		g.p("\treturn false")
		g.p("}")
		return
	}
	names := make([]string, len(ti.fields))
	for i, f := range ti.fields {
		names[i] = fmt.Sprintf("%q", f.jsonName)
	}
	g.p("\tswitch name {")
	g.p("\tcase %s:", strings.Join(names, ", "))
	g.p("\t\treturn true")
	g.p("\t}")
	g.p("\treturn false")
	g.p("}")
}

// emitOrderedField emits zjDecodeOrderedField<T>(l, v, a, k) bool: decodes
// the value for declared field index k (the matching key+colon literal
// has already been consumed), using the exact same lexer read functions
// and arena assignments as the fallback decoder, so both paths produce
// identical results by construction. Returns false (a bail, propagated by
// the caller) on any value parse error or nested ordered-decode failure.
func (g *generator) emitOrderedField(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s, k int) bool {", orderedFieldFuncName(n), n, arenaName(n))
	var nullable []int
	for i, f := range ti.fields {
		if isNullableField(f) {
			nullable = append(nullable, i)
		}
	}
	g.p("\tif l.IsNull() {")
	if len(nullable) > 0 {
		g.p("\t\tswitch k {")
		for _, i := range nullable {
			g.p("\t\tcase %d:", i)
			g.p("\t\t\tv.%s = nil", ti.fields[i].access)
			if needsSeenState(ti.fields[i]) {
				g.p("\t\t\ta.s%s = false", ti.fields[i].cellName)
			}
		}
		g.p("\t\t}")
	}
	g.p("\t\treturn true")
	g.p("\t}")
	g.p("\tswitch k {")
	for i, f := range ti.fields {
		g.p("\tcase %d:", i)
		g.emitDecode(f, true)
	}
	g.p("\t}")
	g.p("\treturn l.Err() == nil")
	g.p("}")
}

// emitOrderedDecode emits zjDecodeOrdered<T>(l, v, a) bool: the member
// loop described in the file comment above.
func (g *generator) emitOrderedDecode(ti typeInfo) {
	n := ti.name
	numFields := len(ti.fields)
	g.p("")
	g.p("// %s speculatively decodes a compact, declaration-ordered object", orderedDecodeName(n))
	g.p("// (the shape zerojson's own encoder emits) directly into v, without")
	g.p("// the general keyed loop. It returns false with zero observable")
	g.p("// effect (once the caller rewinds via Lexer.OrderedBail) on any")
	g.p("// structural deviation: out-of-order/duplicate/escaped keys or")
	g.p("// non-compact syntax. The caller retries those through %s;", fallbackDecodeName(n))
	g.p("// real value/grammar errors are preserved without a redundant retry.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) bool {", orderedDecodeName(n), n, arenaName(n))
	g.p("\t_ = a")
	g.p("\tdata, pos := l.DataAndPos()")
	g.p("\tif pos >= len(data) || data[pos] != '{' {")
	g.p("\t\treturn false")
	g.p("\t}")
	g.p("\tpos++")
	g.p("\tif pos < len(data) && data[pos] == '}' {")
	g.p("\t\tl.SetPos(pos + 1)")
	g.p("\t\treturn true")
	g.p("\t}")
	g.p("\tl.SetPos(pos)")
	g.p("\texpected := 0")
	g.p("\tfor {")
	g.p("\t\tmatched := -1")
	g.p("\t\tfor k := expected; k < %d; k++ {", numFields)
	g.p("\t\t\tif %s(l, k) {", orderedKeyFuncName(n))
	g.p("\t\t\t\tmatched = k")
	g.p("\t\t\t\tbreak")
	g.p("\t\t\t}")
	g.p("\t\t}")
	g.p("\t\tif matched < 0 {")
	g.p("\t\t\t// A whitespace-padded empty object (`{ }`) is valid but")
	g.p("\t\t\t// cannot literal-match the compact fast path. Bail without")
	g.p("\t\t\t// asking ReadKey to parse the closing brace as a string;")
	g.p("\t\t\t// the fallback will consume it through EnterObject.")
	g.p("\t\t\tif l.IsDelim('}') {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\t// No declared field k >= expected literal-matched at this")
	g.p("\t\t\t// position. Read the key properly (ReadKey tolerates")
	g.p("\t\t\t// whitespace and unescapes, unlike the word-compare above)")
	g.p("\t\t\t// and check its *name*, not its byte position: a declared")
	g.p("\t\t\t// name here — regardless of index — means duplicate,")
	g.p("\t\t\t// out-of-order, non-compact syntax around an otherwise")
	g.p("\t\t\t// valid key, or an escaped key that decodes to a declared")
	g.p("\t\t\t// name; any of those must bail so the fallback's ")
	g.p("\t\t\t// last-wins/general handling applies. Only a name matching")
	g.p("\t\t\t// no declared field is genuinely unknown and safe to skip")
	g.p("\t\t\t// inline (the projection-struct fast path).")
	g.p("\t\t\tkey := l.ReadKey()")
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\tif %s(string(key)) {", declaredKeyFuncName(n))
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\tl.Skip()")
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t} else {")
	g.p("\t\t\tif !%s(l, v, a, matched) {", orderedFieldFuncName(n))
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\texpected = matched + 1")
	g.p("\t\t}")
	g.p("\t\td, p := l.DataAndPos()")
	g.p("\t\tif p >= len(d) {")
	g.p("\t\t\treturn false")
	g.p("\t\t}")
	g.p("\t\tswitch d[p] {")
	g.p("\t\tcase ',':")
	g.p("\t\t\tl.SetPos(p + 1)")
	g.p("\t\tcase '}':")
	g.p("\t\t\tl.SetPos(p + 1)")
	g.p("\t\t\treturn true")
	g.p("\t\tdefault:")
	g.p("\t\t\treturn false")
	g.p("\t\t}")
	g.p("\t}")
	g.p("}")
}
