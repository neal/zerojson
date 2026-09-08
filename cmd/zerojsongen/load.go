package main

import (
	"fmt"
	"go/constant"
	"go/types"
	"log"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

func (g *generator) collectFields(typeName string, st *types.Struct) []fieldInfo {
	var candidates []fieldInfo
	g.collectInto(typeName, st, "", "", &candidates)
	fields := resolveDominance(typeName, candidates)
	// Arena cell identifiers must not be derived by joining selector names:
	// an embedded A.B and a direct A_B otherwise both become A_B and emit
	// duplicate arena fields. Stable numeric ids are unambiguous and private
	// to the generated implementation.
	for i := range fields {
		fields[i].cellName = fmt.Sprintf("%d", i)
	}
	return fields
}

// resolveDominance applies encoding/json's rule for JSON-name collisions
// between direct and promoted (embedded) fields, once every candidate has
// been collected so declaration order cannot change the outcome: among
// the candidates for a name, those at the shallowest embedding depth
// compete; a single one wins; if several tie, the one with an explicit
// tag name wins if there is exactly one such; otherwise stdlib silently
// drops them all — which is refused here instead, with a pointer at the
// fix. Survivors keep their collection order (declaration order, with
// promoted fields at their embed's position — stdlib's index order).
func resolveDominance(typeName string, candidates []fieldInfo) []fieldInfo {
	byName := map[string][]int{}
	var names []string
	for i, f := range candidates {
		if _, seen := byName[f.jsonName]; !seen {
			names = append(names, f.jsonName)
		}
		byName[f.jsonName] = append(byName[f.jsonName], i)
	}
	keep := make(map[int]bool, len(candidates))
	for _, name := range names {
		idxs := byName[name]
		if len(idxs) == 1 {
			keep[idxs[0]] = true
			continue
		}
		minDepth := candidates[idxs[0]].embedDepth
		for _, i := range idxs[1:] {
			minDepth = min(minDepth, candidates[i].embedDepth)
		}
		var shallow, tagged []int
		for _, i := range idxs {
			if candidates[i].embedDepth == minDepth {
				shallow = append(shallow, i)
				if candidates[i].explicitName {
					tagged = append(tagged, i)
				}
			}
		}
		switch {
		case len(shallow) == 1:
			keep[shallow[0]] = true
		case len(tagged) == 1:
			keep[tagged[0]] = true
		default:
			log.Fatalf("%s: JSON field %q is claimed by %d fields at the same embedding depth with no single explicitly tagged one — encoding/json would silently drop all of them; give one an explicit distinct tag or json:\"-\"", typeName, name, len(shallow))
		}
	}
	out := make([]fieldInfo, 0, len(candidates))
	for i, f := range candidates {
		if keep[i] {
			out = append(out, f)
		}
	}
	return out
}

// collectRoot classifies a named slice/map root type (type X []T, []*T, or
// map[string]T) by classifying its UNDERLYING type directly — deliberately
// bypassing the named-type delegation in classify (which would otherwise
// treat a root that still carries easyjson-generated MarshalJSON methods,
// mid-migration, as a kMarshaler and refuse to generate a real codec for
// it). The resulting fieldInfo is a kSlice/kLeafSlice/kMap describing the
// element (slice) or value (map), reused by the root codecs in root.go
// exactly as a slice/map FIELD would be. under is obj.Type().Underlying(),
// already known to be *types.Slice or *types.Map by the caller.
func (g *generator) collectRoot(typeName string, under types.Type) *fieldInfo {
	fi := &fieldInfo{goName: typeName, cellName: "Root", jsonName: typeName}
	g.classify(fi, under, typeName)
	switch fi.kind {
	case kSlice, kLeafSlice, kMap:
		return fi
	default:
		// classify only produces these for a slice/map underlying, so this
		// is defensive; the unsupported-key/element cases (e.g. a non-string
		// map key) are already rejected inside classify with their own
		// messages.
		log.Fatalf("%s: unsupported root type (kind %d) — roots must be a named slice ([]T / []*T) or a named string-keyed map (map[string]T)", typeName, fi.kind)
		return nil
	}
}

// collectInto appends st's fields to *out, recursing into untagged
// embedded value structs so their fields are promoted (flattened) into the
// parent — matching encoding/json. accessPrefix/cellPrefix thread the Go
// selector and arena-id path for promoted fields.
func (g *generator) collectInto(typeName string, st *types.Struct, accessPrefix, cellPrefix string, out *[]fieldInfo) {
	depth := strings.Count(accessPrefix, ".")
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		// Unexported embedded structs still expose exported descendants.
		// A tag name nests them; an option-only tag still promotes them.
		if !f.Exported() {
			t := types.Unalias(f.Type())
			p, pointer := t.(*types.Pointer)
			if pointer {
				t = p.Elem()
			}
			if _, structType := t.Underlying().(*types.Struct); !f.Embedded() || !structType {
				continue
			}
			if f.Pkg() != g.pkg.Types && tag != "-" {
				log.Fatalf("%s.%s: inaccessible embedded field from %s — use an exported wrapper or generate its codec in the declaring package", typeName, f.Name(), f.Pkg().Path())
			}
			if pointer && tag != "-" {
				log.Fatalf("%s.%s: unexported embedded pointer types are not supported — encoding/json cannot allocate the pointee", typeName, f.Name())
			}
		}
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		// An invalid tag NAME is ignored and the Go field name is used
		// instead, exactly as encoding/json does (isValidTag below is a
		// copy of the stdlib character-class check). This runs before the
		// embedded-promotion decision so that, like stdlib, an embedded
		// field whose tag name is invalid is treated as untagged (name
		// "") and its fields are promoted.
		invalidTag := false
		if name != "" && !isValidTag(name) {
			name = ""
			invalidTag = true
		}

		if f.Embedded() && name == "" {
			g.flattenEmbedded(typeName, f, accessPrefix, cellPrefix, out)
			continue
		}

		explicitName := name != ""
		if name == "" {
			name = f.Name()
		}
		// Collisions between this name and a promoted or outer field are
		// resolved by resolveDominance once every candidate is collected.

		fi := fieldInfo{
			goName:       f.Name(),
			access:       accessPrefix + f.Name(),
			cellName:     cellPrefix + f.Name(),
			jsonName:     name,
			embedDepth:   depth,
			explicitName: explicitName,
			invalidTag:   invalidTag,
		}
		ctx := typeName + "." + f.Name()
		if err := g.parseJSONOptions(&fi, parts[1:], ctx); err != nil {
			log.Fatal(err)
		}
		if err := g.parseZerojsonTag(&fi, reflect.StructTag(st.Tag(i)).Get("zerojson"), ctx); err != nil {
			log.Fatal(err)
		}

		// Inspect aliases for pointer shape, but preserve the spelled type
		// for classify's emitted casts (an exported alias may be the only
		// accessible name for an otherwise unexported external type).
		t := f.Type()
		if p, ok := types.Unalias(t).(*types.Pointer); ok {
			fi.ptr = true
			t = p.Elem()
		}
		g.classify(&fi, t, ctx)
		if err := g.validateFieldOptions(fi, ctx); err != nil {
			log.Fatal(err)
		}
		*out = append(*out, fi)
	}
}

