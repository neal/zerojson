package main

import "fmt"

// root.go emits codecs for named slice/map ROOT types — a top-level shape
// easyjson supports:
//
//	type EventList []Event      // []T and []*T
//	type LegIndex  map[string]EventLeg
//
// A root's element (slice) or value (map) is classified exactly as a
// slice/map FIELD would be (collectRoot in load.go), so it reuses the same
// element/value codecs — element structs keep their ordered fast path. The
// root loop itself is the plain array/object protocol: nil encodes as
// null; a non-nil empty root as [] (slice) or {} (map); JSON null decodes
// to nil. Empty arrays clear slices; empty objects preserve existing map
// entries. These are the same rules as slice/map fields. Roots have no
// ordered fast path of their own (only their element structs do), so no
// zjDecodeFallback/zjDecodeOrdered is emitted for them.

// genRootArena emits per-call working storage for a slice/map root.
func (g *generator) genRootArena(ti typeInfo) {
	n := ti.name
	g.p("")
	g.p("// %s holds working storage for one %s decode.", arenaName(n), n)
	g.p("type %s struct {", arenaName(n))
	// A map root decodes straight into *v through its zjReadMap helper.
	if ti.rootField.kind != kMap {
		g.p("\tcRoot %s", n)
	}
	g.p("}")
}

// genRootEncode emits AppendZJSON for a slice/map root.
func (g *generator) genRootEncode(ti typeInfo) {
	n := ti.name
	rf := ti.rootField
	empty := "[]"
	if rf.kind == kMap {
		empty = "{}"
	}
	g.p("")
	g.p("// AppendZJSON appends the JSON encoding of v to dst: a nil %s as null,", n)
	g.p("// a non-nil empty one as %s. On error the returned buffer holds a", empty)
	g.p("// partial encoding and must be discarded.")
	g.p("func (v *%s) AppendZJSON(dst []byte) ([]byte, error) {", n)
	g.p("\tif *v == nil {")
	g.p("\t\treturn append(dst, \"null\"...), nil")
	g.p("\t}")
	if rf.kind == kMap {
		// Range order, matching easyjson (encoding/json would sort keys).
		g.p("\tdst = append(dst, '{')")
		g.p("\tfirst := true")
		g.p("\tfor mk, mv := range *v {")
		g.p("\t\tif !first { dst = append(dst, ',') }")
		g.p("\t\tfirst = false")
		g.p("\t\tdst = zerojson.AppendString(dst, string(mk))")
		g.p("\t\tdst = append(dst, ':')")
		g.emitMapValueAppend(rf.valInfo, "mv", "\t\t")
		g.p("\t}")
		g.p("\treturn append(dst, '}'), nil")
	} else {
		g.p("\tdst = append(dst, '[')")
		g.p("\tfor i := range *v {")
		g.p("\t\tif i > 0 { dst = append(dst, ',') }")
		if rf.elemPtr {
			g.p("\t\tif (*v)[i] == nil {")
			g.p("\t\t\tdst = append(dst, \"null\"...)")
			g.p("\t\t\tcontinue")
			g.p("\t\t}")
		}
		g.emitElemAppend(rf, "(*v)[i]", "\t\t")
		g.p("\t}")
		g.p("\treturn append(dst, ']'), nil")
	}
	g.p("}")
}

