package main

import (
	"fmt"
	"strings"
)

// leafAppend emits an encode statement for a custom leaf value at ptrExpr
// (which must be a pointer to the value).
func (g *generator) leafAppend(ls *leafSpec, ptrExpr, ind string) {
	if ls.appendPkg == runtimePath && ls.appendFn == "AppendRaw" {
		// zerojson.Raw is validated on encode like json.RawMessage: a
		// user-constructed Raw that is not valid JSON is an error, never
		// silently invalid output.
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendRawChecked(dst, %s)", ind, ptrExpr)
		g.p("%sif err != nil { return dst, err }", ind)
		return
	}
	g.p("%sdst = %s%s(dst, %s)", ind, qualDot(ls.appendQual), ls.appendFn, ptrExpr)
}

// qualDot renders a package qualifier for a call: "pkg." or, for a
// codec living in the package being generated, nothing.
func qualDot(q string) string {
	if q == "" {
		return ""
	}
	return q + "."
}

func (g *generator) genEncode(ti typeInfo) {
	g.p("")
	g.p("// AppendZJSON appends the JSON encoding of v to dst. On error the")
	g.p("// returned buffer contains a partial encoding and must be discarded.")
	g.p("func (v *%s) AppendZJSON(dst []byte) ([]byte, error) {", ti.name)
	g.p("\tdst = append(dst, '{')")
	g.p("\tn := len(dst)")
	g.p("\t_ = n")

	for _, f := range ti.fields {
		gate := g.encodeGate(f)
		if gate != "" {
			g.p("\tif %s {", gate)
		} else {
			g.p("\t{")
		}
		g.p("\t\tif len(dst) > n { dst = append(dst, ',') }")
		if keyLiteralSafe(f.jsonName) {
			g.p("\t\tdst = append(dst, `%q:`...)", f.jsonName)
		} else {
			// A name that needs JSON escaping (an HTML-sensitive byte,
			// a control char, non-ASCII) goes through the same
			// AppendString the values use, so the key is escaped exactly
			// as encoding/json and easyjson escape it — rather than as a
			// Go-quoted literal, which is not JSON escaping.
			g.p("\t\tdst = zerojson.AppendString(dst, %q)", f.jsonName)
			g.p("\t\tdst = append(dst, ':')")
		}
		if f.ptr && !f.omitempty && !f.omitzero {
			// Non-omitempty, non-omitzero pointer: key always present,
			// null when nil.
			g.p("\t\tif v.%s == nil {", f.access)
			g.p("\t\t\tdst = append(dst, \"null\"...)")
			g.p("\t\t} else {")
			g.emitValue(f, "\t\t\t")
			g.p("\t\t}")
		} else {
			g.emitValue(f, "\t\t")
		}
		g.p("\t}")
	}
	g.p("\treturn append(dst, '}'), nil")
	g.p("}")
}

// keyLiteralSafe reports whether a JSON field name can be emitted as the
// raw-string literal `"name":` — every byte must be one AppendString would
// pass through verbatim (jsonNameNeedsEscape), and it must not contain a
// backtick, which would terminate the Go raw string the literal lives in.
func keyLiteralSafe(name string) bool {
	return !jsonNameNeedsEscape(name) && !strings.Contains(name, "`")
}

// encodeGate returns the emit condition, or "" for always-emit. omitempty
// and omitzero are independent gates (both may be set on one field, in
// which case both conditions must hold to emit); each contributes its own
// condition via emptyGate/zeroGate and they're ANDed together.
func (g *generator) encodeGate(f fieldInfo) string {
	if f.ptr {
		if f.omitzero && f.hasIsZero {
			return fmt.Sprintf("v.%s != nil && !v.%s.IsZero()", f.access, f.access)
		}
		if f.omitempty || f.omitzero {
			return fmt.Sprintf("v.%s != nil", f.access)
		}
		return ""
	}
	var conds []string
	if f.omitempty {
		if c := g.emptyGate(f); c != "" {
			conds = append(conds, c)
		}
	}
	if f.omitzero {
		// For a scalar both options reduce to the same test; emitting it
		// twice (`v.X != 0 && v.X != 0`) is flagged by go vet's bools
		// check, which go test runs by default.
		if c := g.zeroGate(f); c != "" && (len(conds) == 0 || conds[0] != c) {
			conds = append(conds, c)
		}
	}
	return strings.Join(conds, " && ")
}