// isValidTag reports whether s is a valid JSON tag name, using the exact
// character-class logic of encoding/json's own isValidTag (encoding/
// json/tags.go): non-empty, and every rune is either one of the allowed
// punctuation characters, a Unicode letter, or a Unicode digit. Backslash
// and the double-quote are deliberately excluded (they are reserved), so a
// tag like `json:"a\b"` is invalid and the generator falls back to the Go
// field name — matching stdlib rather than passing the raw bytes through
// as a wire key.
func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote chars are reserved, but otherwise any
			// punctuation chars are allowed in a tag name.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// parseJSONOptions parses the options following the name in a json tag.
// Only encoding/json's own standard options are accepted here — the name,
// omitempty, omitzero, and string; anything else (e.g. a ",intern"
// convention) is an error pointing at the zerojson tag instead, so a
// stray option can never silently do nothing.
func (g *generator) parseJSONOptions(fi *fieldInfo, opts []string, ctx string) error {
	for _, opt := range opts {
		switch opt {
		case "":
			// Tolerate a trailing comma (e.g. a leftover "name,").
		case "omitempty":
			fi.omitempty = true
		case "omitzero":
			fi.omitzero = true
		case "string":
			fi.stringOpt = true
		default:
			return fmt.Errorf("%s: unknown json tag option %q — standard options are the name, omitempty, omitzero, string, and \"-\"; move custom options (e.g. intern) to a `zerojson:\"...\"` tag", ctx, opt)
		}
	}
	return nil
}

// parseZerojsonTag parses the comma-separated custom option list in a
// zerojson tag. intern is the only option today; anything else is an
// error.
func (g *generator) parseZerojsonTag(fi *fieldInfo, tag, ctx string) error {
	if tag == "" {
		return nil
	}
	for opt := range strings.SplitSeq(tag, ",") {
		switch opt {
		case "intern":
			fi.intern = true
		default:
			return fmt.Errorf("%s: unknown zerojson tag option %q — the only option today is intern", ctx, opt)
		}
	}
	return nil
}

// validateFieldOptions rejects option/kind combinations the generator
// cannot honor, once classify has determined the field's kind.
func (g *generator) validateFieldOptions(fi fieldInfo, ctx string) error {
	if fi.intern && fi.kind != kString && fi.kind != kEnum && fi.kind != kMap {
		return fmt.Errorf("%s: zerojson:\"intern\" is supported only on string, named-string enum, or map fields (on a map it interns the keys)", ctx)
	}
	if fi.stringOpt {
		switch fi.kind {
		case kInt, kUint:
			// Supported: encode.go emits a quoted append and decode uses the
			// strict ReadQuotedInt/Uint width variant.
		case kQuotedInt:
			return fmt.Errorf("%s: %s already marshals as a quoted string (implements MarshalJSON) — drop the redundant \",string\" tag", ctx, fi.typeStr)
		default:
			return fmt.Errorf("%s: \",string\" is implemented for int/uint fields only today", ctx)
		}
	}
	if fi.omitzero && !fi.ptr {
		switch fi.kind {
		case kString, kEnum, kBool, kQuotedInt, kInt, kUint, kFloat32, kFloat64,
			kSlice, kLeafSlice, kMap, kByteSlice, kInterface, kUUID, kJSONNumber:
			// Always representable: scalar zero value, or nil for
			// reference kinds (uuid.UUID falls back to a comparable
			// zero-value check in zeroGate when it has no IsZero method).
		case kTime, kStruct:
			if !fi.hasIsZero {
				return fmt.Errorf("%s: omitzero requires an IsZero() bool method on %s (none found) — implement one or drop omitzero", ctx, fi.typeStr)
			}
		case kCustomLeaf, kTextMarshaler, kAppender:
			// A scalar-, slice-, or map-underlying custom type gates on
			// its underlying zero value (stdlib semantics) when it has no
			// IsZero; only a struct-shaped one needs the method.
			if !fi.hasIsZero && (fi.scalarEmpty == "" || fi.scalarEmpty == "array") {
				return fmt.Errorf("%s: omitzero requires an IsZero() bool method on %s (none found) — implement one or drop omitzero", ctx, fi.typeStr)
			}
			// Preserve the existing Raw option restriction. Empty Raw now
			// safely encodes as null, but enabling this option is separate
			// from custom-type emptiness classification.
			if fi.kind == kCustomLeaf && fi.leaf != nil && fi.leaf.appendPkg == runtimePath && fi.leaf.appendFn == "AppendRaw" {
				return fmt.Errorf("%s: omitzero is not supported on zerojson.Raw — use omitempty, or a *zerojson.Raw", ctx)
			}
		case kMarshaler:
			if !fi.marshalerIsSlice && !fi.marshalerIsMap && !fi.hasIsZero && (fi.scalarEmpty == "" || fi.scalarEmpty == "array") {
				return fmt.Errorf("%s: omitzero requires an IsZero() bool method on %s (none found) — implement one or drop omitzero", ctx, fi.typeStr)
			}
		default:
			return fmt.Errorf("%s: omitzero is not supported for this field kind (%s)", ctx, fi.typeStr)
		}
	}
	return nil
}

