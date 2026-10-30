package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// leafRead emits a decode statement for a custom leaf into ptrExpr.
func (g *generator) leafRead(ls *leafSpec, ptrExpr, ind string) {
	g.p("%s%s%s(l, %s)", ind, qualDot(ls.readQual), ls.readFn, ptrExpr)
}

// enumHelper is one zjReadEnum<T> function the generated file needs: the
// per-element decoder for an enum type appearing as a slice element or map
// value, which switches to the declared constants exactly like the field
// path does and COPIES an unknown value (interning is opt-in per field via
// zerojson:"intern", which has no slice-element form — see README).
type enumHelper struct {
	name    string
	typeStr string
	consts  []enumConst
}

// leafReadExpr returns a decode expression for a leaf-kind value.
func (g *generator) leafReadExpr(fi *fieldInfo) string {
	switch fi.kind {
	case kString:
		return "l.ReadString()"
	case kJSONNumber:
		return fmt.Sprintf("%s(l.ReadJSONNumber())", fi.typeStr)
	case kEnum:
		return g.enumReadCall(fi)
	case kBool:
		return fmt.Sprintf("%s(l.ReadBool())", fi.typeStr)
	case kInt, kQuotedInt, kUint:
		return fmt.Sprintf("%s(%s)", fi.typeStr, fi.intRead)
	case kFloat32:
		return fmt.Sprintf("%s(l.ReadFloat32())", fi.typeStr)
	case kFloat64:
		return fmt.Sprintf("%s(l.ReadFloat64())", fi.typeStr)
	case kTime:
		return "l.ReadTime()"
	case kUUID:
		return fmt.Sprintf("%s(l.ReadUUIDBytes())", fi.typeStr)
	case kByteSlice:
		return byteSliceReadExpr(*fi)
	}
	return ""
}

// enumReadCall registers (once) and returns the call to the zjReadEnum
// helper for fi's enum type.
func (g *generator) enumReadCall(fi *fieldInfo) string {
	h, ok := g.enumHelpers[fi.typeStr]
	if !ok {
		// The sanitized name is lossy (`a_b.X` and `a.b_X` collide), so it
		// is de-duplicated against helpers already registered for OTHER
		// type expressions.
		ident := strings.NewReplacer(".", "_", "[", "_", "]", "_", "*", "_").Replace(fi.typeStr)
		name := "zjReadEnum_" + ident
		for taken := true; taken; {
			taken = false
			for _, other := range g.enumHelpers {
				if other.name == name {
					name += "_"
					taken = true
					break
				}
			}
		}
		h = &enumHelper{name: name, typeStr: fi.typeStr, consts: fi.enumConsts}
		g.enumHelpers[fi.typeStr] = h
	}
	return h.name + "(l)"
}

// emitEnumHelpers writes every registered zjReadEnum helper, in a stable
// order, after the type bodies.
func (g *generator) emitEnumHelpers() {
	names := make([]string, 0, len(g.enumHelpers))
	for k := range g.enumHelpers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		h := g.enumHelpers[k]
		g.p("")
		g.p("// %s decodes one %s value (a slice element or map value): a declared", h.name, h.typeStr)
		g.p("// constant by switch, any other value copied — never interned, since")
		g.p("// interning is a per-field opt-in that has no element-level form.")
		g.p("func %s(l *zerojson.Lexer) %s {", h.name, h.typeStr)
		g.p("\tswitch b := l.ReadEnumBytes(); string(b) {")
		for _, c := range h.consts {
			g.p("\tcase %q:", c.val)
			g.p("\t\treturn %s", c.name)
		}
		g.p("\tdefault:")
		g.p("\t\treturn %s(string(b))", h.typeStr)
		g.p("\t}")
		g.p("}")
	}
}

func (g *generator) genArena(ti typeInfo) {
	ownCells := false
	for _, f := range ti.fields {
		ownCells = ownCells || f.ptr
	}
	block := g.typeHasCells(ti.name, map[string]bool{})
	g.p("")
	g.p("// %s holds per-call working state for a decoded %s: publication", arenaName(ti.name), ti.name)
	g.p("// flags, container headers, and nested types' arenas. It stays off the")
	g.p("// heap; pointer cells live in a cell block allocated on first use.")
	g.p("// Detached storage is not reused within the same decode.")
	g.p("type %s struct {", arenaName(ti.name))
	if block {
		g.p("\tzjBlock *%s", cellsName(ti.name))
	}
	for _, f := range ti.fields {
		if f.ptr {
			g.p("\tpublished%s bool", f.cellName)
		}
		if f.kind == kStruct {
			g.p("\ta%s %s", f.cellName, arenaName(f.elem))
			continue
		}
		switch f.kind {
		case kSlice:
			g.p("\tc%s %s", f.cellName, sliceCellType(f.elem, f.elemPtr))
		case kLeafSlice:
			g.p("\tc%s %s", f.cellName, sliceCellType(f.valInfo.typeStr, f.elemPtr))
		case kMap:
			g.p("\tm%s %s", f.cellName, f.typeStr)
		}
	}
	g.p("}")
	if !block {
		return
	}
	g.p("")
	g.p("// %s holds the first-use pointer cells of one %s decode, and those of", cellsName(ti.name), ti.name)
	g.p("// its nested structs, in a single allocation made only when the input")
	g.p("// sets a nil pointer field.")
	g.p("type %s struct {", cellsName(ti.name))
	for _, f := range ti.fields {
		switch {
		case f.ptr && f.kind == kStruct:
			g.p("\tv%s %s", f.cellName, f.elem)
		case f.ptr:
			g.p("\tf%s %s", f.cellName, f.typeStr)
		}
		if f.kind == kStruct && g.typeHasCells(f.elem, map[string]bool{}) {
			g.p("\ts%s %s", f.cellName, cellsName(f.elem))
		}
	}
	g.p("}")
	if !ownCells {
		return
	}
	g.p("")
	g.p("func (a *%s) zjCells() *%s {", arenaName(ti.name), cellsName(ti.name))
	g.p("\tif a.zjBlock == nil {")
	g.p("\t\ta.zjBlock = new(%s)", cellsName(ti.name))
	g.p("\t}")
	g.p("\treturn a.zjBlock")
	g.p("}")
}

