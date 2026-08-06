package main

import (
	"fmt"
	"strings"
)

// leafRead emits a decode statement for a custom leaf into ptrExpr.
func (g *generator) leafRead(ls *leafSpec, ptrExpr, ind string) {
	g.p("%s%s.%s(l, %s)", ind, ls.readQual, ls.readFn, ptrExpr)
}

// leafReadExpr returns a decode expression for a leaf-kind value.
func (fi *fieldInfo) leafReadExpr() string {
	switch fi.kind {
	case kString:
		return "l.ReadString()"
	case kEnum:
		return fmt.Sprintf("%s(l.ReadInternedString())", fi.typeStr)
	case kBool:
		return fmt.Sprintf("%s(l.ReadBool())", fi.typeStr)
	case kInt, kQuotedInt, kUint:
		return fmt.Sprintf("%s(%s)", fi.typeStr, fi.intRead)
	case kFloat32, kFloat64:
		return fmt.Sprintf("%s(l.ReadFloat64())", fi.typeStr)
	case kTime:
		return "l.ReadTime()"
	case kUUID:
		return fmt.Sprintf("%s(l.ReadUUIDBytes())", fi.typeStr)
	case kByteSlice:
		return fmt.Sprintf("%s(l.ReadBytesBase64())", fi.typeStr)
	}
	return ""
}