// flattenEmbedded promotes an untagged embedded struct's fields. Only
// embedded value structs without their own JSON methods are supported;
// embedded pointers and Marshaler-bearing embeds change encoding/json
// semantics in subtle ways and are refused loudly.
func (g *generator) flattenEmbedded(typeName string, f *types.Var, accessPrefix, cellPrefix string, out *[]fieldInfo) {
	t := types.Unalias(f.Type())
	if _, ok := t.(*types.Pointer); ok {
		log.Fatalf("%s.%s: embedded pointer types are not supported — unembed it or give it a json tag", typeName, f.Name())
	}
	named, ok := t.(*types.Named)
	if !ok {
		log.Fatalf("%s.%s: embedded non-named type is not supported", typeName, f.Name())
	}
	if (hasMarshalJSON(named) && g.handwritten(named, "MarshalJSON")) || (hasMarshalText(named) && g.handwritten(named, "MarshalText")) {
		// Go promotes the method to the parent, so json.Marshal(parent)
		// would call it and ignore sibling fields — not something to
		// silently reimplement.
		log.Fatalf("%s.%s: embedded type %s has its own JSON/Text methods; embedding it changes marshaling of the whole parent — unembed it or add an explicit tag", typeName, f.Name(), named.Obj().Name())
	}
	if g.handwritten(named, "AppendZJSON") || g.handwritten(named, "ReadZJSON") {
		// Same trap for zerojson's own extension pair: the promoted
		// AppendZJSON/ReadZJSON would make the PARENT look like an
		// Appender/Reader to any generator that sees it, and stdlib
		// (which knows nothing of those methods) would still encode the
		// parent structurally — the two can never agree.
		log.Fatalf("%s.%s: embedded type %s has its own AppendZJSON/ReadZJSON methods, which embedding would promote to the parent — unembed it or add an explicit tag", typeName, f.Name(), named.Obj().Name())
	}
	est, ok := named.Underlying().(*types.Struct)
	if !ok {
		log.Fatalf("%s.%s: embedded non-struct type %s is not supported", typeName, f.Name(), named.Obj().Name())
	}
	g.collectInto(typeName, est, accessPrefix+f.Name()+".", cellPrefix+f.Name()+"_", out)
}