// typeHasCells reports whether decoding the named generated struct can
// publish a pointer cell, in its own fields or in a nested struct field's.
func (g *generator) typeHasCells(name string, visiting map[string]bool) bool {
	if visiting[name] {
		return false
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		for _, f := range ti.fields {
			if f.ptr || (f.kind == kStruct && g.typeHasCells(f.elem, visiting)) {
				return true
			}
		}
	}
	return false
}

// emitShareCells points a nested struct's arena at its slot in this
// decode's cell block when the block already exists, so a populated
// message keeps one cell allocation. Otherwise the nested decode allocates
// its own block on first use. The link is made only while the nested arena
// has no block, so a published cell never moves.
func (g *generator) emitShareCells(f fieldInfo, ind string) {
	if !g.typeHasCells(f.elem, map[string]bool{}) {
		return
	}
	g.p("%sif a.a%s.zjBlock == nil && a.zjBlock != nil {", ind, f.cellName)
	g.p("%s\ta.a%s.zjBlock = &a.zjBlock.s%s", ind, f.cellName, f.cellName)
	g.p("%s}", ind)
}

func sliceCellType(elemType string, elemPtr bool) string {
	if elemPtr {
		return "[]*" + elemType
	}
	return "[]" + elemType
}

// Custom element methods can inspect or replace the container while it is
// being decoded. Let stdlib own that traversal and publication protocol;
// an arena-backed private header cannot reproduce those observations.
func stdlibMethodContainer(f fieldInfo) bool {
	return (f.kind == kLeafSlice || f.kind == kMap) && f.valInfo != nil &&
		(f.valInfo.kind == kMarshaler || f.valInfo.kind == kTextMarshaler)
}

// Delegation must not bypass explicitly registered/marker reader contracts.
func (g *generator) stdlibValue(f fieldInfo, visiting map[string]bool) bool {
	switch f.kind {
	case kCustomLeaf:
		return isRawLeaf(f)
	case kQuotedInt, kAppender:
		return false
	case kStruct, kSlice:
		if visiting[f.elem] {
			return true
		}
		visiting[f.elem] = true
		defer delete(visiting, f.elem)
		for _, ti := range g.types {
			if ti.name != f.elem {
				continue
			}
			for _, child := range ti.fields {
				if !g.stdlibValue(child, visiting) {
					return false
				}
			}
			return true
		}
		return false
	case kLeafSlice, kMap:
		return f.valInfo != nil && g.stdlibValue(*f.valInfo, visiting)
	}
	return true
}

// Preserve an existing receiver, including aliases held elsewhere in the
// caller's object graph. An arena cell can be published only once per public
// decode: null may detach it after a callback has retained its address.
// No nested call or ordered bail permits republishing a detached cell.
func (g *generator) emitPointerCellInit(f fieldInfo, target, cell, ind string) {
	g.p("%sif %s == nil {", ind, target)
	g.p("%s\tif a.published%s {", ind, f.cellName)
	g.p("%s\t\t%s = new(%s)", ind, target, f.typeStr)
	g.p("%s\t} else {", ind)
	// An unpublished cell in this call's fresh cell block is already zero.
	g.p("%s\t%s = &%s", ind, target, cell)
	g.p("%s\ta.published%s = true", ind, f.cellName)
	g.p("%s\t}", ind)
	g.p("%s}", ind)
}

// The target is authoritative even on first use: a callback or another wire
// representation may replace its slice header, even without an intervening
// null. Never recycle the detached array in place of the current target.
func (g *generator) emitSlicePublication(f fieldInfo, target, cell, ind string) {
	source := target
	present := target + " != nil"
	if f.ptr {
		source = "*" + target
		present += " && " + source + " != nil"
	}
	g.p("%sif %s {", ind, present)
	g.p("%s\t%s = %s", ind, cell, source)
	g.p("%s} else {", ind)
	g.p("%s\t%s = nil", ind, cell)
	g.p("%s}", ind)
}

// Empty arrays replace the slice with a non-nil, zero-capacity value, as
// stdlib does. Keeping an arena's old capacity would expose previous-message
// elements when a later duplicate field reuses those slots for JSON null.
// The empty composite literal itself does not allocate backing storage.
func (g *generator) emitNonNilEmptySlice(cell, cellType, ind string) {
	g.p("%sif len(%s) == 0 {", ind, cell)
	g.p("%s\t%s = %s{}", ind, cell, cellType)
	g.p("%s}", ind)
}

// emitDecodeBody emits the decode the public entry points share once any
// validity check has passed: decode data into v, then require that nothing
// but whitespace follows.
func (g *generator) emitDecodeBody(n, borrow string) {
	g.p("\tl := zerojson.NewLexer(data, %s)", borrow)
	g.p("\ta := &%s{}", arenaName(n))
	g.p("\t%s(&l, v, a)", decodeName(n))
	g.p("\tl.Finish()")
	g.p("\treturn l.Err()")
}

// emitTrustedDecode emits UnmarshalZJSONTrusted under -trusted.
func (g *generator) emitTrustedDecode(n string) {
	if !g.trusted {
		return
	}
	g.p("")
	g.p("// UnmarshalZJSONTrusted is UnmarshalZJSON without the up-front check that")
	g.p("// data is valid JSON, for data already known to be valid, such as")
	g.p("// AppendZJSON output read back from storage that detects corruption.")
	g.p("// Invalid JSON may decode without an error.")
	g.p("func (v *%s) UnmarshalZJSONTrusted(data []byte) error {", n)
	g.emitDecodeBody(n, "false")
	g.p("}")
}