func (g *generator) genArena(ti typeInfo) {
	g.p("")
	g.p("// %s backs every pointer field of a decoded %s with a single", arenaName(ti.name), ti.name)
	g.p("// allocation, and (for -pool Decoders, which reuse an arena across")
	g.p("// decodes instead of allocating one per call) holds a persistent")
	g.p("// cell per slice/map field so repeated decodes reuse their backing")
	g.p("// storage instead of reallocating it every time.")
	g.p("type %s struct {", arenaName(ti.name))
	for _, f := range ti.fields {
		if needsSeenState(f) {
			g.p("\ts%s bool", f.cellName)
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

func needsSeenState(f fieldInfo) bool {
	if f.kind == kMap {
		return true
	}
	if !f.ptr {
		return false
	}
	switch f.kind {
	case kStruct, kMarshaler, kTextMarshaler, kAppender:
		return true
	}
	return false
}

func (g *generator) emitSeenReset(ti typeInfo) {
	for _, f := range ti.fields {
		if needsSeenState(f) {
			g.p("\ta.s%s = false", f.cellName)
		}
	}
}

func (g *generator) emitNestedSeenReset(typeName, arenaExpr, ind string) {
	for _, ti := range g.types {
		if ti.name != typeName {
			continue
		}
		base := strings.TrimPrefix(arenaExpr, "&")
		for _, f := range ti.fields {
			if needsSeenState(f) {
				g.p("%s%s.s%s = false", ind, base, f.cellName)
			}
		}
		return
	}
}

// emitPointerCellInit seeds an arena-backed pointer cell from an existing
// target on its first occurrence in this object. Subsequent duplicate keys
// reuse the same cell, matching encoding/json's merge/receiver semantics.
// With a zero-valued pooled target, the persistent cell is cleared once per
// message so state cannot leak from the previous Decode.
func (g *generator) emitPointerCellInit(f fieldInfo, target, cell, ind string) {
	g.p("%sif !a.s%s {", ind, f.cellName)
	g.p("%s\ta.s%s = true", ind, f.cellName)
	g.p("%s\tif %s != nil {", ind, target)
	g.p("%s\t\t%s = *%s", ind, cell, target)
	g.p("%s\t} else {", ind)
	g.p("%s\t\tvar zjZero %s", ind, f.typeStr)
	g.p("%s\t\t%s = zjZero", ind, cell)
	g.p("%s\t}", ind)
	g.p("%s}", ind)
	g.p("%s%s = &%s", ind, target, cell)
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
	g.p("// UnmarshalZJSON decodes data into v. JSON null clears pointer,")
	g.p("// slice, and map fields and leaves value fields untouched, matching")
	g.p("// encoding/json semantics for reused targets.")
	g.p("func (v *%s) UnmarshalZJSON(data []byte) error {", ti.name)
	g.p("\treturn v.unmarshalZJSON(data, false)")
	g.p("}")
	g.p("")
	g.p("// UnmarshalZJSONBorrow is UnmarshalZJSON with zero-copy string")
	g.p("// decoding: decoded strings may alias data, so data must stay live and")
	g.p("// unmodified for the lifetime of v. Never use it with pooled or reused")
	g.p("// input buffers.")
	g.p("func (v *%s) UnmarshalZJSONBorrow(data []byte) error {", ti.name)
	g.p("\treturn v.unmarshalZJSON(data, true)")
	g.p("}")
	g.p("")
	g.p("func (v *%s) unmarshalZJSON(data []byte, borrow bool) error {", ti.name)
	g.p("\tl := zerojson.NewLexer(data, borrow)")
	g.p("\ta := &%s{}", arenaName(ti.name))
	g.p("\t%s(&l, v, a)", decodeName(ti.name))
	g.p("\tl.Finish()")
	g.p("\treturn l.Err()")
	g.p("}")

	orderedOK := g.ordered && g.canOrderType(ti)
	g.p("")
	g.p("// %s tries the speculative ordered fast path first (see ordered.go),", decodeName(ti.name))
	g.p("// falling back to the general keyed decoder on any deviation from the")
	g.p("// compact, declaration-ordered shape zerojson's own encoder emits.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", decodeName(ti.name), ti.name, arenaName(ti.name))
	g.p("\tif l.IsNull() {")
	g.p("\t\t// A JSON null decodes to a no-op success, leaving v untouched —")
	g.p("\t\t// matching encoding/json's reused-target semantics")
	g.p("\t\t// (json.Unmarshal(\"null\", &v) succeeds without modifying v). A")
	g.p("\t\t// nested struct field never reaches here on null (the enclosing")
	g.p("\t\t// decoder consumes null and clears the field itself before this")
	g.p("\t\t// is called), so this fires only at a top-level entry point;")
	g.p("\t\t// Finish still runs afterward, so trailing bytes (e.g. \"nullx\")")
	g.p("\t\t// are still rejected.")
	g.p("\t\treturn")
	g.p("\t}")
	g.emitSeenReset(ti)
	if orderedOK {
		g.p("\tstart := l.Pos()")
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
	g.p("// ReadKey and dispatched by a string switch. It is always correct and")
	g.p("// is what the ordered fast path falls back to.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", fallbackDecodeName(ti.name), ti.name, arenaName(ti.name))
	g.p("\t_ = a")
	g.p("\tif !l.EnterObject() {")
	g.p("\t\treturn")
	g.p("\t}")
	g.p("\tfor {")
	g.p("\t\tkey := l.ReadKey()")
	g.p("\t\tif l.Err() != nil { return }")
	g.p("\t\tif l.IsNull() {")
	g.emitNullSwitch(ti)
	g.p("\t\t\tif !l.NextMember() { break }")
	g.p("\t\t\tcontinue")
	g.p("\t\t}")
	g.p("\t\tswitch string(key) {")
	for _, f := range ti.fields {
		g.p("\t\tcase %q:", f.jsonName)
		g.emitDecode(f, false)
	}
	g.p("\t\tdefault:")
	g.p("\t\t\tl.Skip()")
	g.p("\t\t}")
	g.p("\t\tif l.Err() != nil { return }")
	g.p("\t\tif !l.NextMember() { break }")
	g.p("\t}")
	g.p("}")
}

// isNullableField reports whether a JSON null clears f (pointers, slices,
// maps, interfaces, and slice- or map-shaped Marshalers go nil; other
// value-typed fields are left untouched by both the fallback and ordered
// decoders).
func isNullableField(f fieldInfo) bool {
	return f.ptr || f.kind == kSlice || f.kind == kLeafSlice || f.kind == kMap || f.kind == kByteSlice || f.kind == kInterface || (f.kind == kMarshaler && (f.marshalerIsSlice || f.marshalerIsMap))
}

// emitNullSwitch clears reference-typed fields on JSON null, matching
// encoding/json: pointers and slices go nil; value fields are untouched.
func (g *generator) emitNullSwitch(ti typeInfo) {
	var nullable []fieldInfo
	for _, f := range ti.fields {
		if isNullableField(f) {
			nullable = append(nullable, f)
		}
	}
	if len(nullable) == 0 {
		return
	}
	g.p("\t\t\tswitch string(key) {")
	for _, f := range nullable {
		g.p("\t\t\tcase %q:", f.jsonName)
		g.p("\t\t\t\tv.%s = nil", f.access)
		if needsSeenState(f) {
			g.p("\t\t\t\ta.s%s = false", f.cellName)
		}
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
	cell := "a.f" + f.cellName
	// zjDecodeOrderedField<T> returns bool; the fallback decoder returns
	// nothing. A mid-loop parse error (kSlice/kLeafSlice/kMap elements)
	// exits the same way every other bail does here.
	retStmt := "return"
	if ordered {
		retStmt = "return false"
	}

	// Pointer fields decode into their arena cell.
	assign := func(readExpr string) {
		if f.ptr {
			g.p("%s%s = %s", ind, cell, readExpr)
			g.p("%s%s = &%s", ind, target, cell)
		} else {
			g.p("%s%s = %s", ind, target, readExpr)
		}
	}

	switch f.kind {
	case kString:
		if f.intern {
			assign("l.ReadInternedString()")
		} else {
			assign("l.ReadString()")
		}
	case kEnum:
		g.p("%sswitch b := l.ReadEnumBytes(); string(b) {", ind)
		for _, c := range f.enumConsts {
			g.p("%scase %q:", ind, c.val)
			if f.ptr {
				g.p("%s\t%s = %s", ind, cell, c.name)
			} else {
				g.p("%s\t%s = %s", ind, target, c.name)
			}
		}
		g.p("%sdefault:", ind)
		unknown := "string(b)"
		if f.intern {
			unknown = "zerojson.Intern(b)"
		}
		if f.ptr {
			g.p("%s\t%s = %s(%s)", ind, cell, f.typeStr, unknown)
		} else {
			g.p("%s\t%s = %s(%s)", ind, target, f.typeStr, unknown)
		}
		g.p("%s}", ind)
		if f.ptr {
			g.p("%s%s = &%s", ind, target, cell)
		}
	case kBool:
		assign("l.ReadBool()")
	case kQuotedInt, kInt, kUint:
		read := f.intRead
		if f.stringOpt {
			read = strings.Replace(read, "l.Read", "l.ReadQuoted", 1)
		}
		assign(fmt.Sprintf("%s(%s)", f.typeStr, read))
	case kFloat32, kFloat64:
		assign(fmt.Sprintf("%s(l.ReadFloat64())", f.typeStr))
	case kTime:
		assign("l.ReadTime()")
	case kUUID:
		assign(fmt.Sprintf("%s(l.ReadUUIDBytes())", f.typeStr))
	case kInterface:
		assign("l.ReadInterface()")
	case kByteSlice:
		// base64 string or numeric byte array → []byte (a fresh allocation
		// from ReadBytesBase64);
		// null is handled by the caller's IsNull check, which clears the
		// field via emitNullSwitch (kByteSlice is a nullable field).
		assign(fmt.Sprintf("%s(l.ReadBytesBase64())", f.typeStr))
	case kCustomLeaf:
		if f.ptr {
			g.p("%s%s = &%s", ind, target, cell)
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
				g.emitNestedSeenReset(f.elem, arenaExpr, ind)
				// Direct call to the nested type's pure ordered decoder:
				// its bail propagates as ours, so the whole top-level
				// object restarts on ITS fallback (see ordered.go).
				g.p("%sif !%s(l, %s, %s) { return false }", ind, orderedDecodeName(f.elem), targetExpr, arenaExpr)
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
		g.p("%sif err := %s.UnmarshalJSON(l.RawValue()); err != nil {", ind, target)
		g.p("%s\tl.AddError(err)", ind)
		g.p("%s}", ind)
	case kTextMarshaler:
		if f.ptr {
			g.emitPointerCellInit(f, target, cell, ind)
		}
		g.p("%sif err := %s.UnmarshalText(l.ReadTextBytes()); err != nil {", ind, target)
		g.p("%s\tl.AddError(err)", ind)
		g.p("%s}", ind)
	case kSlice:
		cell := "a.c" + f.cellName
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
		g.p("%s%s = %s", ind, target, cell)
	case kLeafSlice:
		cell := "a.c" + f.cellName
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
				g.p("%s\t\tif err := e.UnmarshalJSON(l.RawValue()); err != nil { l.AddError(err) }", ind)
			case kTextMarshaler:
				g.p("%s\t\tif err := e.UnmarshalText(l.ReadTextBytes()); err != nil { l.AddError(err) }", ind)
			}
			g.p("%s\t}", ind)
			g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
			g.p("%s\t%s = append(%s, e)", ind, cell, cell)
			g.p("%s\tif !l.NextElem() { break }", ind)
			g.p("%s}", ind)
			g.p("%s}", ind)
			g.emitNonNilEmptySlice(cell, sliceCellType(f.valInfo.typeStr, f.elemPtr), ind)
			g.p("%s%s = %s", ind, target, cell)
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
		g.p("%s\tif !l.IsNull() {", ind)
		switch f.valInfo.kind {
		case kCustomLeaf:
			g.leafRead(f.valInfo.leaf, fmt.Sprintf("&%s[idx]", cell), ind+"\t\t")
		case kMarshaler:
			g.p("%s\t\tif err := %s[idx].UnmarshalJSON(l.RawValue()); err != nil { l.AddError(err) }", ind, cell)
		case kTextMarshaler:
			g.p("%s\t\tif err := %s[idx].UnmarshalText(l.ReadTextBytes()); err != nil { l.AddError(err) }", ind, cell)
		case kInterface:
			g.p("%s\t\t%s[idx] = l.ReadInterface()", ind, cell)
		default:
			g.p("%s\t\t%s[idx] = %s", ind, cell, f.valInfo.leafReadExpr())
		}
		g.p("%s\t}", ind)
		g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
		g.p("%s\tif !l.NextElem() { break }", ind)
		g.p("%s}", ind)
		g.p("%s}", ind)
		g.emitNonNilEmptySlice(cell, sliceCellType(f.valInfo.typeStr, f.elemPtr), ind)
		g.p("%s%s = %s", ind, target, cell)
	case kMap:
		cell := "a.m" + f.cellName
		// Reuse the arena's persistent map cell across -pool decodes
		// (clear() empties it without discarding the underlying table);
		// aliased into target exactly like the slice cells above, with
		// the same lifetime contract (invalidated by the next Decode).
		g.p("%sif !a.s%s {", ind, f.cellName)
		g.p("%s\ta.s%s = true", ind, f.cellName)
		g.p("%s\tif %s != nil {", ind, target)
		g.p("%s\t\t%s = %s", ind, cell, target)
		g.p("%s\t} else if %s == nil {", ind, cell)
		g.p("%s\t\t%s = make(%s)", ind, cell, f.typeStr)
		g.p("%s\t} else {", ind)
		g.p("%s\t\tclear(%s)", ind, cell)
		g.p("%s\t}", ind)
		g.p("%s}", ind)
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
		g.p("%s\tif !l.IsNull() {", ind)
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
			g.p("%s\t\tif err := mv.UnmarshalJSON(l.RawValue()); err != nil { l.AddError(err) }", ind)
		case kTextMarshaler:
			g.p("%s\t\tif err := mv.UnmarshalText(l.ReadTextBytes()); err != nil { l.AddError(err) }", ind)
		case kQuotedInt:
			if !f.valInfo.marshalOnValue {
				g.p("%s\t\tif err := mv.UnmarshalJSON(l.RawValue()); err != nil { l.AddError(err) }", ind)
			} else {
				g.p("%s\t\tmv = %s", ind, f.valInfo.leafReadExpr())
			}
		case kInterface:
			g.p("%s\t\tmv = l.ReadInterface()", ind)
		default:
			g.p("%s\t\tmv = %s", ind, f.valInfo.leafReadExpr())
		}
		g.p("%s\t}", ind)
		g.p("%s\tif l.Err() != nil { %s }", ind, retStmt)
		g.p("%s\t%s[mk] = mv", ind, cell)
		g.p("%s\tif !l.NextMember() { break }", ind)
		g.p("%s}", ind)
		g.p("%s}", ind)
		g.p("%s%s = %s", ind, target, cell)
	}
}