func (g *generator) classify(fi *fieldInfo, t types.Type, ctx string) {
	// The emitted type expression keeps the alias name the user wrote
	// (json.RawMessage, not jsontext.Value — which would drag an import
	// of a package the user never named into the generated file); every
	// classification decision below looks through the alias to the type
	// it denotes. Without this, an alias is neither *types.Named nor
	// anything else the switch recognizes and silently falls through to
	// its underlying type — on Go 1.27, where json.RawMessage became an
	// alias, that meant base64-encoding it as a plain []byte.
	fi.typeStr = g.typeExpr(t)
	t = types.Unalias(t)

	if named, ok := t.(*types.Named); ok {
		// Recorded uniformly for every named-type kind so omitzero can
		// honor an IsZero() bool method per Go 1.24 stdlib semantics
		// (time.Time and custom domain types can have one; plain
		// uuid.UUID and most struct/Marshaler types don't, and fall back
		// to a zero-value comparison or a generate-time error).
		fi.hasIsZero = hasIsZeroMethod(named)
		// Record the underlying emptiness category (if any) so omitempty
		// on a Marshaler/Appender/TextMarshaler/custom-leaf value field
		// can gate on it like encoding/json's isEmptyValue does — which
		// inspects the underlying kind: zero for a scalar, len 0 for a
		// slice or map (see fieldInfo.scalarEmpty).
		switch u := named.Underlying().(type) {
		case *types.Basic:
			switch {
			case u.Info()&types.IsNumeric != 0:
				fi.scalarEmpty = "num"
			case u.Info()&types.IsString != 0:
				fi.scalarEmpty = "str"
			case u.Info()&types.IsBoolean != 0:
				fi.scalarEmpty = "bool"
			}
		case *types.Slice, *types.Map:
			fi.scalarEmpty = "len"
		case *types.Array:
			// Keep arrays distinct: unlike slices/maps, null must not
			// clear them and omitzero is not a nil comparison.
			fi.scalarEmpty = "array"
		}
		obj := named.Obj()
		qual := ""
		if obj.Pkg() != nil {
			qual = obj.Pkg().Path()
		}
		if ls := g.leaves[qual+"."+obj.Name()]; ls != nil {
			fi.kind = kCustomLeaf
			fi.leaf = ls
			g.useLeaf(ls)
			return
		}
		switch qual + "." + obj.Name() {
		case "time.Time":
			fi.kind = kTime
			return
		case "encoding/json.Number":
			// A named string type, but NOT an enum: encoding/json (and
			// easyjson) write the literal bare and read a bare number —
			// classifying it as a quoted enum would silently change the
			// wire format.
			fi.kind = kJSONNumber
			return
		case uuidPath + ".UUID":
			fi.kind = kUUID
			return
		}

		// Hand-written AppendZJSON/ReadZJSON extension pair (see
		// interface.go): takes precedence over everything below, including
		// MarshalJSON and same-package struct auto-discovery — a type
		// author opting into the fast append/read calling convention
		// directly always wins. g.handwritten only reports true for a
		// method actually written by hand: our own generated types already
		// have an AppendZJSON method (that's the generated encode entry
		// point), so the replaceableMethodFiles check keeps a generated
		// type from matching this by its own emitted AppendZJSON.
		hasAppend := hasAppendZJSONSig(named) && g.handwritten(named, "AppendZJSON")
		hasRead := hasReadZJSONSig(named) && g.handwritten(named, "ReadZJSON")
		if hasAppend && hasRead {
			fi.kind = kAppender
			// encoding/json delivers a null through UnmarshalJSON on the
			// strength of that method alone (MarshalJSON is irrelevant to
			// decoding), so the null-delivery decision follows it alone.
			fi.appenderHasUnmarshalJSON = hasUnmarshalJSON(named)
			return
		}
		if hasAppend != hasRead {
			log.Fatalf("%s: %s has only one of AppendZJSON/ReadZJSON — implement both (see interface.go)", ctx, t)
		}

		hasM, hasU := hasMarshalJSON(named), hasUnmarshalJSON(named)

		// Same-package structs get generated codecs when explicitly
		// listed, when they have no JSON methods, or when their methods
		// live in generated files (easyjson output — exactly what is
		// being replaced). Hand-written methods are always respected.
		if obj.Pkg() == g.pkg.Types {
			if _, isStruct := named.Underlying().(*types.Struct); isStruct &&
				(g.explicit[obj.Name()] || g.replaceableMethods(named)) {
				rejectGeneric(named, ctx)
				g.discover(obj.Name())
				fi.kind = kStruct
				fi.elem = obj.Name()
				return
			}
		}

		// Named int64 marshaled as a quoted string. The fast path is explicit:
		// arbitrary int64-backed MarshalJSON methods can implement any wire
		// representation, so inferring quoted digits from the underlying kind
		// silently corrupts custom formats.
		if hasQuotedInt64Marker(named) {
			b, basic := named.Underlying().(*types.Basic)
			if !basic || b.Kind() != types.Int64 || !hasM || !hasU || !hasMarshalJSONValue(named) {
				log.Fatalf("%s: %s declares ZeroJSONQuotedInt64 but must be an int64-backed type with value MarshalJSON and UnmarshalJSON", ctx, t)
			}
			fi.kind = kQuotedInt
			// The marker promises the quoted form on the wire; the read
			// stays tolerant of a bare number too (as the type's own
			// UnmarshalJSON typically is), unlike the strict bare-only
			// readers plain int fields use.
			fi.intRead = "l.ReadQuotedOrBareInt64()"
			fi.marshalOnValue = hasMarshalJSONValue(named)
			return
		}

		// Custom JSON methods win over structural handling: a type that
		// defines its own wire format must be delegated to, even if it is
		// a same-package struct we could otherwise generate for.
		if hasM && hasU {
			fi.kind = kMarshaler
			fi.marshalOnValue = hasMarshalJSONValue(named)
			switch named.Underlying().(type) {
			case *types.Slice:
				fi.marshalerIsSlice = true
			case *types.Map:
				fi.marshalerIsMap = true
			}
			return
		}
		if hasM != hasU {
			log.Fatalf("%s: %s has only one of MarshalJSON/UnmarshalJSON — implement both", ctx, t)
		}

		// Text marshalers (e.g. civil.Date) serialize as quoted strings,
		// exactly as encoding/json and easyjson treat them.
		hasMT, hasUT := hasMarshalText(named), hasUnmarshalText(named)
		if hasMT != hasUT {
			log.Fatalf("%s: %s has only one of MarshalText/UnmarshalText — implement both", ctx, t)
		}
		if hasMT && hasUT {
			fi.kind = kTextMarshaler
			fi.textOnValue = hasMarshalTextValue(named)
			return
		}

		// String-kind named types are enums: collect their constants.
		if b, ok := named.Underlying().(*types.Basic); ok && b.Kind() == types.String {
			fi.kind = kEnum
			fi.enumConsts = g.enumConstants(named)
			return
		}
	}

	// Basic kinds, including named types with basic underlying kinds
	// (e.g. time.Duration): generated code casts via typeStr.
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.String:
			fi.kind = kString
			return
		case types.Bool:
			fi.kind = kBool
			return
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
			fi.kind = kInt
			fi.intRead = signedReadCall(b.Kind())
			return
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			fi.kind = kUint
			fi.intRead = unsignedReadCall(b.Kind())
			return
		case types.Uintptr:
			// A uintptr on the JSON wire is a raw machine-pointer value: not
			// portable, not meaningful to a decoder on another host, and a
			// near-certain modeling mistake. Refuse it at generate time (like
			// a non-string map key) rather than emit a codec for it.
			log.Fatalf("%s: uintptr field is not supported — a raw pointer value has no portable JSON representation; use a fixed-width integer type instead", ctx)
		case types.Float32:
			fi.kind = kFloat32
			return
		case types.Float64:
			fi.kind = kFloat64
			return
		}
	}

	if s, ok := t.Underlying().(*types.Slice); ok {
		// []byte and named byte-slice types serialize as base64 strings
		// (both encoding/json and easyjson), UNLESS the element type
		// implements its own Marshaler/TextMarshaler — then it's an ordinary
		// element array. The element check is on the UNDERLYING basic kind,
		// so a named byte type element (type MyByte uint8, no methods)
		// base64s too, exactly as encoding/json's `Elem().Kind()==Uint8`
		// test does. A named byte-slice that carries its own MarshalJSON
		// (e.g. json.RawMessage, zerojson.Raw) was already classified as a
		// Marshaler/leaf above and never reaches here.
		if eb, ok := s.Elem().Underlying().(*types.Basic); ok && eb.Kind() == types.Uint8 {
			// Validate the element through the ordinary classifier before
			// selecting a representation shortcut. This enforces the same
			// method-set policy for direct fields and optimized containers.
			var element fieldInfo
			g.classify(&element, s.Elem(), ctx+"[elem]")
			if !types.Identical(s.Elem(), types.Typ[types.Uint8]) {
				fi.byteElem = g.typeExpr(s.Elem())
			}
			if en, isNamed := types.Unalias(s.Elem()).(*types.Named); !isNamed || (!hasMarshalJSON(en) && !hasMarshalText(en)) {
				fi.kind = kByteSlice
				if !types.Identical(s.Elem(), types.Typ[types.Uint8]) {
					fi.byteElem = g.typeExpr(s.Elem())
				}
				return
			}
		}
		// The element type is handed to classify WITH any alias intact, so
		// the emitted element type expression keeps the user's spelling
		// (json.RawMessage, not the jsontext.Value it aliases on Go 1.27 —
		// which would add an import the user never wrote); classify looks
		// through the alias itself for every decision.
		elem := s.Elem()
		if p, ok := types.Unalias(elem).(*types.Pointer); ok {
			fi.elemPtr = true
			elem = p.Elem()
		}
		if en, ok := types.Unalias(elem).(*types.Named); ok && en.Obj().Pkg() == g.pkg.Types {
			if _, isStruct := en.Underlying().(*types.Struct); isStruct &&
				(g.explicit[en.Obj().Name()] || g.replaceableMethods(en)) {
				rejectGeneric(en, ctx+"[elem]")
				g.discover(en.Obj().Name())
				fi.kind = kSlice
				fi.elem = en.Obj().Name()
				return
			}
		}
		vi := &fieldInfo{}
		g.classify(vi, elem, ctx+"[elem]")
		if vi.kind == kAppender {
			// Appender/Reader elements have no slice codec yet; a type
			// that ALSO carries MarshalJSON/UnmarshalJSON is delegated to
			// those in this position (its own fields keep the fast pair).
			if !g.demoteAppenderToMarshaler(vi, elem) {
				log.Fatalf("%s: slice of an Appender-interface type (%s) is not supported yet — add MarshalJSON/UnmarshalJSON to the element type, which are used in this position while fields keep AppendZJSON/ReadZJSON", ctx, t)
			}
		}
		if fi.elemPtr {
			// Pointer elements: only self-marshaling types (nil -> null).
			switch vi.kind {
			case kMarshaler, kTextMarshaler:
				fi.kind = kLeafSlice
				fi.valInfo = vi
				return
			}
			log.Fatalf("%s: unsupported pointer-element slice %s", ctx, t)
		}
		switch vi.kind {
		case kString, kEnum, kBool, kInt, kUint, kFloat32, kFloat64,
			kQuotedInt, kTime, kUUID, kCustomLeaf, kMarshaler, kTextMarshaler, kInterface, kByteSlice, kJSONNumber:
			fi.kind = kLeafSlice
			fi.valInfo = vi
			return
		}
	}

	if m, ok := t.Underlying().(*types.Map); ok {
		kb, ok := m.Key().Underlying().(*types.Basic)
		if !ok || kb.Kind() != types.String {
			log.Fatalf("%s: unsupported map key type %s (string-kind keys only)", ctx, m.Key())
		}
		if kn, ok := types.Unalias(m.Key()).(*types.Named); ok && (hasMarshalText(kn) || hasUnmarshalText(kn)) {
			// encoding/json routes such keys through MarshalText/
			// UnmarshalText; the generated map codec converts the raw
			// key string directly, which would silently bypass them.
			log.Fatalf("%s: map key type %s has MarshalText/UnmarshalText, which encoding/json would apply to the keys — not supported; use a plain string-kind key type", ctx, m.Key())
		}
		vi := &fieldInfo{}
		if _, named := types.Unalias(m.Elem()).(*types.Named); !named && isEmptyStruct(m.Elem()) {
			vi.kind = kEmptyStruct
			vi.typeStr = g.typeExpr(m.Elem())
		} else {
			g.classify(vi, m.Elem(), ctx+"[value]")
			if vi.kind == kStruct {
				rejectGenericType(m.Elem(), ctx+"[value]")
			}
			if vi.kind == kAppender && !g.demoteAppenderToMarshaler(vi, m.Elem()) {
				log.Fatalf("%s: map of an Appender-interface type (%s) is not supported yet — add MarshalJSON/UnmarshalJSON to the value type, which are used in this position while fields keep AppendZJSON/ReadZJSON", ctx, m.Elem())
			}
		}
		switch vi.kind {
		case kString, kEnum, kBool, kInt, kUint, kFloat32, kFloat64,
			kQuotedInt, kTime, kUUID, kCustomLeaf, kEmptyStruct, kStruct, kMarshaler, kTextMarshaler, kInterface, kByteSlice, kJSONNumber:
		default:
			log.Fatalf("%s: unsupported map value kind for %s", ctx, m.Elem())
		}
		fi.kind = kMap
		fi.keyTypeStr = g.typeExpr(m.Key())
		fi.valInfo = vi
		return
	}

	// Named empty structs without methods (including external set-map
	// values) reach here only AFTER custom codec classification.
	if isEmptyStruct(t) {
		fi.kind = kEmptyStruct
		return
	}
	// Empty interfaces use generic readers unless an existing concrete
	// pointer requires encoding/json's receiver semantics.
	if iface, ok := t.Underlying().(*types.Interface); ok && iface.NumMethods() == 0 {
		fi.kind = kInterface
		return
	}

	if named, ok := types.Unalias(t).(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg() != g.pkg.Types {
		if _, isStruct := named.Underlying().(*types.Struct); isStruct {
			log.Fatalf("%s: %s is a struct from another package with no codec — run zerojsongen in %s with -types %s (its generated AppendZJSON/ReadZJSON pair is then delegated to here), register it with -leaf, or implement MarshalJSON/UnmarshalJSON on it",
				ctx, t, named.Obj().Pkg().Path(), named.Obj().Name())
		}
	}
	log.Fatalf("%s: unsupported field type %s — add it to the generator's leaf table or implement MarshalJSON/UnmarshalJSON", ctx, t)
}