func (g *generator) genDecode(ti typeInfo) {
	g.p("")
	g.p("// UnmarshalZJSON decodes data into v, copying strings and Raw values.")
	g.p("// Existing pointers and ordinary slice elements are reused as in stdlib.")
	g.p("// Registered leaf contracts may override traversal and reuse.")
	g.p("// Custom codecs define their own value/null behavior. Errors may leave v")
	g.p("// partially changed. Complete JSON grammar is validated before decoding.")
	g.p("func (v *%s) UnmarshalZJSON(data []byte) error {", ti.name)
	g.p("\treturn v.unmarshalZJSON(data, false)")
	g.p("}")
	g.p("")
	g.p("// UnmarshalZJSONBorrow is UnmarshalZJSON with borrowed strings and Raw")
	g.p("// values. Keep data unmodified while any result refers to it; input")
	g.p("// buffers may be reused only after those results are no longer needed.")
	g.p("func (v *%s) UnmarshalZJSONBorrow(data []byte) error {", ti.name)
	g.p("\treturn v.unmarshalZJSON(data, true)")
	g.p("}")
	g.p("")
	g.p("func (v *%s) unmarshalZJSON(data []byte, borrow bool) error {", ti.name)
	g.p("\tif err := zerojson.Valid(data); err != nil { return err }")
	g.emitDecodeBody(ti.name, "borrow")
	g.p("}")
	g.emitTrustedDecode(ti.name)
	g.genReadZJSON(ti.name)
	g.emitFieldName(ti)

	orderedOK := g.ordered && g.canOrderType(ti)
	g.p("")
	g.p("// %s tries the speculative ordered fast path first (see ordered.go),", decodeName(ti.name))
	g.p("// falling back to the general keyed decoder on any deviation from the")
	g.p("// compact, declaration-ordered shape zerojson's own encoder emits.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", decodeName(ti.name), ti.name, arenaName(ti.name))
	g.p("\tif l.IsNull() {")
	g.p("\t\t// Null leaves a value-typed struct unchanged. The public wrapper")
	g.p("\t\t// still calls Finish to reject trailing data.")
	g.p("\t\treturn")
	g.p("\t}")
	if orderedOK {
		g.p("\tstart := l.Mark()")
		g.p("\tif %s(l, v, a) {", orderedDecodeName(ti.name))
		g.p("\t\treturn")
		g.p("\t}")
		g.p("\tif l.Err() != nil {")
		g.p("\t\t// A value reader or strict skip already produced the final")
		g.p("\t\t// decode error. Retrying cannot succeed and can duplicate")
		g.p("\t\t// attacker-controlled allocation work (for example, a large")
		g.p("\t\t// invalid base64 byte slice), so preserve the first error.")
		g.p("\t\treturn")
		g.p("\t}")
		g.p("\tl.OrderedBail(start)")
	}
	g.p("\t%s(l, v, a)", fallbackDecodeName(ti.name))
	g.p("}")

	if orderedOK {
		g.emitOrderedDecode(ti)
		g.emitOrderedField(ti)
		g.emitOrderedTryKey(ti)
		g.emitOrderedSeekKey(ti)
		g.emitOrderedDeclaredKey(ti)
	}

	g.p("")
	g.p("// %s is the general keyed decoder: every member is read via", fallbackDecodeName(ti.name))
	g.p("// ReadKey and dispatched by a string switch. It handles arbitrary")
	g.p("// field order and is the fallback for structural ordered-path mismatches.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", fallbackDecodeName(ti.name), ti.name, arenaName(ti.name))
	g.p("\t_ = a")
	g.p("\tif !l.EnterObject() {")
	g.p("\t\treturn")
	g.p("\t}")
	if orderedOK {
		// The ordered decoder hands a deviating object's remaining members
		// to the same loop, so it lives in its own function.
		g.p("\t%s(l, v, a)", membersDecodeName(ti.name))
		g.p("}")
		g.p("")
		g.p("// %s decodes the remaining members of an object whose opening", membersDecodeName(ti.name))
		g.p("// brace and nesting level are already consumed, positioned at a key.")
		g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", membersDecodeName(ti.name), ti.name, arenaName(ti.name))
		g.p("\t_ = a")
	}
	g.p("\tfor {")
	g.p("\t\tkey := l.ReadKey()")
	g.p("\t\tif l.Err() != nil { return }")
	g.p("\t\tname := string(key)")
	g.p("\t\tif l.IsNull() {")
	g.p("\t\t\tname = zjFieldName_%s(name)", ti.name)
	g.emitNullSwitch(ti)
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\tl.AnnotateField(key)")
	g.p("\t\t\t\treturn")
	g.p("\t\t\t}")
	g.p("\t\t\tif !l.NextMember() { break }")
	g.p("\t\t\tcontinue")
	g.p("\t\t}")
	g.p("\t\tzjDispatch:")
	g.p("\t\tswitch name {")
	for _, f := range ti.fields {
		g.p("\t\tcase %q:", f.jsonName)
		g.emitDecode(f, false)
	}
	g.p("\t\tdefault:")
	g.p("\t\t\tif folded := %s(name); folded != name { name = folded; goto zjDispatch }", foldFieldNameFunc(ti.name))
	g.p("\t\t\tl.Skip()")
	g.p("\t\t}")
	g.p("\t\tif l.Err() != nil {")
	g.p("\t\t\tl.AnnotateField(key)")
	g.p("\t\t\treturn")
	g.p("\t\t}")
	g.p("\t\tif !l.NextMember() { break }")
	g.p("\t}")
	g.p("}")
}

