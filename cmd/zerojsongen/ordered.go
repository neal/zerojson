package main

import (
	"fmt"
	"strings"
)

// ordered.go emits the speculative ordered-decode fast path: for a struct
// type whose JSON field names are all safe to embed as literal byte
// constants (see canOrderType), the generator additionally emits
//
//	zjDecodeOrdered_<T>(l, v, a) bool         — the member loop
//	zjDecodeOrderedField_<T>(l, v, a, k) bool — per-field value dispatch
//	zjTryOrderedKey<T>(l, k) bool             — per-field literal key match
//	zjSeekOrderedKey<T>(l, from) int          — one-switch key lookup
//
// zjDecode_<T> (decode.go) tries zjDecodeOrdered_<T> first and falls back to
// zjDecodeFallback_<T> — the unchanged keyed decoder — on structural
// deviations. If the ordered attempt records a real value/grammar error, that
// error is final: retrying the same failing value would only duplicate work.
//
// The matcher exploits the fact that zerojson's own encoder (and
// easyjson, whose bytes fill the existing wire format) emits fields in
// declaration order, compact, with omitempty fields skipped: it walks an
// `expected` cursor forward through the declared fields, trying the
// expected field's literal first and otherwise looking the key up by
// name, so an object missing some omitempty fields still matches the fast
// path (this is what makes projection structs — decoding only a few of the
// wire fields — fast too: unknown keys are skipped inline rather than
// causing a bail). A deviation at a member boundary (out-of-order key,
// duplicate key, escaped key, or non-compact syntax around one) hands the
// rest of the object to the keyed member loop; one inside a value bails:
// the caller rewinds via Lexer.OrderedBail and retries with the fallback.
// A real value/grammar error is retained without retrying the same work.

func orderedDecodeName(t string) string    { return "zjDecodeOrdered_" + t }
func orderedFieldFuncName(t string) string { return "zjDecodeOrderedField_" + t }
func orderedKeyFuncName(t string) string   { return "zjTryOrderedKey" + t }
func orderedSeekFuncName(t string) string  { return "zjSeekOrderedKey" + t }
func declaredKeyFuncName(t string) string  { return "zjIsDeclaredKey" + t }
func fallbackDecodeName(t string) string   { return "zjDecodeFallback_" + t }
func membersDecodeName(t string) string    { return "zjDecodeMembers_" + t }

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
	return g.namesOrderable(ti.name, map[string]bool{}) && !g.typeHasReplayCallbacks(ti.name, map[string]bool{})
}