func isEmptyStruct(t types.Type) bool {
	s, ok := t.Underlying().(*types.Struct)
	return ok && s.NumFields() == 0
}

// demoteAppenderToMarshaler re-classifies an Appender/Reader element or
// map value as a Marshaler when its type also implements
// MarshalJSON/UnmarshalJSON, since the generator has no slice/map codec
// for the Appender pair yet. Reports whether it could.
func (g *generator) demoteAppenderToMarshaler(vi *fieldInfo, t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || !hasMarshalJSON(named) || !hasUnmarshalJSON(named) {
		return false
	}
	vi.kind = kMarshaler
	vi.marshalOnValue = hasMarshalJSONValue(named)
	switch named.Underlying().(type) {
	case *types.Slice:
		vi.marshalerIsSlice = true
	case *types.Map:
		vi.marshalerIsMap = true
	}
	return true
}

// rejectGeneric refuses to generate a codec for a generic type (or an
// instantiation of one). The generator emits methods on the bare type
// name — `func (v *Box) ...` — which does not compile for a generic
// `Box[T]`, and it would previously report success and leave the user
// with a broken file. A generic type from another package that carries
// its own MarshalJSON is unaffected: it is delegated to, never generated.
func rejectGeneric(named *types.Named, ctx string) {
	if named.TypeParams().Len() > 0 || named.TypeArgs().Len() > 0 {
		log.Fatalf("%s: generic type %s is not supported — wrap it in a non-generic named type, or implement MarshalJSON/UnmarshalJSON on it so it is delegated to", ctx, named)
	}
}