// Exact names take precedence. Folded ties follow field index order, like
// encoding/json's first folded-name match, including Unicode simple folds.
// zjFieldName_<T> checks the exact names first; callers that have already
// ruled them out call zjFoldFieldName_<T> directly.
func (g *generator) emitFieldName(ti typeInfo) {
	g.p("func zjFieldName_%s(name string) string {", ti.name)
	if len(ti.fields) != 0 {
		g.p("\tswitch name {")
		for _, f := range ti.fields {
			g.p("\tcase %q: return name", f.jsonName)
		}
		g.p("\t}")
	}
	g.p("\treturn %s(name)", foldFieldNameFunc(ti.name))
	g.p("}")
	g.p("")
	g.emitFoldFieldName(ti)
}

// emitFoldFieldName emits zjFoldFieldName_<T>(name), the first declared
// name that strings.EqualFold matches, or name itself. name must not be a
// declared name exactly: an exact match can also fold onto an earlier field.
//
// An all-ASCII key, nearly every key on the wire, matches a declared name
// exactly when the key, lowercased, equals that name's ASCII fold key (see
// asciiFoldKey), so one switch replaces a strings.EqualFold call per field.
// Any other key takes the EqualFold chain.
func (g *generator) emitFoldFieldName(ti typeInfo) {
	n := ti.name
	g.p("func %s(name string) string {", foldFieldNameFunc(n))
	if len(ti.fields) == 0 {
		g.p("\treturn name")
		g.p("}")
		return
	}
	type foldCase struct{ key, name string }
	var cases []foldCase
	seen := map[string]bool{}
	width := 0
	for _, f := range ti.fields {
		key, ok := asciiFoldKey(f.jsonName)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		cases = append(cases, foldCase{key, f.jsonName})
		width = max(width, len(key))
	}
	if len(cases) == 0 {
		g.p("\tfor i := 0; i < len(name); i++ {")
		g.p("\t\tif name[i] >= 0x80 { goto zjUnicode }")
		g.p("\t}")
		g.p("\treturn name")
	} else {
		g.p("\tvar lower [%d]byte", width)
		g.p("\tfor i := 0; i < len(name); i++ {")
		g.p("\t\tc := name[i]")
		g.p("\t\tif c >= 0x80 { goto zjUnicode }")
		g.p("\t\tif i < len(lower) {")
		g.p("\t\t\tif 'A' <= c && c <= 'Z' { c += 'a' - 'A' }")
		g.p("\t\t\tlower[i] = c")
		g.p("\t\t}")
		g.p("\t}")
		g.p("\tif len(name) > len(lower) { return name }")
		g.p("\tswitch string(lower[:len(name)]) {")
		for _, c := range cases {
			g.p("\tcase %q: return %q", c.key, c.name)
		}
		g.p("\t}")
		g.p("\treturn name")
	}
	g.p("zjUnicode:")
	q := g.importAlias("strings", "strings")
	for _, f := range ti.fields {
		g.p("\tif %s.EqualFold(name, %q) { return %q }", q, f.jsonName, f.jsonName)
	}
	g.p("\treturn name")
	g.p("}")
}

// asciiFoldKey returns the lowercase ASCII string that an all-ASCII key,
// lowercased, must equal to match name under strings.EqualFold. EqualFold
// compares rune by rune, and an ASCII byte matches exactly the runes whose
// simple-fold orbit contains it, so each rune of name contributes the
// lowercased ASCII member of its orbit: itself for ASCII, 'k' for the
// Kelvin sign, 's' for long s. It reports false when some rune's orbit has
// no ASCII member, since no ASCII key can then match name.
func asciiFoldKey(name string) (string, bool) {
	key := make([]byte, 0, len(name))
	for _, r := range name {
		c, ok := asciiFoldMember(r)
		if !ok {
			return "", false
		}
		key = append(key, c)
	}
	return string(key), true
}

func asciiFoldMember(r rune) (byte, bool) {
	for f := r; ; {
		if f < utf8.RuneSelf {
			c := byte(f)
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			return c, true
		}
		if f = unicode.SimpleFold(f); f == r {
			return 0, false
		}
	}
}

func foldFieldNameFunc(t string) string { return "zjFoldFieldName_" + t }

// genReadZJSON emits ReadZJSON, zerojson.Reader's method, next to the
// generated AppendZJSON (which already satisfies zerojson.Appender). The
// pair is what lets a generated type be used as a FIELD of a generated
// type in ANOTHER package: that package's generator sees an
// Appender/Reader pair and dispatches through it, the same way it treats
// a hand-written leaf. (Slices and maps of Appender-only types have no
// codec yet; a type that also has MarshalJSON/UnmarshalJSON is delegated
// to those in such positions.) Same-package fields never go
// through this method — they call the zjDecode_<T> dispatcher directly with
// the parent's arena — so it costs nothing on the in-package fast path.
func (g *generator) genReadZJSON(name string) {
	g.p("")
	g.p("// ReadZJSON implements zerojson.Reader: it decodes the next JSON value")
	g.p("// from l into v, so %s can be a field of a zerojson-generated type in", name)
	g.p("// another package. Errors are reported through l, per the Reader contract.")
	g.p("func (v *%s) ReadZJSON(l *zerojson.Lexer) {", name)
	g.p("\ta := &%s{}", arenaName(name))
	g.p("\t%s(l, v, a)", decodeName(name))
	g.p("}")
}