// emitElemAppend emits the encode statement(s) for one slice-root element
// at expr (for []*T, the caller has already null-checked and expr is the
// non-nil pointer), mirroring the slice-FIELD element handling in encode.go.
func (g *generator) emitElemAppend(rf *fieldInfo, expr, ind string) {
	if rf.kind == kSlice {
		g.p("%svar err error", ind)
		g.p("%sdst, err = %s.AppendZJSON(dst)", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
		return
	}
	vi := rf.valInfo
	switch vi.kind {
	case kMarshaler:
		g.p("%sraw, err := %s.MarshalJSON()", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
		g.p("%sif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)
		g.p("%sdst = append(dst, raw...)", ind)
	case kTextMarshaler:
		g.p("%sraw, err := %s.MarshalText()", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
		g.p("%sdst = zerojson.AppendStringBytes(dst, raw)", ind)
	case kCustomLeaf:
		g.leafAppend(vi.leaf, "&"+expr, ind)
	case kInterface:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	default:
		g.emitLeafAppend(vi, expr, ind)
	}
}

// emitMapValueAppend emits the encode statement(s) for one map-root value
// at expr, mirroring the map-FIELD value handling in encode.go.
func (g *generator) emitMapValueAppend(vi *fieldInfo, expr, ind string) {
	switch vi.kind {
	case kStruct:
		g.emitValueStructAppend(vi.elem, expr, ind)
	case kMarshaler:
		if vi.marshalOnValue {
			g.p("%sraw, err := %s.MarshalJSON()", ind, expr)
			g.p("%sif err != nil { return dst, err }", ind)
			g.p("%sif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)
			g.p("%sdst = append(dst, raw...)", ind)
		} else {
			g.p("%svar err error", ind)
			g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, expr)
			g.p("%sif err != nil { return dst, err }", ind)
		}
	case kTextMarshaler:
		if vi.textOnValue {
			g.p("%sraw, err := %s.MarshalText()", ind, expr)
			g.p("%sif err != nil { return dst, err }", ind)
			g.p("%sdst = zerojson.AppendStringBytes(dst, raw)", ind)
		} else {
			g.p("%svar err error", ind)
			g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, expr)
			g.p("%sif err != nil { return dst, err }", ind)
		}
	case kQuotedInt:
		if vi.marshalOnValue {
			g.emitLeafAppend(vi, expr, ind)
		} else {
			g.p("%svar err error", ind)
			g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, expr)
			g.p("%sif err != nil { return dst, err }", ind)
		}
	case kInterface:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	default:
		g.emitLeafAppend(vi, expr, ind)
	}
}

// genRootDecode emits UnmarshalZJSON/Borrow wrappers and zjDecode_<T> for a
// slice/map root. There is a single decode path (no ordered/fallback split
// at the root level; element structs keep their own ordered fast path).
func (g *generator) genRootDecode(ti typeInfo) {
	n := ti.name
	rf := ti.rootField
	empty := "[]"
	if rf.kind == kMap {
		empty = "{}"
	}
	g.p("")
	g.p("// UnmarshalZJSON decodes data into v. JSON null sets v to nil; %s decodes", empty)
	g.p("// to a non-nil container; reused maps retain keys absent from the input.")
	g.p("func (v *%s) UnmarshalZJSON(data []byte) error {", n)
	g.p("\treturn v.unmarshalZJSON(data, false)")
	g.p("}")
	g.p("")
	g.p("// UnmarshalZJSONBorrow is UnmarshalZJSON with zero-copy string decoding:")
	g.p("// decoded strings and Raw values may alias data, which must stay")
	g.p("// live and unmodified for the result's lifetime.")
	g.p("func (v *%s) UnmarshalZJSONBorrow(data []byte) error {", n)
	g.p("\treturn v.unmarshalZJSON(data, true)")
	g.p("}")
	g.p("")
	g.p("func (v *%s) unmarshalZJSON(data []byte, borrow bool) error {", n)
	g.p("\tif err := zerojson.Valid(data); err != nil { return err }")
	g.emitDecodeBody(n, "borrow")
	g.p("}")
	g.emitTrustedDecode(n)
	g.genReadZJSON(n)

	g.p("")
	g.p("// %s decodes a JSON array/object root directly into v. Element (slice)", decodeName(n))
	g.p("// and value (map) codecs are the same ones a slice/map field uses, so")
	g.p("// element structs keep their ordered fast path; the root loop itself is")
	g.p("// the plain array/object protocol.")
	g.p("func %s(l *zerojson.Lexer, v *%s, a *%s) {", decodeName(n), n, arenaName(n))
	delegate := stdlibMethodContainer(*rf) || (g.stdlibValue(*rf, map[string]bool{}) && g.fieldHasReplayCallbacks(*rf, map[string]bool{}))
	reusedSlice := !delegate && rf.kind != kMap && g.stdlibValue(*rf, map[string]bool{})
	if delegate || reusedSlice {
		if reusedSlice {
			g.p("\tif *v != nil {")
		}
		if reusedSlice && scalarSlice(*rf) {
			g.emitReusedScalarSlice(*rf, "*v", "\t")
		} else {
			q := g.importAlias("encoding/json", "json")
			// A methodless local view prevents recursion through generated
			// compatibility UnmarshalJSON wrappers on the container itself.
			g.p("\ttype container %s", n)
			g.p("\tif raw := l.RawValue(); l.Err() == nil {")
			g.p("\t\tif err := %s.Unmarshal(raw, (*container)(v)); err != nil { l.AddError(err) }", q)
			g.p("\t}")
		}
		if reusedSlice {
			g.p("\treturn\n\t}")
		}
	}
	if !delegate && rf.kind == kMap {
		g.emitRootMapDecode(ti)
	} else if !delegate {
		g.emitRootSliceDecode(ti)
	}
	g.p("}")
}

func (g *generator) emitRootSliceDecode(ti typeInfo) {
	n := ti.name
	rf := ti.rootField
	ind := "\t"
	cell := "a.cRoot"
	g.p("%sif l.IsNull() {", ind)
	g.p("%s\t*v = nil", ind)
	g.p("%s\treturn", ind)
	g.p("%s}", ind)
	if rf.byteElem != "" {
		g.p("%sif l.IsDelim('\"') {", ind)
		g.p("%s\t*v = %s", ind, byteSliceReadExpr(*rf))
		g.p("%s\treturn", ind)
		g.p("%s}", ind)
	}
	// Root decoding enters once per public call. Non-pooled callers retain
	// their existing slice aliases; pooled wrappers zero *v before entry,
	// so they start with fresh storage.
	g.p("%s%s = *v", ind, cell)
	g.p("%s%s = %s[:0]", ind, cell, cell)
	g.p("%sif l.EnterArray() {", ind)
	g.p("%sfor {", ind)
	if rf.kind == kSlice {
		g.emitStructSliceElem(rf.elem, rf.elemPtr, g.hasLeafOverride(*rf, map[string]bool{}), cell, ind+"\t")
		g.p("%s\tif l.Err() != nil { return }", ind)
		g.p("%s\tif !l.NextElem() { break }", ind)
	} else {
		g.emitRootLeafSliceBody(rf, cell, ind)
	}
	g.p("%s}", ind)
	g.p("%s}", ind)
	g.emitNonNilEmptySlice(cell, n, ind)
	g.p("%s*v = %s", ind, cell)
}

// emitRootLeafSliceBody emits one iteration of a leaf-element slice root's
// decode loop, mirroring the kLeafSlice field body in decode.go.
func (g *generator) emitRootLeafSliceBody(rf *fieldInfo, cell, ind string) {
	vi := rf.valInfo
	if rf.elemPtr {
		// Only Marshaler/TextMarshaler/Appender elements can be pointers.
		g.emitReusedPointerElem(cell, vi.typeStr, ind+"\t")
		switch vi.kind {
		case kMarshaler:
			g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := e.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
		case kTextMarshaler:
			g.p("%s\t\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := e.UnmarshalText(txt); err != nil { l.AddError(err) } }", ind)
		case kAppender:
			g.p("%s\t\te.ReadZJSON(l)", ind)
		}
		g.p("%s\t}", ind)
		g.p("%s\tif l.Err() != nil { return }", ind)
		g.p("%s\t%s = append(%s, e)", ind, cell, cell)
		g.p("%s\tif !l.NextElem() { break }", ind)
		return
	}
	g.emitGrowLeafSlice(cell, vi, ind+"\t")
	g.p("%s\tidx := len(%s) - 1", ind, cell)
	g.emitElemNullOpen(vi, fmt.Sprintf("%s[idx]", cell), ind+"\t")
	switch vi.kind {
	case kCustomLeaf:
		// The leaf read func takes *T; a slice element cell[idx] is a T, so
		// pass its address (matching the kLeafSlice field emit in decode.go).
		g.leafRead(vi.leaf, "&"+cell+"[idx]", ind+"\t\t")
	case kMarshaler:
		g.p("%s\t\tif raw := l.RawValue(); l.Err() == nil { if err := %s[idx].UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind, cell)
	case kTextMarshaler:
		g.p("%s\t\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := %s[idx].UnmarshalText(txt); err != nil { l.AddError(err) } }", ind, cell)
	case kAppender:
		g.p("%s\t\t%s[idx].ReadZJSON(l)", ind, cell)
	case kInterface:
		g.p("%s\t\t%s[idx] = l.ReadInterface()", ind, cell)
	default:
		g.p("%s\t\t%s[idx] = %s", ind, cell, g.leafReadExpr(vi))
	}
	g.p("%s\t}", ind)
	g.p("%s\tif l.Err() != nil { return }", ind)
	g.p("%s\tif !l.NextElem() { break }", ind)
}

func (g *generator) emitRootMapDecode(ti typeInfo) {
	n := ti.name
	rf := ti.rootField
	ind := "\t"
	g.p("%sif l.IsNull() {", ind)
	g.p("%s\t*v = nil", ind)
	g.p("%s\treturn", ind)
	g.p("%s}", ind)
	// A named map root has no struct-tag site to request key interning, so
	// keys are copied (string() — ReadKey aliases the buffer), never interned.
	g.p("%s*v = %s(l, *v)", ind, g.mapReadCall(n, rf.keyTypeStr, rf.valInfo, false, rf.mapEntrySize))
}