func rejectGenericType(t types.Type, ctx string) {
	if named, ok := types.Unalias(t).(*types.Named); ok {
		rejectGeneric(named, ctx)
	}
}

// signedReadCall / unsignedReadCall pick the width-aware, range-checked
// Lexer read for an integer field's basic kind, so a value outside the
// target width errors (matching encoding/json) instead of being wrapped by
// a blind cast. Platform-width `int`/`uint` map to ReadInt/ReadUint, which
// range-check against math.MaxInt/math.MinInt/math.MaxUint: on a 64-bit
// build those equal the 64-bit limits (identical codegen to ReadInt64/
// ReadUint64), on a 32-bit build they tighten to the word, so the generated
// code is portable rather than silently wrapping a >32-bit value. (`uintptr`
// is refused at classification time — a uintptr JSON field is a raw pointer
// value on the wire, always a design smell — so it never reaches here.)
func signedReadCall(k types.BasicKind) string {
	switch k {
	case types.Int8:
		return "l.ReadInt8()"
	case types.Int16:
		return "l.ReadInt16()"
	case types.Int32:
		return "l.ReadInt32()"
	case types.Int64:
		return "l.ReadInt64()"
	default: // Int
		return "l.ReadInt()"
	}
}

func unsignedReadCall(k types.BasicKind) string {
	switch k {
	case types.Uint8:
		return "l.ReadUint8()"
	case types.Uint16:
		return "l.ReadUint16()"
	case types.Uint32:
		return "l.ReadUint32()"
	case types.Uint64:
		return "l.ReadUint64()"
	default: // Uint
		return "l.ReadUint()"
	}
}