// isNullableField reports whether a JSON null clears f (pointers, slices,
// maps, interfaces, and byte slices go nil). Appender and TextMarshaler
// classification must not hide a named slice/map's underlying kind.
// Null delivery takes precedence over clearing for value-typed JSON
// unmarshalers and Raw (see isNullDelivered).
func isNullableField(f fieldInfo) bool {
	return f.ptr || f.kind == kSlice || f.kind == kLeafSlice || f.kind == kMap || f.kind == kByteSlice || f.kind == kInterface ||
		((f.kind == kAppender || f.kind == kTextMarshaler) && f.scalarEmpty == "len")
}

// isNullDelivered reports whether a JSON null on f is handed to the value
// itself: encoding/json invokes UnmarshalJSON([]byte("null")) on a
// VALUE-typed json.Unmarshaler (including slice- and map-shaped ones
// such as json.RawMessage, which then holds the bytes null), and Raw
// mirrors RawMessage. A pointer field is cleared instead (isNullableField).
// An Appender/Reader type that ALSO has UnmarshalJSON receives the null
// through it (stdlib decodes through that method alone, whether or not
// the type also has MarshalJSON); an Appender-only type is structural to
// stdlib, so null clears a slice/map and leaves other value types alone. A
// ZeroJSONQuotedInt64 marker type is decoded by the fast path, never by
// its own UnmarshalJSON, so null leaves it untouched (documented).
func isNullDelivered(f fieldInfo) bool {
	if f.ptr {
		return false
	}
	return f.kind == kMarshaler || isRawLeaf(f) || (f.kind == kAppender && f.appenderHasUnmarshalJSON)
}

func isRawLeaf(f fieldInfo) bool {
	return f.kind == kCustomLeaf && f.leaf != nil && f.leaf.appendPkg == runtimePath && f.leaf.appendFn == "AppendRaw"
}

// emitNullDelivery emits the statement that hands a just-consumed null to
// the value at target (see isNullDelivered).
func (g *generator) emitNullDelivery(f fieldInfo, target, ind string) {
	if isRawLeaf(f) {
		g.p("%szerojson.ReadRawNull(l, &%s)", ind, target)
		return
	}
	g.p("%sif err := %s.UnmarshalJSON(l.NullBytes()); err != nil {", ind, target)
	g.p("%s\tl.AddError(err)", ind)
	g.p("%s}", ind)
}

// emitNullSwitch clears reference-typed fields on JSON null, matching
// encoding/json: pointers and slices go nil; value fields are untouched.
func (g *generator) emitNullSwitch(ti typeInfo) {
	var nullable []fieldInfo
	for _, f := range ti.fields {
		if isNullableField(f) || isNullDelivered(f) {
			nullable = append(nullable, f)
		}
	}
	if len(nullable) == 0 {
		return
	}
	g.p("\t\t\tswitch name {")
	for _, f := range nullable {
		g.p("\t\t\tcase %q:", f.jsonName)
		if isNullDelivered(f) {
			g.emitNullDelivery(f, "v."+f.access, "\t\t\t\t")
			continue
		}
		if f.kind == kInterface && !f.ptr {
			g.emitInterfaceNull("v."+f.access, "\t\t\t\t")
			continue
		}
		g.p("\t\t\t\tv.%s = nil", f.access)
	}
	g.p("\t\t\t}")
}

// scalarSlice identifies values that cannot invoke user code while the
// slice header is private.
// Bytes, enums, registered readers and method-bearing values keep their
// existing paths; representation and ownership rules differ for those kinds.
func scalarSlice(f fieldInfo) bool {
	if f.kind != kLeafSlice || f.elemPtr || f.byteElem != "" || f.valInfo == nil {
		return false
	}
	switch f.valInfo.kind {
	case kString, kBool, kInt, kUint, kFloat32, kFloat64, kJSONNumber:
		return true
	}
	return false
}

func (g *generator) emitReusedScalarSlice(f fieldInfo, target, ind string) {
	g.p("%sif l.IsNull() { %s = nil } else if l.EnterArray() {", ind, target)
	g.p("%s\tzjSlice := %s", ind, target)
	g.p("%s\tzjIndex := 0", ind)
	g.p("%s\tfor {", ind)
	// Stdlib extends through capacity, preserving even previously hidden
	// elements on null. Newly appended elements alone start at zero.
	g.p("%s\t\tif zjIndex < cap(zjSlice) { zjSlice = zjSlice[:zjIndex+1] } else {", ind)
	g.p("%s\t\t\tvar zjZero %s; zjSlice = append(zjSlice, zjZero)", ind, f.valInfo.typeStr)
	g.p("%s\t\t}", ind)
	g.p("%s\t\tif !l.IsNull() { zjSlice[zjIndex] = %s }", ind, g.leafReadExpr(f.valInfo))
	g.p("%s\t\tzjIndex++", ind)
	g.p("%s\t\tif l.Err() != nil || !l.NextElem() { break }", ind)
	g.p("%s\t}", ind)
	g.p("%s\t%s = zjSlice[:zjIndex]", ind, target)
	g.p("%s} else if l.Err() == nil { %s = make(%s, 0) }", ind, target, f.typeStr)
}

// emitReusedByteSlice is emitReusedScalarSlice for the array-of-bytes form
// of a []byte-kind field decoded into an existing slice: the lexer is at
// '[' and target is non-nil.
func (g *generator) emitReusedByteSlice(f fieldInfo, target, ind string) {
	elem := "byte"
	if f.byteElem != "" {
		elem = f.byteElem
	}
	g.p("%sif l.EnterArray() {", ind)
	g.p("%s\tzjSlice := %s", ind, target)
	g.p("%s\tzjIndex := 0", ind)
	g.p("%s\tfor {", ind)
	g.p("%s\t\tif zjIndex < cap(zjSlice) { zjSlice = zjSlice[:zjIndex+1] } else {", ind)
	g.p("%s\t\t\tvar zjZero %s; zjSlice = append(zjSlice, zjZero)", ind, elem)
	g.p("%s\t\t}", ind)
	g.p("%s\t\tif !l.IsNull() { zjSlice[zjIndex] = %s(l.ReadUint8()) }", ind, elem)
	g.p("%s\t\tzjIndex++", ind)
	g.p("%s\t\tif l.Err() != nil || !l.NextElem() { break }", ind)
	g.p("%s\t}", ind)
	g.p("%s\t%s = zjSlice[:zjIndex]", ind, target)
	g.p("%s} else if l.Err() == nil { %s = make(%s, 0) }", ind, target, f.typeStr)
}