// namesOrderable reports whether every JSON name of the type AND of every
// struct type its ordered decoder would call directly (kStruct fields,
// value or pointer — the ordered decoder recurses into zjDecodeOrdered_<elem>
// for those, so the nested type must have one) is a plain-ASCII literal.
// Slice elements and map values go through the nested type's dispatcher
// instead, which always exists, so they do not constrain the parent.
func (g *generator) namesOrderable(name string, visiting map[string]bool) bool {
	if visiting[name] {
		return true
	}
	visiting[name] = true
	defer delete(visiting, name)
	for i := range g.types {
		if g.types[i].name != name || g.types[i].rootField != nil {
			continue
		}
		for _, f := range g.types[i].fields {
			if jsonNameNeedsEscape(f.jsonName) {
				return false
			}
			if f.kind == kStruct && !g.namesOrderable(f.elem, visiting) {
				return false
			}
		}
	}
	return true
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

// Existing pointers and collections can share storage with other fields, and
// an existing interface can carry an arbitrary callback. Speculative writes
// cannot safely be replayed: collection growth may detach one alias, making
// the second traversal observably different. Guard collections themselves
// and recurse through value structs. Fresh/reset-first targets still order.
func (g *generator) orderedReuseGuards(ti typeInfo, prefix string) []string {
	var guards []string
	for _, f := range ti.fields {
		target := prefix + f.access
		if f.ptr || f.kind == kInterface || f.kind == kSlice || f.kind == kLeafSlice || f.kind == kByteSlice || f.kind == kMap {
			guards = append(guards, target+" != nil")
		} else if f.kind == kStruct {
			for _, child := range g.types {
				if child.name == f.elem {
					guards = append(guards, g.orderedReuseGuards(child, target+".")...)
					break
				}
			}
		}
	}
	return guards
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
func emitLiteralMismatchChecks(lit, posExpr, binaryQ string) []string {
	var conds []string
	off := 0
	for len(lit)-off >= 8 {
		conds = append(conds, fmt.Sprintf("%s.LittleEndian.Uint64(data[%s+%d:]) != %#x", binaryQ, posExpr, off, leLiteral(lit, off, 8)))
		off += 8
	}
	if len(lit)-off >= 4 {
		conds = append(conds, fmt.Sprintf("%s.LittleEndian.Uint32(data[%s+%d:]) != %#x", binaryQ, posExpr, off, uint32(leLiteral(lit, off, 4))))
		off += 4
	}
	if len(lit)-off >= 2 {
		conds = append(conds, fmt.Sprintf("%s.LittleEndian.Uint16(data[%s+%d:]) != %#x", binaryQ, posExpr, off, uint16(leLiteral(lit, off, 2))))
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
	binaryQ := g.importAlias("encoding/binary", "binary")
	g.p("\tdata, pos := l.DataAndPos()")
	g.p("\tn := len(data)")
	g.p("\tswitch k {")
	for i, f := range ti.fields {
		lit := fmt.Sprintf("%q:", f.jsonName)
		g.p("\tcase %d:", i)
		g.p("\t\tif n-pos < %d {", len(lit))
		g.p("\t\t\treturn false")
		g.p("\t\t}")
		conds := emitLiteralMismatchChecks(lit, "pos", binaryQ)
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

// emitOrderedSeekKey emits zjSeekOrderedKey<T>(l, from) int, which the
// member loop calls when the key at l's position is not field from's
// literal. It reads a plain key (Lexer.PlainKey) once and classifies it:
//
//	k >= from  the first field at or after from whose literal
//	           zjTryOrderedKey<T> would match; consumed
//	-2         a declared name before from, or one that matches only under
//	           case folding; not consumed, so the keyed loop takes the member
//	-3         an unknown name; consumed, so its value is next
//	-1         not a plain key; not consumed, and the caller uses ReadKey
//
// canOrderType admits only plain-ASCII names without quotes, so field k's
// literal matches exactly when a plain key spells k's name; one switch
// replaces trying each remaining field in turn. ReadKey would return the
// same bytes, so -2 and -3 split keys as zjIsDeclaredKey<T> does.
func (g *generator) emitOrderedSeekKey(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("// %s classifies a plain key that is not field from's literal:", orderedSeekFuncName(n))
	g.p("// k >= from is the field it names, consumed; -2 a declared name the")
	g.p("// keyed loop must take, not consumed; -3 an unknown name, consumed;")
	g.p("// -1 not a plain key, not consumed.")
	g.p("func %s(l *zerojson.Lexer, from int) int {", orderedSeekFuncName(n))
	if len(ti.fields) == 0 {
		g.p("\t_, next, ok := l.PlainKey()")
		g.p("\tif !ok {")
		g.p("\t\treturn -1")
		g.p("\t}")
		g.p("\tl.SetPos(next)")
		g.p("\treturn -3")
		g.p("}")
		return
	}
	g.p("\tkey, next, ok := l.PlainKey()")
	g.p("\tif !ok {")
	g.p("\t\treturn -1")
	g.p("\t}")
	g.p("\tk := -1")
	g.p("\tswitch string(key) {")
	for i, f := range ti.fields {
		g.p("\tcase %q:", f.jsonName)
		g.p("\t\tk = %d", i)
	}
	g.p("\t}")
	g.p("\tif k >= from {")
	g.p("\t\tl.SetPos(next)")
	g.p("\t\treturn k")
	g.p("\t}")
	g.p("\tif k >= 0 {")
	g.p("\t\treturn -2")
	g.p("\t}")
	maxLen := 0
	for _, f := range ti.fields {
		maxLen = max(maxLen, len(f.jsonName))
	}
	g.p("\t// The names are ASCII, so a key folds only onto one of its length.")
	g.p("\tif len(key) <= %d {", maxLen)
	g.p("\t\tif name := string(key); %s(name) != name {", foldFieldNameFunc(n))
	g.p("\t\t\treturn -2")
	g.p("\t\t}")
	g.p("\t}")
	g.p("\tl.SetPos(next)")
	g.p("\treturn -3")
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
// not a position-limited word-compare — to classify a key that is not
// plain (zjSeekOrderedKey<T> classifies plain ones the same way): a
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
	g.p("\treturn %s(name) != name", foldFieldNameFunc(n))
	g.p("}")
}

// emitOrderedField emits zjDecodeOrderedField_<T>(l, v, a, k) bool: decodes
// the value for declared field index k (the matching key+colon literal
// has already been consumed), using the exact same lexer read functions
// and arena assignments as the fallback decoder. False propagates either a
// structural mismatch or an error; the dispatcher checks l.Err() to decide
// whether to retry or return the error.
func (g *generator) emitOrderedField(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s, k int) bool {", orderedFieldFuncName(n), n, arenaName(n))
	var nullable []int
	for i, f := range ti.fields {
		if isNullableField(f) || isNullDelivered(f) {
			nullable = append(nullable, i)
		}
	}
	g.p("\tif l.IsNull() {")
	if len(nullable) > 0 {
		g.p("\t\tswitch k {")
		for _, i := range nullable {
			g.p("\t\tcase %d:", i)
			if isNullDelivered(ti.fields[i]) {
				g.emitNullDelivery(ti.fields[i], "v."+ti.fields[i].access, "\t\t\t")
				continue
			}
			if ti.fields[i].kind == kInterface && !ti.fields[i].ptr {
				g.emitInterfaceNull("v."+ti.fields[i].access, "\t\t\t")
				continue
			}
			g.p("\t\t\tv.%s = nil", ti.fields[i].access)
		}
		g.p("\t\t}")
	}
	g.p("\t\tif l.Err() != nil {")
	g.p("\t\t\tl.AnnotateFieldString(%s[k])", fieldNamesVar(n))
	g.p("\t\t\treturn false")
	g.p("\t\t}")
	g.p("\t\treturn true")
	g.p("\t}")
	g.p("\tswitch k {")
	for i, f := range ti.fields {
		g.p("\tcase %d:", i)
		g.emitDecode(f, true)
	}
	g.p("\t}")
	g.p("\tif l.Err() != nil {")
	g.p("\t\tl.AnnotateFieldString(%s[k])", fieldNamesVar(n))
	g.p("\t\treturn false")
	g.p("\t}")
	g.p("\treturn true")
	g.p("}")
	g.p("")
	g.p("// %s are %s's JSON field names by declared index, for error paths.", fieldNamesVar(n), n)
	g.p("var %s = [...]string{", fieldNamesVar(n))
	for _, f := range ti.fields {
		g.p("\t%q,", f.jsonName)
	}
	g.p("}")
}

func fieldNamesVar(t string) string { return "zjFieldNames" + t }

// emitOrderedDecode emits zjDecodeOrdered_<T>(l, v, a) bool: the member
// loop described in the file comment above.
func (g *generator) emitOrderedDecode(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("// %s speculatively decodes a compact, declaration-ordered object", orderedDecodeName(n))
	g.p("// (the shape zerojson's own encoder emits) directly into v, without")
	g.p("// the general keyed loop. A deviation at a member boundary (an")
	g.p("// out-of-order, duplicate, or escaped key, or non-compact syntax")
	g.p("// around one) hands the rest of the object to %s:", membersDecodeName(n))
	g.p("// every member before it was decoded exactly as the keyed decoder")
	g.p("// would, so only the remainder needs the general path. Any other")
	g.p("// deviation returns false, and the caller rewinds via")
	g.p("// Lexer.OrderedBail and retries the object through %s;", fallbackDecodeName(n))
	g.p("// real value/grammar errors are preserved without a redundant retry.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) bool {", orderedDecodeName(n), n, arenaName(n))
	g.p("\t_ = a")
	if guards := g.orderedReuseGuards(ti, "v."); len(guards) != 0 {
		g.p("\t// Caller-owned references and dynamic receivers must not be replayed.")
		g.p("\tif %s { return false }", strings.Join(guards, " || "))
	}
	g.p("\tdata, pos := l.DataAndPos()")
	g.p("\tif pos >= len(data) || data[pos] != '{' {")
	g.p("\t\treturn false")
	g.p("\t}")
	g.p("\tpos++")
	g.p("\tif pos < len(data) && data[pos] == '}' {")
	g.p("\t\t// An empty object still counts as a nesting level.")
	g.p("\t\tif !l.DepthOK() {")
	g.p("\t\t\treturn false")
	g.p("\t\t}")
	g.p("\t\tl.SetPos(pos + 1)")
	g.p("\t\treturn true")
	g.p("\t}")
	g.p("\tl.SetPos(pos)")
	g.p("\t// This decoder consumes the braces itself, so it accounts for the")
	g.p("\t// nesting level itself too (EnterObject does this for the fallback):")
	g.p("\t// an inline Skip of an unknown member must measure depth from here.")
	g.p("\t// A bail leaves the level unbalanced; the dispatcher's OrderedBail")
	g.p("\t// restores it along with the position.")
	g.p("\tif !l.Enter() {")
	g.p("\t\treturn false")
	g.p("\t}")
	g.p("\texpected := 0")
	g.p("\tfor {")
	g.p("\t\tmatched := expected")
	g.p("\t\tif !%s(l, expected) {", orderedKeyFuncName(n))
	g.p("\t\t\tmatched = %s(l, expected)", orderedSeekFuncName(n))
	g.p("\t\t}")
	g.p("\t\tswitch {")
	g.p("\t\tcase matched >= 0:")
	g.p("\t\t\tif !%s(l, v, a, matched) {", orderedFieldFuncName(n))
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\texpected = matched + 1")
	g.p("\t\tcase matched == -3:")
	g.p("\t\t\t// A name matching no declared field, even folded as the keyed")
	g.p("\t\t\t// decoder folds it, is unknown and safe to skip inline (the")
	g.p("\t\t\t// projection-struct fast path).")
	g.p("\t\t\tl.Skip()")
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\tcase matched == -2:")
	g.p("\t\t\t// A declared name out of order, repeated, or matching only")
	g.p("\t\t\t// under case folding: the keyed decoder's last-wins/general")
	g.p("\t\t\t// handling takes over from this member.")
	g.p("\t\t\t%s(l, v, a)", membersDecodeName(n))
	g.p("\t\t\treturn l.Err() == nil")
	g.p("\t\tdefault:")
	g.p("\t\t\tmemberPos := l.Pos()")
	g.p("\t\t\t// A whitespace-padded empty object (`{ }`) is valid but")
	g.p("\t\t\t// cannot literal-match the compact fast path. Bail without")
	g.p("\t\t\t// asking ReadKey to parse the closing brace as a string;")
	g.p("\t\t\t// the fallback will consume it through EnterObject.")
	g.p("\t\t\tif l.IsDelim('}') {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\t// Not a plain key: whitespace around it, an escape, or a")
	g.p("\t\t\t// control or non-ASCII byte. Read it properly and check its")
	g.p("\t\t\t// *name*: a declared name (duplicate, out of order, folded,")
	g.p("\t\t\t// or escaped) hands the member to the keyed decoder; only an")
	g.p("\t\t\t// unknown name is skipped inline.")
	g.p("\t\t\tkey := l.ReadKey()")
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
	g.p("\t\t\tif %s(string(key)) {", declaredKeyFuncName(n))
	g.p("\t\t\t\tl.SetPos(memberPos)")
	g.p("\t\t\t\t%s(l, v, a)", membersDecodeName(n))
	g.p("\t\t\t\treturn l.Err() == nil")
	g.p("\t\t\t}")
	g.p("\t\t\tl.Skip()")
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\treturn false")
	g.p("\t\t\t}")
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
	g.p("\t\t\tl.Leave()")
	g.p("\t\t\treturn true")
	g.p("\t\tdefault:")
	g.p("\t\t\t// Whitespace before the separator: the member is complete,")
	g.p("\t\t\t// so the keyed decoder continues from here. NextMember")
	g.p("\t\t\t// leaves the nesting level on the closing brace.")
	g.p("\t\t\tif l.NextMember() {")
	g.p("\t\t\t\t%s(l, v, a)", membersDecodeName(n))
	g.p("\t\t\t}")
	g.p("\t\t\treturn l.Err() == nil")
	g.p("\t\t}")
	g.p("\t}")
	g.p("}")
}