// replaceableMethods reports whether a type's JSON methods can be
// superseded by a generated codec: hand-written methods are respected,
// but methods living in generated files (easyjson output) are exactly
// what zerojson replaces.
func (g *generator) replaceableMethods(named *types.Named) bool {
	ms := types.NewMethodSet(types.NewPointer(named))
	for method := range ms.Methods() {
		m := method.Obj()
		sig, _ := m.Type().(*types.Signature)
		var proper bool
		switch m.Name() {
		case "MarshalJSON", "MarshalText":
			proper = sigIs(sig, nil, []types.Type{tByteSlice, tError})
		case "UnmarshalJSON", "UnmarshalText":
			proper = sigIs(sig, []types.Type{tByteSlice}, []types.Type{tError})
		case "AppendZJSON":
			// A hand-written zerojson extension pair defines the wire
			// format just as firmly as MarshalJSON does; generating a
			// second AppendZJSON for the type would not even compile.
			proper = hasAppendZJSONSig(named)
		case "ReadZJSON":
			proper = hasReadZJSONSig(named)
		default:
			continue
		}
		// Only a CORRECTLY-signed hand-written method makes the type
		// non-replaceable (its wire format must be respected). A same-named
		// method with the wrong signature is not a real Marshaler — stdlib
		// and easyjson ignore it and encode structurally — so it must not
		// block generating a structural codec for the type.
		if proper && !g.replaceableMethodFiles[g.pkg.Fset.Position(m.Pos()).Filename] {
			return false
		}
	}
	return true
}

// discover queues a same-package struct type for generation.
func (g *generator) discover(name string) {
	if !g.genSet[name] {
		g.genSet[name] = true
		g.queue = append(g.queue, name)
	}
}

// methodSig returns the signature of named's method called name (searching
// the pointer receiver's method set, so both value- and pointer-receiver
// methods are found), or nil if there is no such method.
func methodSig(named *types.Named, name string) *types.Signature {
	ms := types.NewMethodSet(types.NewPointer(named))
	for method := range ms.Methods() {
		if method.Obj().Name() == name {
			sig, _ := method.Obj().Type().(*types.Signature)
			return sig
		}
	}
	return nil
}

var (
	tByteSlice = types.NewSlice(types.Typ[types.Byte])
	tError     = types.Universe.Lookup("error").Type()
	tBool      = types.Typ[types.Bool]
)

// sigIs reports whether sig has exactly the given (non-variadic) parameter
// and result types, by go/types identity. This is what distinguishes a
// real json.Marshaler etc. from an unrelated method that merely shares the
// name but has the wrong signature (e.g. `func (T) MarshalJSON() string`) —
// stdlib and easyjson ignore such a method and encode the value
// structurally, and so must the generator.
func sigIs(sig *types.Signature, params, results []types.Type) bool {
	if sig == nil || sig.Variadic() {
		return false
	}
	if sig.Params().Len() != len(params) || sig.Results().Len() != len(results) {
		return false
	}
	for i, p := range params {
		if !types.Identical(sig.Params().At(i).Type(), p) {
			return false
		}
	}
	for i, r := range results {
		if !types.Identical(sig.Results().At(i).Type(), r) {
			return false
		}
	}
	return true
}

// The following predicates check for a method with the EXACT signature the
// corresponding standard interface requires — the equivalent of
// types.Implements against json.Marshaler, json.Unmarshaler,
// encoding.TextMarshaler, encoding.TextUnmarshaler, and (for omitzero) an
// IsZero() bool method. The signatures are expressed with universe/basic
// types so no dependency on the encoding/json or encoding packages being
// in the generated package's import graph is required.

// hasMarshalJSON reports whether named implements json.Marshaler:
// MarshalJSON() ([]byte, error).
func hasMarshalJSON(named *types.Named) bool {
	return sigIs(methodSig(named, "MarshalJSON"), nil, []types.Type{tByteSlice, tError})
}

func hasMarshalJSONValue(named *types.Named) bool {
	return sigIs(methodSigFromSet(types.NewMethodSet(named), "MarshalJSON"), nil, []types.Type{tByteSlice, tError})
}

// hasUnmarshalJSON reports whether named implements json.Unmarshaler:
// UnmarshalJSON([]byte) error.
func hasUnmarshalJSON(named *types.Named) bool {
	return sigIs(methodSig(named, "UnmarshalJSON"), []types.Type{tByteSlice}, []types.Type{tError})
}

// hasMarshalText reports whether named implements encoding.TextMarshaler:
// MarshalText() ([]byte, error).
func hasMarshalText(named *types.Named) bool {
	return sigIs(methodSig(named, "MarshalText"), nil, []types.Type{tByteSlice, tError})
}

func hasMarshalTextValue(named *types.Named) bool {
	return sigIs(methodSigFromSet(types.NewMethodSet(named), "MarshalText"), nil, []types.Type{tByteSlice, tError})
}

// hasUnmarshalText reports whether named implements encoding.TextUnmarshaler:
// UnmarshalText([]byte) error.
func hasUnmarshalText(named *types.Named) bool {
	return sigIs(methodSig(named, "UnmarshalText"), []types.Type{tByteSlice}, []types.Type{tError})
}

// hasIsZero reports whether named has an IsZero() bool method (Go 1.24
// omitzero semantics). A same-named method with a different signature (e.g.
// IsZero() string) does not count.
func hasIsZeroMethod(named *types.Named) bool {
	return sigIs(methodSig(named, "IsZero"), nil, []types.Type{tBool})
}

