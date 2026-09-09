package main

import (
	"fmt"
	"sort"
	"strings"
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
	g.p("")
	g.p("// %s holds first-use pointer cells for a decoded %s in one", arenaName(ti.name), ti.name)
	g.p("// allocation, and (for -pool Decoders, which reuse an arena across")
	g.p("// decodes instead of allocating one per call) holds a persistent")
	g.p("// cell per slice/map field so repeated decodes reuse their backing")
	g.p("// storage. Detached storage is not reused within the same decode.")
	g.p("type %s struct {", arenaName(ti.name))
	for _, f := range ti.fields {
		if f.ptr {
			g.p("\tpublished%s bool", f.cellName)
		}
		if f.kind == kSlice || f.kind == kLeafSlice {
			g.p("\tslicePublished%s bool", f.cellName)
			g.p("\tsliceBorrowed%s bool // adopted storage must not be recycled on the next pooled call", f.cellName)
		}
		if f.kind == kStruct {
			if f.ptr {
				g.p("\tv%s %s", f.cellName, f.elem)
			}
			g.p("\ta%s %s", f.cellName, arenaName(f.elem))
			continue
		}
		if f.ptr {
			g.p("\tf%s %s", f.cellName, f.typeStr)
		}
		switch f.kind {
		case kSlice:
			g.p("\tc%s %s", f.cellName, sliceCellType(f.elem, f.elemPtr))
		case kLeafSlice:
			g.p("\tc%s %s", f.cellName, sliceCellType(f.valInfo.typeStr, f.elemPtr))
		case kMap:
			g.p("\tm%s %s", f.cellName, f.typeStr)
			g.p("\towned%s bool // only arena-owned maps may be cleared for reuse", f.cellName)
			g.p("\tmapPublished%s bool", f.cellName)
		}
	}
	g.p("}")
	g.p("func (a *%s) beginDecode() {", arenaName(ti.name))
	for _, f := range ti.fields {
		if f.ptr {
			g.p("\ta.published%s = false", f.cellName)
		}
		if f.kind == kMap {
			g.p("\ta.mapPublished%s = false", f.cellName)
		}
		if f.kind == kSlice || f.kind == kLeafSlice {
			g.p("\ta.slicePublished%s = false", f.cellName)
		}
		if f.kind == kStruct {
			g.p("\ta.a%s.beginDecode()", f.cellName)
		}
	}
	g.p("}")
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
// decode: null may detach it after a callback has retained its address. Only
// the next pooled call permits reuse, never a nested call or ordered bail.
func (g *generator) emitPointerCellInit(f fieldInfo, target, cell, ind string) {
	g.p("%sif %s == nil {", ind, target)
	g.p("%s\tif a.published%s {", ind, f.cellName)
	g.p("%s\t\t%s = new(%s)", ind, target, f.typeStr)
	g.p("%s\t} else {", ind)
	g.p("%s\tvar zjZero %s", ind, f.typeStr)
	g.p("%s\t%s = zjZero", ind, cell)
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
	// The target may hand back the arena's own cell: the ordered path
	// published it and then rewound to the keyed decoder, or the member
	// is duplicated. That storage is still ours to recycle on the next
	// pooled call; only a header over SOME OTHER array (supplied by the
	// caller or a callback) is borrowed — and once borrowed, handing the
	// same foreign header back again keeps it borrowed.
	g.p("%s\ta.sliceBorrowed%s = a.sliceBorrowed%s || !zerojson.SameBacking(%s, %s)", ind, f.cellName, f.cellName, source, cell)
	g.p("%s\t%s = %s", ind, cell, source)
	g.p("%s} else if a.slicePublished%s || a.sliceBorrowed%s {", ind, f.cellName, f.cellName)
	g.p("%s\t%s = nil", ind, cell)
	g.p("%s\ta.sliceBorrowed%s = false", ind, f.cellName)
	g.p("%s}", ind)
	g.p("%sa.slicePublished%s = true", ind, f.cellName)
}

// emitNonNilEmptySlice ensures cell is non-nil after a slice/leaf-slice
// decode, matching easyjson (and encoding/json): a JSON `[]` decoded onto
// a never-before-used arena cell leaves it nil (EnterArray's loop body
// never runs, so the `cell[:0]` reset — a no-op on nil — is the last
// write), which would then re-encode as `null` instead of `[]`. This only
// ever fires on first use: a pooled cell already populated by a prior
// decode is already non-nil and this is a no-op (the reslice already
// happened above). The composite literal costs no allocation (Go's
// runtime shares one zero-size backing pointer for it), so this doesn't
// touch the zero-alloc pooled decode budget.
func (g *generator) emitNonNilEmptySlice(cell, cellType, ind string) {
	g.p("%sif %s == nil {", ind, cell)
	g.p("%s\t%s = %s{}", ind, cell, cellType)
	g.p("%s}", ind)
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
	g.p("\tl := zerojson.NewLexer(data, borrow)")
	g.p("\ta := &%s{}", arenaName(ti.name))
	g.p("\t%s(&l, v, a)", decodeName(ti.name))
	g.p("\tl.Finish()")
	g.p("\treturn l.Err()")
	g.p("}")
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
	g.p("\tfor {")
	g.p("\t\tkey := l.ReadKey()")
	g.p("\t\tif l.Err() != nil { return }")
	g.p("\t\tname := zjFieldName_%s(string(key))", ti.name)
	g.p("\t\tif l.IsNull() {")
	g.emitNullSwitch(ti)
	g.p("\t\t\tif l.Err() != nil {")
	g.p("\t\t\t\tl.AnnotateField(key)")
	g.p("\t\t\t\treturn")
	g.p("\t\t\t}")
	g.p("\t\t\tif !l.NextMember() { break }")
	g.p("\t\t\tcontinue")
	g.p("\t\t}")
	g.p("\t\tswitch name {")
	for _, f := range ti.fields {
		g.p("\t\tcase %q:", f.jsonName)
		g.emitDecode(f, false)
	}
	g.p("\t\tdefault:")
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
func (g *generator) emitFieldName(ti typeInfo) {
	g.p("func zjFieldName_%s(name string) string {", ti.name)
	if len(ti.fields) != 0 {
		g.p("\tswitch name {")
		for _, f := range ti.fields {
			g.p("\tcase %q: return name", f.jsonName)
		}
		g.p("\t}")
		q := g.importAlias("strings", "strings")
		for _, f := range ti.fields {
			g.p("\tif %s.EqualFold(name, %q) { return %q }", q, f.jsonName, f.jsonName)
		}
	}
	g.p("\treturn name")
	g.p("}")
}

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

// emitDecode emits the decode statement(s) for a single field's value
// (the lexer is already positioned at the value; key/colon/null handling
// is the caller's responsibility). ordered selects, for kStruct fields
// only, whether nested recursion goes through the nested type's pure
// ordered decoder (propagating a bail as a bool) or its normal
// try-ordered-then-fallback dispatcher (fallback mode, which never
// "fails" — any error surfaces via l.Err()).
func (g *generator) emitDecode(f fieldInfo, ordered bool) {
	ind := "\t\t\t"
	target := "v." + f.access
	collection := f.kind == kSlice || f.kind == kLeafSlice || f.kind == kMap
	delegate := stdlibMethodContainer(f) || (collection && g.stdlibValue(f, map[string]bool{}) && g.fieldHasReplayCallbacks(f, map[string]bool{}))
	reusedSlice := !delegate && (f.kind == kSlice || f.kind == kLeafSlice) && g.stdlibValue(f, map[string]bool{})
	if delegate || reusedSlice {
		q := g.importAlias("encoding/json", "json")
		if reusedSlice {
			g.p("%sif %s != nil {", ind, target)
		}
		g.p("%sif raw := l.RawValue(); l.Err() == nil {", ind)
		g.p("%s\tif err := %s.Unmarshal(raw, &%s); err != nil { l.AddError(err) }", ind, q, target)
		g.p("%s}", ind)
		if delegate {
			return
		}
		g.p("%s} else {", ind)
		defer g.p("%s}", ind)
	}
	cell := "a.f" + f.cellName
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
	// Slice/map fields decode into the arena's persistent collection cell
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
		// base64 string or numeric byte array → []byte (a fresh allocation
		// from ReadBytesBase64);
		// null is handled by the caller's IsNull check, which clears the
		// field via emitNullSwitch (kByteSlice is a nullable field).
		assign(byteSliceReadExpr(f))
	case kEmptyStruct:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		g.p("%sl.SkipObject()", ind)
	case kCustomLeaf:
		if f.ptr {
			// Keep an existing receiver; clear arena storage only when
			// allocating a new one (including on each pooled decode).
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
			g.emitPointerCellInit(f, target, "a.v"+f.cellName, ind)
			nestedCall(target, "&a.a"+f.cellName)
		} else {
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
		// Reslice-or-append onto the arena's persistent cell instead of
		// appending directly to target: a -pool Decoder's arena survives
		// across decodes, so this reuses the backing array instead of
		// reallocating it every message. Resetting the cell's length to 0
		// up front — keeping its capacity — is also a correctness
		// requirement independent of pooling: re-decoding this field onto a
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
			// Rare (only Marshaler/TextMarshaler elements can be
			// pointers): pool the outer slice-of-pointers only, same
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
		// Reuse the arena's persistent map cell across -pool decodes
		// (clear() empties it without discarding the underlying table);
		// aliased into target exactly like the slice cells above, with
		// the same lifetime contract (invalidated by the next Decode).
		// Seed the cell from an existing NON-NIL map behind target (for a
		// pointer field: the pointer must be non-nil AND point at a
		// non-nil map — a pointer to a nil map must not be adopted as the
		// cell, or the first assignment would panic).
		seedCond := target + " != nil"
		if f.ptr {
			seedCond += " && " + derefTarget + " != nil"
		}
		// A sibling pointer or callback may have replaced the target's map
		// header since this field last appeared. Re-adopt it on every member.
		g.p("%s{", ind)
		g.p("%s\tif %s {", ind, seedCond)
		// The arena's own map handed back (ordered rewind, duplicate
		// member) stays owned; see emitSlicePublication.
		g.p("%s\t\ta.owned%s = a.owned%s && zerojson.SameMap(%s, %s)", ind, f.cellName, f.cellName, derefTarget, cell)
		g.p("%s\t\t%s = %s", ind, cell, derefTarget)
		g.p("%s\t} else if %s == nil || !a.owned%s || a.mapPublished%s {", ind, cell, f.cellName, f.cellName)
		g.p("%s\t\t%s = make(%s)", ind, cell, f.typeStr)
		g.p("%s\t\ta.owned%s = true", ind, f.cellName)
		g.p("%s\t} else {", ind)
		g.p("%s\t\tclear(%s)", ind, cell)
		g.p("%s\t}", ind)
		g.p("%s}", ind)
		g.p("%sa.mapPublished%s = true", ind, f.cellName)
		g.p("%sif l.EnterObject() {", ind)
		g.p("%sfor {", ind)
		if f.intern {
			g.p("%s\tmk := %s(zerojson.Intern(l.ReadKey()))", ind, f.keyTypeStr)
		} else {
			// string() copies the key: ReadKey aliases the input buffer and a
			// map key outlives the decode. Interning (which also copies) is
			// opt-in via zerojson:"intern" — map keys are attacker-controlled
			// and unbounded in cardinality, so it is not done by default.
			g.p("%s\tmk := %s(string(l.ReadKey()))", ind, f.keyTypeStr)
		}
		g.p("%s\tvar mv %s", ind, f.valInfo.typeStr)
		g.emitElemNullOpen(f.valInfo, "mv", ind+"\t")
		switch f.valInfo.kind {
		case kCustomLeaf:
			g.leafRead(f.valInfo.leaf, "&mv", ind+"\t\t")
		case kEmptyStruct:
			// struct{} set-map value: require an object (any contents,
			// ignored) — a number/array/string is rejected, matching
			// encoding/json and easyjson. null already handled by IsNull.
			g.p("%s\t\tl.SkipObject()", ind)
		case kStruct:
			g.p("%s\t\tvar ea %s", ind, arenaName(f.valInfo.elem))
			g.p("%s\t\t%s(l, &mv, &ea)", ind, decodeName(f.valInfo.elem))
		case kMarshaler:
			g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := mv.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
		case kTextMarshaler:
			g.p("%s\t\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := mv.UnmarshalText(txt); err != nil { l.AddError(err) } }", ind)
		case kQuotedInt:
			if !f.valInfo.marshalOnValue {
				g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := mv.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
			} else {
				g.p("%s\t\tmv = %s", ind, g.leafReadExpr(f.valInfo))
			}
		case kInterface:
			g.p("%s\t\tmv = l.ReadInterface()", ind)
		default:
			g.p("%s\t\tmv = %s", ind, g.leafReadExpr(f.valInfo))
		}
		g.p("%s\t}", ind)
		g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
		g.p("%s\t%s[mk] = mv", ind, cell)
		g.p("%s\tif !l.NextMember() { break }", ind)
		g.p("%s}", ind)
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