// emptyGate returns omitempty's emit condition for f, or "" if omitempty
// cannot gate this kind (struct-shaped values whose emptiness can't be
// checked structurally — matching easyjson/encoding/json v1, which have
// the same limitation for omitempty specifically).
func (g *generator) emptyGate(f fieldInfo) string {
	switch f.kind {
	case kString, kEnum, kJSONNumber:
		return fmt.Sprintf("v.%s != \"\"", f.access)
	case kBool:
		return fmt.Sprintf("v.%s", f.access)
	case kQuotedInt, kInt, kUint, kFloat32, kFloat64:
		return fmt.Sprintf("v.%s != 0", f.access)
	case kSlice, kLeafSlice, kMap, kByteSlice:
		return fmt.Sprintf("len(v.%s) != 0", f.access)
	case kInterface:
		return fmt.Sprintf("v.%s != nil", f.access)
	case kMarshaler:
		if f.marshalerIsSlice || f.marshalerIsMap {
			return fmt.Sprintf("len(v.%s) != 0", f.access)
		}
		return scalarEmptyGate(f) // scalar-underlying: gate on zero; struct: always emit
	case kTextMarshaler, kAppender, kCustomLeaf:
		return scalarEmptyGate(f)
	default:
		return "" // time.Time / uuid / decimal / struct values: always emit
	}
}

// scalarEmptyGate returns omitempty's emit condition for a Marshaler/
// Appender/TextMarshaler/CustomLeaf value field whose underlying type is a
// scalar (numeric/string/bool) or a slice/map — matching encoding/json's
// isEmptyValue, which inspects the underlying kind, so a zero-valued
// scalar custom type is omitted (e.g. a quoted-money Cents(0)) and an
// empty slice-shaped one (zerojson.Raw, a []string-backed TextMarshaler)
// is omitted too, while a struct-underlying one (decimal.Decimal, a
// struct-shaped Marshaler) has no emptiness and is always emitted.
func scalarEmptyGate(f fieldInfo) string {
	switch f.scalarEmpty {
	case "num":
		return fmt.Sprintf("v.%s != 0", f.access)
	case "str":
		return fmt.Sprintf("v.%s != \"\"", f.access)
	case "bool":
		return fmt.Sprintf("bool(v.%s)", f.access)
	case "len", "array":
		return fmt.Sprintf("len(v.%s) != 0", f.access)
	}
	return ""
}

// scalarZeroGate is scalarEmptyGate's omitzero counterpart for a custom
// type WITHOUT an IsZero method: Go 1.24 stdlib semantics compare against
// the zero value of the underlying kind, which for a slice/map is nil
// (not len 0 — the omitempty/omitzero distinction).
func scalarZeroGate(f fieldInfo) string {
	if f.scalarEmpty == "array" {
		// Without an IsZero method array omitzero remains unsupported;
		// emptiness (length) is not the same as element-wise zeroness.
		return ""
	}
	if f.scalarEmpty == "len" {
		return fmt.Sprintf("v.%s != nil", f.access)
	}
	return scalarEmptyGate(f)
}

// zeroGate returns omitzero's emit condition for f (Go 1.24 stdlib
// semantics): nil for pointer-shaped reference kinds (note this differs
// from emptyGate for slices/maps, which checks len==0 — a non-nil empty
// slice is "empty" but not "zero"), and an IsZero() bool call for kinds
// where one is required by validateFieldOptions (time.Time,
// custom domain types, and any user type with the method). uuid.UUID
// has no IsZero method but is a comparable array type, so it falls back
// to a zero-value comparison instead of erroring.
func (g *generator) zeroGate(f fieldInfo) string {
	if f.hasIsZero {
		return fmt.Sprintf("!v.%s.IsZero()", f.access)
	}
	switch f.kind {
	case kString, kEnum, kJSONNumber:
		return fmt.Sprintf("v.%s != \"\"", f.access)
	case kBool:
		return fmt.Sprintf("v.%s", f.access)
	case kQuotedInt, kInt, kUint, kFloat32, kFloat64:
		return fmt.Sprintf("v.%s != 0", f.access)
	case kSlice, kLeafSlice, kMap, kByteSlice, kInterface:
		return fmt.Sprintf("v.%s != nil", f.access)
	case kMarshaler:
		if f.marshalerIsSlice || f.marshalerIsMap {
			return fmt.Sprintf("v.%s != nil", f.access)
		}
		if c := scalarZeroGate(f); c != "" {
			return c
		}
		return fmt.Sprintf("!v.%s.IsZero()", f.access)
	case kCustomLeaf, kTextMarshaler, kAppender:
		if c := scalarZeroGate(f); c != "" {
			return c
		}
		return fmt.Sprintf("!v.%s.IsZero()", f.access)
	case kTime, kStruct:
		return fmt.Sprintf("!v.%s.IsZero()", f.access)
	case kUUID:
		if f.hasIsZero {
			return fmt.Sprintf("!v.%s.IsZero()", f.access)
		}
		return fmt.Sprintf("v.%s != (%s{})", f.access, f.typeStr)
	default:
		return ""
	}
}