// emitDecode emits a field value read. The caller handles the key, colon,
// and field-level null; the lexer is already positioned at the value.
func (g *generator) emitDecode(f fieldInfo, ordered bool) {
	ind := "\t\t\t"
	target := "v." + f.access
	collection := f.kind == kSlice || f.kind == kLeafSlice || f.kind == kMap
	delegate := stdlibMethodContainer(f) || (collection && g.stdlibValue(f, map[string]bool{}) && g.fieldHasReplayCallbacks(f, map[string]bool{}))
	reusedSlice := !delegate && (f.kind == kSlice || f.kind == kLeafSlice) && g.stdlibValue(f, map[string]bool{})
	if delegate || reusedSlice {
		if reusedSlice {
			g.p("%sif %s != nil {", ind, target)
		}
		if reusedSlice && scalarSlice(f) {
			sliceTarget := target
			if f.ptr {
				sliceTarget = "*" + target
			}
			g.emitReusedScalarSlice(f, sliceTarget, ind)
		} else {
			q := g.importAlias("encoding/json", "json")
			g.p("%sif raw := l.RawValue(); l.Err() == nil {", ind)
			g.p("%s\tif err := %s.Unmarshal(raw, &%s); err != nil { l.AddError(err) }", ind, q, target)
			g.p("%s}", ind)
		}
		if delegate {
			return
		}
		g.p("%s} else {", ind)
		defer g.p("%s}", ind)
	}
	cell := "a.zjCells().f" + f.cellName
	// A mid-loop element error (kSlice/kLeafSlice/kMap) breaks out of
	// the element loop rather than returning, so control reaches the
	// enclosing decoder's error check, which annotates the error with
	// THIS field's name (the container) before unwinding further; the
	// statements between the loop and that check are harmless on error.
	retStmt := "break"

	_ = ordered
	// Existing pointers are caller-owned; fresh pointers use arena cells.
	assign := func(readExpr string) {
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
			g.p("%s*%s = %s", ind, target, readExpr)
		} else {
			g.p("%s%s = %s", ind, target, readExpr)
		}
	}
	// Slice/map fields decode into the arena's per-call collection cell
	// (a.c<N> / a.m<N>) and then alias target to it. For a POINTER to a
	// slice/map (*[]T, *map[string]T — a shape encoding/json supports) the
	// collection goes through the arena's pointer cell (a.f<N>, of the
	// non-pointer type) so target can point at it.
	finishCollection := func(collCell string) {
		assign(collCell)
	}
	// derefTarget is the collection value behind target (for seeding a
	// map cell from an existing non-nil target).
	derefTarget := target
	if f.ptr {
		derefTarget = "(*" + target + ")"
	}

	switch f.kind {
	case kString:
		if f.intern {
			assign("l.ReadInternedString()")
		} else {
			assign("l.ReadString()")
		}
	case kEnum:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		enumTarget := target
		if f.ptr {
			enumTarget = "*" + target
		}
		g.p("%sswitch b := l.ReadEnumBytes(); string(b) {", ind)
		for _, c := range f.enumConsts {
			g.p("%scase %q:", ind, c.val)
			g.p("%s\t%s = %s", ind, enumTarget, c.name)
		}
		g.p("%sdefault:", ind)
		unknown := "string(b)"
		if f.intern {
			unknown = "zerojson.Intern(b)"
		}
		g.p("%s\t%s = %s(%s)", ind, enumTarget, f.typeStr, unknown)
		g.p("%s}", ind)
	case kBool:
		assign(fmt.Sprintf("%s(l.ReadBool())", f.typeStr))
	case kJSONNumber:
		assign(fmt.Sprintf("%s(l.ReadJSONNumber())", f.typeStr))
	case kQuotedInt, kInt, kUint:
		read := f.intRead
		if f.stringOpt {
			read = strings.Replace(read, "l.Read", "l.ReadQuoted", 1)
			// encoding/json accepts the quoted spelling "null" on a
			// `,string` field exactly like a bare null: the value is
			// left untouched, a pointer is cleared.
			g.p("%sif l.IsQuotedNull() {", ind)
			if f.ptr {
				g.p("%s\t%s = nil", ind, target)
			}
			g.p("%s} else {", ind)
			ind += "\t"
			assign(fmt.Sprintf("%s(%s)", f.typeStr, read))
			ind = ind[:len(ind)-1]
			g.p("%s}", ind)
			break
		}
		assign(fmt.Sprintf("%s(%s)", f.typeStr, read))
	case kFloat32:
		assign(fmt.Sprintf("%s(l.ReadFloat32())", f.typeStr))
	case kFloat64:
		assign(fmt.Sprintf("%s(l.ReadFloat64())", f.typeStr))
	case kTime:
		assign("l.ReadTime()")
	case kUUID:
		assign(fmt.Sprintf("%s(l.ReadUUIDBytes())", f.typeStr))
	case kInterface:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
			target = "(*" + target + ")"
		}
		g.emitInterfaceRead(target, ind)
	case kByteSlice:
		// base64 string or numeric byte array → []byte. The string form is
		// always a fresh allocation, as in encoding/json. The ARRAY form on
		// an existing non-nil slice follows the ordinary slice-reuse rule
		// instead: elements are written in place through the capacity and
		// a null element leaves the existing byte untouched — a fresh
		// slice here would zero bytes stdlib preserves (a duplicated
		// member, or a pre-populated target). null for the whole value is
		// handled by the caller's IsNull check, which clears the field via
		// emitNullSwitch (kByteSlice is a nullable field).
		if f.ptr {
			g.p("%sif %s != nil && %s != nil && l.IsDelim('[') {", ind, target, derefTarget)
		} else {
			g.p("%sif %s != nil && l.IsDelim('[') {", ind, target)
		}
		g.emitReusedByteSlice(f, derefTarget, ind+"\t")
		g.p("%s} else {", ind)
		assign(byteSliceReadExpr(f))
		g.p("%s}", ind)
	case kEmptyStruct:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		g.p("%sl.SkipObject()", ind)
	case kCustomLeaf:
		if f.ptr {
			// Keep existing receivers; publish a fresh cell for a nil target.
			g.emitPointerCellInit(f, target, cell, ind)
			g.leafRead(f.leaf, target, ind)
		} else {
			g.leafRead(f.leaf, "&"+target, ind)
		}
	case kAppender:
		// Same convention as kCustomLeaf: the hand-written ReadZJSON method
		// reports errors via l.Fail/l.AddError itself, so there's nothing
		// to check here (matching every other leaf read).
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
			g.p("%s%s.ReadZJSON(l)", ind, target)
		} else {
			g.p("%s(&%s).ReadZJSON(l)", ind, target)
		}
	case kStruct:
		nestedCall := func(targetExpr, arenaExpr string) {
			if ordered {
				// Direct call to the nested type's pure ordered decoder:
				// its bail propagates as ours, so the whole top-level
				// object restarts on ITS fallback (see ordered.go).
				// On a real error the enclosing field name must be recorded
				// here, since this returns before the field function's own
				// trailing check; on a bail l.err is nil and this is a no-op.
				g.p("%sif !%s(l, %s, %s) {", ind, orderedDecodeName(f.elem), targetExpr, arenaExpr)
				g.p("%s\tl.AnnotateFieldString(%q)", ind, f.jsonName)
				g.p("%s\treturn false", ind)
				g.p("%s}", ind)
			} else {
				g.p("%s%s(l, %s, %s)", ind, decodeName(f.elem), targetExpr, arenaExpr)
			}
		}
		if f.ptr {
			g.emitPointerCellInit(f, target, "a.zjCells().v"+f.cellName, ind)
			g.emitShareCells(f, ind)
			nestedCall(target, "&a.a"+f.cellName)
		} else {
			g.emitShareCells(f, ind)
			nestedCall("&"+target, "&a.a"+f.cellName)
		}
	case kMarshaler:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		// The callback runs only on a cleanly captured value: if RawValue
		// hit a grammar error, the lexer already holds the decode error and
		// handing the (empty or partial) span to user code would invoke
		// its side effects on attacker-controlled malformed input.
		g.p("%sif raw := l.RawValue(); l.Err() == nil {", ind)
		g.p("%s\tif err := %s.UnmarshalJSON(raw); err != nil {", ind, target)
		g.p("%s\t\tl.AddError(err)", ind)
		g.p("%s\t}", ind)
		g.p("%s}", ind)
	case kTextMarshaler:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		g.p("%sif txt := l.ReadTextBytes(); l.Err() == nil {", ind)
		g.p("%s\tif err := %s.UnmarshalText(txt); err != nil {", ind, target)
		g.p("%s\t\tl.AddError(err)", ind)
		g.p("%s\t}", ind)
		g.p("%s}", ind)
	case kSlice:
		cell := "a.c" + f.cellName
		g.emitSlicePublication(f, target, cell, ind)
		// Reset the cell's length to 0 while keeping its current capacity:
		// re-decoding this field onto a
		// non-empty slice (a duplicate key in the fallback loop, or an
		// ordered attempt's confirmed-key write followed by a fallback
		// retry after a later bail) would otherwise double up elements
		// instead of replacing them. It matches encoding/json's own
		// behavior (it resets a slice's length before populating it) and,
		// combined with zeroing every reused slot below, ensures no stale
		// element survives from a prior decode.
		g.p("%s%s = %s[:0]", ind, cell, cell)
		g.p("%sif l.EnterArray() {", ind)
		g.p("%sfor {", ind)
		zero := "nil"
		if !f.elemPtr {
			zero = f.elem + "{}"
		}
		g.p("%s\tif len(%s) == cap(%s) {", ind, cell, cell)
		g.p("%s\t\t%s = append(%s, %s)", ind, cell, cell, zero)
		g.p("%s\t} else {", ind)
		g.p("%s\t\t%s = %s[:len(%s)+1]", ind, cell, cell, cell)
		g.p("%s\t\t%s[len(%s)-1] = %s", ind, cell, cell, zero)
		g.p("%s\t}", ind)
		if f.elemPtr {
			g.p("%s\tif !l.IsNull() {", ind)
			g.p("%s\t\te := new(%s)", ind, f.elem)
			g.p("%s\t\tvar ea %s", ind, arenaName(f.elem))
			g.p("%s\t\t%s(l, e, &ea)", ind, decodeName(f.elem))
			g.p("%s\t\t%s[len(%s)-1] = e", ind, cell, cell)
			g.p("%s\t}", ind)
		} else {
			g.p("%s\tif !l.IsNull() {", ind)
			g.p("%s\t\tvar ea %s", ind, arenaName(f.elem))
			g.p("%s\t\t%s(l, &%s[len(%s)-1], &ea)", ind, decodeName(f.elem), cell, cell)
			g.p("%s\t}", ind)
		}
		g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
		g.p("%s\tif !l.NextElem() { break }", ind)
		g.p("%s}", ind)
		g.p("%s}", ind)
		g.emitNonNilEmptySlice(cell, sliceCellType(f.elem, f.elemPtr), ind)
		finishCollection(cell)
	case kLeafSlice:
		cell := "a.c" + f.cellName
		g.emitSlicePublication(f, target, cell, ind)
		// A byte-kind slice's string representation bypasses element
		// methods in encoding/json; array elements still invoke them.
		if f.byteElem != "" {
			g.p("%sif l.IsDelim('\"') {", ind)
			assign(byteSliceReadExpr(f))
			g.p("%s} else {", ind)
		}
		if f.elemPtr {
			// Rare (only Marshaler/TextMarshaler/Appender elements can
			// be pointers): pool the outer slice-of-pointers only, same
			// reset-first discipline as kSlice.
			g.p("%s%s = %s[:0]", ind, cell, cell)
			g.p("%sif l.EnterArray() {", ind)
			g.p("%sfor {", ind)
			g.p("%s\tvar e *%s", ind, f.valInfo.typeStr)
			g.p("%s\tif !l.IsNull() {", ind)
			g.p("%s\t\te = new(%s)", ind, f.valInfo.typeStr)
			switch f.valInfo.kind {
			case kMarshaler:
				g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := e.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
			case kTextMarshaler:
				g.p("%s\t\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := e.UnmarshalText(txt); err != nil { l.AddError(err) } }", ind)
			case kAppender:
				g.p("%s\t\te.ReadZJSON(l)", ind)
			}
			g.p("%s\t}", ind)
			g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
			g.p("%s\t%s = append(%s, e)", ind, cell, cell)
			g.p("%s\tif !l.NextElem() { break }", ind)
			g.p("%s}", ind)
			g.p("%s}", ind)
			g.emitNonNilEmptySlice(cell, sliceCellType(f.valInfo.typeStr, f.elemPtr), ind)
			finishCollection(cell)
			return
		}
		g.p("%s%s = %s[:0]", ind, cell, cell)
		g.p("%sif l.EnterArray() {", ind)
		g.p("%sfor {", ind)
		g.p("%s\tif len(%s) == cap(%s) {", ind, cell, cell)
		g.p("%s\t\tvar zjZero %s", ind, f.valInfo.typeStr)
		g.p("%s\t\t%s = append(%s, zjZero)", ind, cell, cell)
		g.p("%s\t} else {", ind)
		g.p("%s\t\t%s = %s[:len(%s)+1]", ind, cell, cell, cell)
		g.p("%s\t\tvar zjZero %s", ind, f.valInfo.typeStr)
		g.p("%s\t\t%s[len(%s)-1] = zjZero", ind, cell, cell)
		g.p("%s\t}", ind)
		g.p("%s\tidx := len(%s) - 1", ind, cell)
		g.emitElemNullOpen(f.valInfo, fmt.Sprintf("%s[idx]", cell), ind+"\t")
		switch f.valInfo.kind {
		case kCustomLeaf:
			g.leafRead(f.valInfo.leaf, fmt.Sprintf("&%s[idx]", cell), ind+"\t\t")
		case kMarshaler:
			g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := %s[idx].UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind, cell)
		case kTextMarshaler:
			g.p("%s\t\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := %s[idx].UnmarshalText(txt); err != nil { l.AddError(err) } }", ind, cell)
		case kAppender:
			g.p("%s\t\t%s[idx].ReadZJSON(l)", ind, cell)
		case kInterface:
			g.p("%s\t\t%s[idx] = l.ReadInterface()", ind, cell)
		default:
			g.p("%s\t\t%s[idx] = %s", ind, cell, g.leafReadExpr(f.valInfo))
		}
		g.p("%s\t}", ind)
		g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
		g.p("%s\tif !l.NextElem() { break }", ind)
		g.p("%s}", ind)
		g.p("%s}", ind)
		g.emitNonNilEmptySlice(cell, sliceCellType(f.valInfo.typeStr, f.elemPtr), ind)
		finishCollection(cell)
		if f.byteElem != "" {
			g.p("%s}", ind)
		}
	case kMap:
		cell := "a.m" + f.cellName
		// Seed the helper with an existing NON-NIL map behind target (for a
		// pointer field: the pointer must be non-nil AND point at a
		// non-nil map — a pointer to a nil map must not be adopted, or the
		// first assignment would panic).
		seedCond := target + " != nil"
		if f.ptr {
			seedCond += " && " + derefTarget + " != nil"
		}
		// A sibling pointer or callback may have replaced the target's map
		// header since this field last appeared. Re-adopt it on every member.
		read := g.mapReadCall(f.typeStr, f.keyTypeStr, f.valInfo, f.intern, f.mapEntrySize)
		g.p("%sif %s {", ind, seedCond)
		g.p("%s\t%s = %s(l, %s)", ind, cell, read, derefTarget)
		g.p("%s} else {", ind)
		g.p("%s\t%s = %s(l, nil)", ind, cell, read)
		g.p("%s}", ind)
		finishCollection(cell)
	}
}

// emitElemNullOpen opens the per-element value read: for a null-delivered
// leaf kind it emits `if l.IsNull() { <deliver> } else {`, otherwise the
// plain `if !l.IsNull() {`; the caller closes the block either way.
func (g *generator) emitElemNullOpen(vi *fieldInfo, target, ind string) {
	if vi != nil && vi.kind != kEmptyStruct && vi.kind != kStruct {
		f := *vi
		f.ptr = false
		if isNullDelivered(f) {
			g.p("%sif l.IsNull() {", ind)
			g.emitNullDelivery(f, target, ind+"\t")
			g.p("%s} else {", ind)
			return
		}
	}
	g.p("%sif !l.IsNull() {", ind)
}