// hasQuotedInt64Marker reports an explicit promise that the type's JSON wire
// representation is the common quoted base-10 int64 convention. Requiring a
// value method makes the promise stable for non-addressable map values too.
func hasQuotedInt64Marker(named *types.Named) bool {
	return sigIs(methodSigFromSet(types.NewMethodSet(named), "ZeroJSONQuotedInt64"), nil, nil)
}

// hasAppendZJSONSig reports whether named implements zerojson.Appender:
// AppendZJSON([]byte) ([]byte, error) — the exact signature every generated
// codec (and every hand-written leaf) uses. Expressible with universe types
// alone.
func hasAppendZJSONSig(named *types.Named) bool {
	return sigIs(methodSig(named, "AppendZJSON"), []types.Type{tByteSlice}, []types.Type{tByteSlice, tError})
}

// hasReadZJSONSig reports whether named implements zerojson.Reader:
// ReadZJSON(*zerojson.Lexer). The parameter is the runtime's *Lexer, which
// may not be nameable via universe types, so this checks the structural
// shape — exactly one pointer parameter to a type named "Lexer" and no
// results — which distinguishes it from a wrong-signature same-name method.
func hasReadZJSONSig(named *types.Named) bool {
	sig := methodSig(named, "ReadZJSON")
	if sig == nil || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return false
	}
	// Look through aliases on both the parameter (`type L = *zerojson.Lexer`)
	// and the pointee (`type LexerAlias = zerojson.Lexer`): either is the
	// same type and must be recognized.
	ptr, ok := types.Unalias(sig.Params().At(0).Type()).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(ptr.Elem()).(*types.Named)
	return ok && n.Obj().Name() == "Lexer" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == runtimePath
}

func methodSigFromSet(ms *types.MethodSet, name string) *types.Signature {
	for method := range ms.Methods() {
		if method.Obj().Name() == name {
			sig, _ := method.Obj().Type().(*types.Signature)
			return sig
		}
	}
	return nil
}

// rejectRecursiveTypes refuses recursive generated schema graphs before
// emitting code. The arena embeds child arenas by value for allocation-free
// decoding; a recursive edge would therefore produce an invalid recursive Go
// type, and recursive encoders would additionally need cycle detection to
// match encoding/json safely. Refusing the shape is preferable to claiming a
// successful generation that later fails to compile or can recurse forever.
func (g *generator) rejectRecursiveTypes() {
	byName := make(map[string]*typeInfo, len(g.types))
	for i := range g.types {
		byName[g.types[i].name] = &g.types[i]
	}
	state := map[string]uint8{}
	var stack []string
	var visit func(string)
	visit = func(name string) {
		if state[name] == 2 {
			return
		}
		if state[name] == 1 {
			start := 0
			for start < len(stack) && stack[start] != name {
				start++
			}
			cycle := append(append([]string(nil), stack[start:]...), name)
			log.Fatalf("recursive generated type graph is not supported: %s", strings.Join(cycle, " -> "))
		}
		state[name] = 1
		stack = append(stack, name)
		ti := byName[name]
		if ti != nil {
			var edges []string
			if ti.rootField != nil {
				edges = structEdges(*ti.rootField)
			} else {
				for _, f := range ti.fields {
					edges = append(edges, structEdges(f)...)
				}
			}
			for _, next := range edges {
				if byName[next] != nil {
					visit(next)
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
	}
	for name := range byName {
		visit(name)
	}
}

func structEdges(f fieldInfo) []string {
	switch f.kind {
	case kStruct, kSlice:
		return []string{f.elem}
	case kMap:
		if f.valInfo != nil && f.valInfo.kind == kStruct {
			return []string{f.valInfo.elem}
		}
	}
	return nil
}

// handwritten reports whether named has a method called name that was NOT
// emitted by a replaceable codec generator (i.e. its defining file isn't in
// g.replaceableMethodFiles). Types outside the package under generation are always
// conservatively treated as hand-written here, same as replaceableMethods:
// replaceableMethodFiles only has file positions for the package currently being
// generated, so a cross-package type's own generated methods (from a
// different generate invocation) look hand-written from here — which is
// still correct, just a missed fast-path opportunity, since kAppender
// dispatches through the interface either way.
func (g *generator) handwritten(named *types.Named, name string) bool {
	ms := types.NewMethodSet(types.NewPointer(named))
	for method := range ms.Methods() {
		m := method.Obj()
		if m.Name() == name {
			return !g.replaceableMethodFiles[g.pkg.Fset.Position(m.Pos()).Filename]
		}
	}
	return false
}

// enumConstants gathers the string constants declared for a named type,
// deduplicated by value.
func (g *generator) enumConstants(named *types.Named) []enumConst {
	scope := named.Obj().Pkg().Scope()
	foreign := named.Obj().Pkg() != g.pkg.Types
	seen := map[string]bool{}
	var consts []enumConst
	names := scope.Names()
	sort.Strings(names)
	for _, n := range names {
		c, ok := scope.Lookup(n).(*types.Const)
		if !ok || !types.Identical(c.Type(), named) {
			continue
		}
		// An enum from another package can only switch on the constants
		// that package exports; its unexported ones are unnameable from
		// the generated file (they were previously emitted verbatim and
		// failed to compile) and decode through the copy/intern default
		// case like any other unknown value.
		if foreign && !c.Exported() {
			continue
		}
		val := constant.StringVal(c.Val())
		if val == "" || seen[val] {
			continue
		}
		seen[val] = true
		consts = append(consts, enumConst{name: g.qualify(named.Obj().Pkg()) + n, val: val})
	}
	return consts
}