// emitValue writes the value-encoding statement.
func (g *generator) emitValue(f fieldInfo, ind string) {
	deref := func(expr string) string {
		if f.ptr {
			return "*" + expr
		}
		return expr
	}
	e := "v." + f.access
	// ce is the collection value for slice/map kinds: the field itself,
	// or, for a pointer-to-slice/map field, the pointee (parenthesized so
	// indexing and ranging apply to the collection, not the pointer).
	ce := e
	if f.ptr {
		ce = "(*" + e + ")"
	}
	switch f.kind {
	case kString, kEnum:
		g.p("%sdst = zerojson.AppendString(dst, string(%s))", ind, deref(e))
	case kJSONNumber:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendJSONNumber(dst, string(%s))", ind, deref(e))
		g.p("%sif err != nil { return dst, err }", ind)
	case kBool:
		g.p("%sdst = zerojson.AppendBool(dst, bool(%s))", ind, deref(e))
	case kQuotedInt:
		g.p("%sdst = zerojson.AppendQuotedInt64(dst, int64(%s))", ind, deref(e))
	case kInt:
		if f.stringOpt {
			g.p("%sdst = zerojson.AppendQuotedInt64(dst, int64(%s))", ind, deref(e))
		} else {
			g.p("%sdst = zerojson.AppendInt64(dst, int64(%s))", ind, deref(e))
		}
	case kUint:
		if f.stringOpt {
			g.p("%sdst = zerojson.AppendQuotedUint64(dst, uint64(%s))", ind, deref(e))
		} else {
			g.p("%sdst = zerojson.AppendUint64(dst, uint64(%s))", ind, deref(e))
		}
	case kFloat32:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendFloat32(dst, float32(%s))", ind, deref(e))
		g.p("%sif err != nil { return dst, err }", ind)
	case kFloat64:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendFloat64(dst, float64(%s))", ind, deref(e))
		g.p("%sif err != nil { return dst, err }", ind)
	case kTime:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendTime(dst, %s)", ind, deref(e))
		g.p("%sif err != nil { return dst, err }", ind)
	case kUUID:
		g.p("%sdst = zerojson.AppendUUIDBytes(dst, [16]byte(%s))", ind, deref(e))
	case kCustomLeaf:
		if f.ptr {
			g.leafAppend(f.leaf, e, ind)
		} else {
			g.leafAppend(f.leaf, "&"+e, ind)
		}
	case kStruct, kAppender:
		g.p("%svar err error", ind)
		g.p("%sdst, err = %s.AppendZJSON(dst)", ind, e)
		g.p("%sif err != nil { return dst, err }", ind)
	case kMarshaler:
		g.p("%sraw, err := %s.MarshalJSON()", ind, e)
		g.p("%sif err != nil { return dst, err }", ind)
		g.p("%sif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)
		g.p("%sdst = append(dst, raw...)", ind)
	case kTextMarshaler:
		g.p("%sraw, err := %s.MarshalText()", ind, e)
		g.p("%sif err != nil { return dst, err }", ind)
		g.p("%sdst = zerojson.AppendStringBytes(dst, raw)", ind)
	case kInterface:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendInterface(dst, %s)", ind, deref(e))
		g.p("%sif err != nil { return dst, err }", ind)
	case kByteSlice:
		// []byte / named byte slice: base64 string, or null when nil
		// (AppendBytesBase64 handles the nil→null case itself), matching
		// encoding/json and easyjson.
		g.p("%sdst = zerojson.AppendBytesBase64(dst, %s)", ind, byteSliceEncodeExpr(f, deref(e)))
	case kEmptyStruct:
		g.p("%sdst = append(dst, \"{}\"...)", ind)
	case kSlice:
		// A nil (non-omitempty, non-omitzero) slice encodes as null, a
		// non-nil one — including an empty non-nil slice — as an array,
		// matching encoding/json and easyjson. (omitempty/omitzero fields
		// never reach here nil: their gate already skipped the field.)
		g.p("%sif %s == nil {", ind, ce)
		g.p("%s\tdst = append(dst, \"null\"...)", ind)
		g.p("%s} else {", ind)
		g.p("%s\tdst = append(dst, '[')", ind)
		g.p("%s\tfor i := range %s {", ind, ce)
		g.p("%s\t\tif i > 0 { dst = append(dst, ',') }", ind)
		if f.elemPtr {
			g.p("%s\t\tif %s[i] == nil {", ind, ce)
			g.p("%s\t\t\tdst = append(dst, \"null\"...)", ind)
			g.p("%s\t\t\tcontinue", ind)
			g.p("%s\t\t}", ind)
		}
		g.p("%s\t\tvar err error", ind)
		g.p("%s\t\tdst, err = %s[i].AppendZJSON(dst)", ind, ce)
		g.p("%s\t\tif err != nil { return dst, err }", ind)
		g.p("%s\t}", ind)
		g.p("%s\tdst = append(dst, ']')", ind)
		g.p("%s}", ind)
	case kLeafSlice:
		g.p("%sif %s == nil {", ind, ce)
		g.p("%s\tdst = append(dst, \"null\"...)", ind)
		g.p("%s} else {", ind)
		g.p("%s\tdst = append(dst, '[')", ind)
		g.p("%s\tfor i := range %s {", ind, ce)
		g.p("%s\t\tif i > 0 { dst = append(dst, ',') }", ind)
		if f.elemPtr {
			g.p("%s\t\tif %s[i] == nil {", ind, ce)
			g.p("%s\t\t\tdst = append(dst, \"null\"...)", ind)
			g.p("%s\t\t\tcontinue", ind)
			g.p("%s\t\t}", ind)
		}
		switch f.valInfo.kind {
		case kMarshaler:
			g.p("%s\t\traw, err := %s[i].MarshalJSON()", ind, ce)
			g.p("%s\t\tif err != nil { return dst, err }", ind)
			g.p("%s\t\tif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)
			g.p("%s\t\tdst = append(dst, raw...)", ind)
		case kTextMarshaler:
			g.p("%s\t\traw, err := %s[i].MarshalText()", ind, ce)
			g.p("%s\t\tif err != nil { return dst, err }", ind)
			g.p("%s\t\tdst = zerojson.AppendStringBytes(dst, raw)", ind)
		case kCustomLeaf:
			g.leafAppend(f.valInfo.leaf, fmt.Sprintf("&%s[i]", ce), ind+"\t\t")
		case kInterface:
			g.p("%s\t\tvar err error", ind)
			g.p("%s\t\tdst, err = zerojson.AppendInterface(dst, %s[i])", ind, ce)
			g.p("%s\t\tif err != nil { return dst, err }", ind)
		default:
			g.emitLeafAppend(f.valInfo, ce+"[i]", ind+"\t\t")
		}
		g.p("%s\t}", ind)
		g.p("%s\tdst = append(dst, ']')", ind)
		g.p("%s}", ind)
	case kMap:
		// Range order, matching easyjson (encoding/json would sort keys).
		// A nil map encodes as null, a non-nil one (empty included) as an
		// object.
		g.p("%sif %s == nil {", ind, ce)
		g.p("%s\tdst = append(dst, \"null\"...)", ind)
		g.p("%s} else {", ind)
		g.p("%s\tdst = append(dst, '{')", ind)
		g.p("%s\tfirst := true", ind)
		g.p("%s\tfor mk, mv := range %s {", ind, ce)
		g.p("%s\t\tif !first { dst = append(dst, ',') }", ind)
		g.p("%s\t\tfirst = false", ind)
		g.p("%s\t\tdst = zerojson.AppendString(dst, string(mk))", ind)
		g.p("%s\t\tdst = append(dst, ':')", ind)
		switch f.valInfo.kind {
		case kStruct:
			g.p("%s\t\tvar err error", ind)
			g.p("%s\t\tdst, err = mv.AppendZJSON(dst)", ind)
			g.p("%s\t\tif err != nil { return dst, err }", ind)
		case kMarshaler:
			if f.valInfo.marshalOnValue {
				g.p("%s\t\traw, err := mv.MarshalJSON()", ind)
				g.p("%s\t\tif err != nil { return dst, err }", ind)
				g.p("%s\t\tif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)
				g.p("%s\t\tdst = append(dst, raw...)", ind)
			} else {
				g.p("%s\t\tvar err error", ind)
				g.p("%s\t\tdst, err = zerojson.AppendInterface(dst, mv)", ind)
				g.p("%s\t\tif err != nil { return dst, err }", ind)
			}
		case kTextMarshaler:
			if f.valInfo.textOnValue {
				g.p("%s\t\traw, err := mv.MarshalText()", ind)
				g.p("%s\t\tif err != nil { return dst, err }", ind)
				g.p("%s\t\tdst = zerojson.AppendStringBytes(dst, raw)", ind)
			} else {
				g.p("%s\t\tvar err error", ind)
				g.p("%s\t\tdst, err = zerojson.AppendInterface(dst, mv)", ind)
				g.p("%s\t\tif err != nil { return dst, err }", ind)
			}
		case kQuotedInt:
			if f.valInfo.marshalOnValue {
				g.emitLeafAppend(f.valInfo, "mv", ind+"\t\t")
			} else {
				g.p("%s\t\tvar err error", ind)
				g.p("%s\t\tdst, err = zerojson.AppendInterface(dst, mv)", ind)
				g.p("%s\t\tif err != nil { return dst, err }", ind)
			}
		case kInterface:
			g.p("%s\t\tvar err error", ind)
			g.p("%s\t\tdst, err = zerojson.AppendInterface(dst, mv)", ind)
			g.p("%s\t\tif err != nil { return dst, err }", ind)
		default:
			g.emitLeafAppend(f.valInfo, "mv", ind+"\t\t")
		}
		g.p("%s\t}", ind)
		g.p("%s\tdst = append(dst, '}')", ind)
		g.p("%s}", ind)
	}
}

// emitLeafAppend emits an encode statement for a leaf-kind value expr.
func (g *generator) emitLeafAppend(fi *fieldInfo, expr, ind string) {
	switch fi.kind {
	case kString, kEnum:
		g.p("%sdst = zerojson.AppendString(dst, string(%s))", ind, expr)
	case kJSONNumber:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendJSONNumber(dst, string(%s))", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	case kBool:
		g.p("%sdst = zerojson.AppendBool(dst, bool(%s))", ind, expr)
	case kInt:
		g.p("%sdst = zerojson.AppendInt64(dst, int64(%s))", ind, expr)
	case kUint:
		g.p("%sdst = zerojson.AppendUint64(dst, uint64(%s))", ind, expr)
	case kFloat32:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendFloat32(dst, float32(%s))", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	case kFloat64:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendFloat64(dst, float64(%s))", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	case kQuotedInt:
		g.p("%sdst = zerojson.AppendQuotedInt64(dst, int64(%s))", ind, expr)
	case kTime:
		g.p("%svar err error", ind)
		g.p("%sdst, err = zerojson.AppendTime(dst, %s)", ind, expr)
		g.p("%sif err != nil { return dst, err }", ind)
	case kUUID:
		g.p("%sdst = zerojson.AppendUUIDBytes(dst, [16]byte(%s))", ind, expr)
	case kCustomLeaf:
		g.leafAppend(fi.leaf, "&"+expr, ind)
	case kByteSlice:
		g.p("%sdst = zerojson.AppendBytesBase64(dst, %s)", ind, byteSliceEncodeExpr(*fi, expr))
	case kEmptyStruct:
		g.p("%s_ = %s", ind, expr)
		g.p("%sdst = append(dst, \"{}\"...)", ind)
	}
}
