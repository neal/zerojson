package main

import (
	stdjson "encoding/json"
	"fmt"
	"strings"
)

// fuzzSeedValue returns a small, syntax-valid JSON value that reaches the
// generated read path for f. Seeds do not need to be semantically accepted by
// every custom method; their job is to put Go's mutator next to the field name
// and token shape instead of asking it to synthesize both from the lone `{}`
// seed.
func fuzzSeedValue(f fieldInfo) string {
	if f.stringOpt {
		return `"1"`
	}
	switch f.kind {
	case kString, kEnum:
		return `"x"`
	case kBool:
		return `true`
	case kInt, kUint, kFloat32, kFloat64:
		return `1`
	case kQuotedInt:
		return `"1"`
	case kTime:
		return `"2000-01-01T00:00:00Z"`
	case kUUID:
		return `"00000000-0000-0000-0000-000000000000"`
	case kByteSlice:
		return `""`
	case kSlice:
		return `[{}]`
	case kLeafSlice:
		if f.valInfo != nil {
			return `[` + fuzzSeedValue(*f.valInfo) + `]`
		}
		return `[]`
	case kMap:
		if f.valInfo != nil {
			return `{"k":` + fuzzSeedValue(*f.valInfo) + `}`
		}
		return `{}`
	case kStruct:
		return `{}`
	default:
		return `null`
	}
}

func fuzzObjectSeed(f fieldInfo) string {
	return fuzzObjectSeedValue(f, fuzzSeedValue(f))
}

func fuzzObjectSeedValue(f fieldInfo, value string) string {
	key, _ := stdjson.Marshal(f.jsonName)
	return "{" + string(key) + ":" + value + "}"
}

func fuzzAlternateSeedValue(f fieldInfo) (string, bool) {
	switch f.kind {
	case kByteSlice:
		return `[0,1,255]`, true
	case kLeafSlice:
		if f.valInfo != nil {
			if v, ok := fuzzAlternateSeedValue(*f.valInfo); ok {
				return `[` + v + `]`, true
			}
		}
	case kMap:
		if f.valInfo != nil {
			if v, ok := fuzzAlternateSeedValue(*f.valInfo); ok {
				return `{"k":` + v + `}`, true
			}
		}
	}
	return "", false
}

// emitStructFuzzSeeds gives every declared field at least one reachable seed.
// This is intentionally schema-aware: a raw byte mutator starting from `{}`
// almost never invents a complete key plus a correctly-shaped value, so
// uncommon fields would otherwise go unexercised.
func emitStructFuzzSeeds(b *strings.Builder, ti typeInfo) {
	fmt.Fprintf(b, "\tf.Add([]byte(`{}`))\n")
	for _, field := range ti.fields {
		fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeed(field))
		if alternate, ok := fuzzAlternateSeedValue(field); ok {
			// encoding/json accepts both the usual base64 string and an
			// array of byte values, including inside supported containers.
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, alternate))
		}
		if field.stringOpt || field.kind == kQuotedInt {
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, `"\u0031"`))
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, `"+00"`))
		}
		if field.kind == kString || field.kind == kEnum {
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, `"\u0078"`))
			key, _ := stdjson.Marshal(field.jsonName)
			prefix := "{" + string(key) + ":\""
			fmt.Fprintf(b, "\tf.Add(append(append([]byte(%q), byte(0xff)), []byte(%q)...))\n", prefix, `"}`)
		}
	}
}

func fuzzSequenceSeeds(ti typeInfo) []string {
	if ti.rootField != nil {
		base := "[]"
		if ti.rootField.kind == kMap {
			base = "{}"
		}
		seeds := []string{base, fuzzSeedValue(*ti.rootField), "null"}
		if alternate, ok := fuzzAlternateSeedValue(*ti.rootField); ok {
			seeds = append(seeds, alternate)
		}
		return seeds
	}
	seeds := []string{"{}"}
	for _, field := range ti.fields {
		seeds = append(seeds, fuzzObjectSeed(field))
		if alternate, ok := fuzzAlternateSeedValue(field); ok {
			seeds = append(seeds, fuzzObjectSeedValue(field, alternate))
		}
	}
	return seeds
}

func fuzzFieldArgs(fields []fieldInfo) string {
	declared := make([]string, 0, len(fields))
	for _, f := range fields {
		declared = append(declared, fmt.Sprintf("%q", f.jsonName))
	}
	if len(declared) == 0 {
		return ""
	}
	return ", " + strings.Join(declared, ", ")
}

// emitStdlibRejectClassify emits the decode-arm handling of a stdlib
// json.Unmarshal that REJECTS data: a value-class rejection (see
// zjStdlibValueError) requires zerojson to reject the same bytes too, while
// a grammar-level rejection is skipped as an allowed divergence. n is the
// type being fuzzed. Assumes `data []byte` and `t *testing.T` are in scope.
func emitStdlibRejectClassify(b *strings.Builder, n string, fields []fieldInfo) {
	fieldArgs := fuzzFieldArgs(fields)
	fmt.Fprintf(b, "\t\tif serr := json.Unmarshal(data, &seed); serr != nil {\n")
	fmt.Fprintf(b, "\t\t\tif zjStdlibValueError(data, serr%s) {\n", fieldArgs)
	fmt.Fprintf(b, "\t\t\t\tvar zj %s\n", n)
	fmt.Fprintf(b, "\t\t\t\tif zj.UnmarshalZJSON(data) == nil {\n")
	fmt.Fprintf(b, "\t\t\t\t\tt.Fatalf(\"stdlib rejected value in %%q (%%v) but zerojson accepted it\", data, serr)\n")
	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\treturn\n")
	fmt.Fprintf(b, "\t\t}\n")
}

// emitRawValueComparison compares successful decodes as Go values before any
// canonical re-encode can erase distinctions such as invalid UTF-8 or nil
// versus empty containers. Opaque custom-codec fields and case-fold-only keys
// are excluded because neither has a meaningful raw-representation oracle.
func (g *generator) emitRawValueComparison(b *strings.Builder, n string, fields []fieldInfo, root bool) {
	var differences []string
	if root {
		differences = append(differences, "!reflect.DeepEqual(&stdRaw, &zjRaw)")
	} else {
		for _, f := range fields {
			if g.fieldRawComparable(f, map[string]bool{}) {
				differences = append(differences, fmt.Sprintf("!zjRawFieldEqual(&stdRaw, &zjRaw, %q)", f.access))
			}
		}
	}
	if len(differences) == 0 {
		return
	}
	fmt.Fprintf(b, "\t\t{\n")
	fmt.Fprintf(b, "\t\t\tvar stdRaw, zjRaw %s\n", n)
	fmt.Fprintf(b, "\t\t\tstdErr := json.Unmarshal(data, &stdRaw)\n")
	fmt.Fprintf(b, "\t\t\tzjErr := zjRaw.UnmarshalZJSON(data)\n")
	fmt.Fprintf(b, "\t\t\tif stdErr == nil && zjErr == nil && !zjHasCaseFoldOnlyKey(data, []string{%s}) && (%s) {\n", strings.TrimPrefix(fuzzFieldArgs(fields), ", "), strings.Join(differences, " || "))
	fmt.Fprintf(b, "\t\t\t\tt.Fatalf(\"raw decode value divergence\\ninput: %%q\\nstdlib: %%#v\\nzerojson: %%#v\", data, &stdRaw, &zjRaw)\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t}\n")
}

// fieldRawComparable reports whether reflect.DeepEqual on this field observes
// its JSON value rather than private state owned by a delegated custom codec.
// Large structs still compare every plain field independently, so one decimal
// or Raw field doesn't blind the oracle to nil/empty differences elsewhere.
func (g *generator) fieldRawComparable(f fieldInfo, seen map[string]bool) bool {
	switch f.kind {
	case kCustomLeaf, kAppender, kMarshaler, kTextMarshaler:
		return false
	case kStruct, kSlice:
		return !g.typeContainsOpaque(f.elem, seen)
	case kLeafSlice, kMap:
		if f.valInfo != nil {
			return g.fieldRawComparable(*f.valInfo, seen)
		}
	}
	return true
}

func (g *generator) typeContainsOpaque(name string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		if ti.rootField != nil {
			return !g.fieldRawComparable(*ti.rootField, seen)
		}
		for _, f := range ti.fields {
			if !g.fieldRawComparable(f, seen) {
				return true
			}
		}
	}
	return false
}

func rawLeaf(fi *fieldInfo) bool {
	return fi != nil && fi.kind == kCustomLeaf && fi.leaf != nil &&
		fi.leaf.appendPkg == runtimePath && fi.leaf.appendFn == "AppendRaw"
}

func (g *generator) fieldContainsRaw(f fieldInfo, seen map[string]bool) bool {
	if rawLeaf(&f) {
		return true
	}
	switch f.kind {
	case kStruct, kSlice:
		return g.typeContainsRaw(f.elem, seen)
	case kLeafSlice, kMap:
		if rawLeaf(f.valInfo) {
			return true
		}
		if f.valInfo != nil && f.valInfo.kind == kStruct {
			return g.typeContainsRaw(f.valInfo.elem, seen)
		}
	}
	return false
}

func (g *generator) typeContainsRaw(name string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		if ti.rootField != nil {
			return g.fieldContainsRaw(*ti.rootField, seen)
		}
		for _, f := range ti.fields {
			if g.fieldContainsRaw(f, seen) {
				return true
			}
		}
	}
	return false
}

// emitRawGraphRoundTrip emits the oracle for type graphs containing
// zerojson.Raw. encoding/json treats that named []byte as base64 data rather
// than as a captured JSON token, so typed stdlib comparisons are invalid for
// these graphs. Successful zerojson decodes must instead emit valid JSON and
// reach a byte-stable fixed point after one decode/encode cycle.
func emitRawGraphRoundTrip(b *strings.Builder, n string) {
	fmt.Fprintf(b, "\t\tvar viaZJ %s\n", n)
	fmt.Fprintf(b, "\t\tif err := viaZJ.UnmarshalZJSON(data); err != nil { return }\n")
	fmt.Fprintf(b, "\t\tenc, err := viaZJ.AppendZJSON(nil)\n")
	fmt.Fprintf(b, "\t\tif err != nil { return }\n")
	fmt.Fprintf(b, "\t\tif !json.Valid(enc) { t.Fatalf(\"zerojson emitted invalid JSON after Raw decode: %%q\", enc) }\n")
	fmt.Fprintf(b, "\t\tvar again %s\n", n)
	fmt.Fprintf(b, "\t\tif err := again.UnmarshalZJSON(enc); err != nil { t.Fatalf(\"zerojson could not re-decode Raw output %%q: %%v\", enc, err) }\n")
	fmt.Fprintf(b, "\t\tencAgain, err := again.AppendZJSON(nil)\n")
	fmt.Fprintf(b, "\t\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(b, "\t\tif !bytes.Equal(enc, encAgain) { t.Fatalf(\"Raw round-trip did not reach a fixed point\\nfirst:  %%q\\nsecond: %%q\", enc, encAgain) }\n")
}

// emitDecodeErrorDirection emits the decode-error ORACLE DIRECTION arm:
// zerojson-error ⇒ stdlib-error. If zerojson REJECTS the raw fuzz input but
// encoding/json ACCEPTS it, zerojson is stricter than stdlib — a violation
// unless allowlisted (zjDecodeStricterAllowed). This is the complement of
// emitStdlibRejectClassify's stdlib-error ⇒ zerojson-error (value-class)
// direction; together they pin that neither decoder is unexpectedly stricter
// than the other. Runs on the raw input (before stdlib canonicalization), so
// it sees strictness the canonicalization would otherwise mask.
func emitDecodeErrorDirection(b *strings.Builder, n string) {
	fmt.Fprintf(b, "\t\t{\n")
	fmt.Fprintf(b, "\t\t\tvar zjRaw %s\n", n)
	fmt.Fprintf(b, "\t\t\tif zjErr := zjRaw.UnmarshalZJSON(data); zjErr != nil && !zjDecodeStricterAllowed(data) {\n")
	fmt.Fprintf(b, "\t\t\t\tvar stdRaw %s\n", n)
	fmt.Fprintf(b, "\t\t\t\tif json.Unmarshal(data, &stdRaw) == nil {\n")
	fmt.Fprintf(b, "\t\t\t\t\tt.Fatalf(\"zerojson rejected %%q that encoding/json accepted: %%v\", data, zjErr)\n")
	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t}\n")
}

// generateTests emits a differential fuzz test per generated type. The
// fuzzers need no hand-written samples: they explore inputs and compare
// zerojson's decode against encoding/json (via normalized re-encode),
// and check that zerojson's own output round-trips and is stdlib-parseable.
func (g *generator) generateTests() string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by zerojsongen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", g.pkg.Types.Name())
	// The runtime import (the directly-called fallback decoder) is used only
	// by struct-root fuzzers. reflect is used by that comparison too, and by
	// raw value comparisons for roots that don't contain zerojson.Raw.
	hasStruct := false
	needsReflect := false
	for _, ti := range g.types {
		if ti.rootField == nil {
			hasStruct = true
			needsReflect = true
		} else if g.fieldRawComparable(*ti.rootField, map[string]bool{}) {
			needsReflect = true
		}
		if g.pool && g.explicit[ti.name] {
			needsReflect = true
		}
	}
	fmt.Fprintf(&b, "import (\n\t\"bytes\"\n\t\"encoding/json\"\n\t\"errors\"\n")
	if needsReflect {
		fmt.Fprintf(&b, "\t\"reflect\"\n")
	}
	fmt.Fprintf(&b, "\t\"strings\"\n\t\"testing\"\n")
	if hasStruct {
		fmt.Fprintf(&b, "\n\t%q\n", runtimePath)
	}
	fmt.Fprintf(&b, ")\n\n")
	if needsReflect {
		// Access raw fields through reflection rather than emitted selector
		// expressions: a promoted field may sit behind a nil embedded pointer,
		// and evaluating a direct a.Embedded.Field selector would panic in the
		// fuzz oracle before it could report the allocation-shape divergence.
		fmt.Fprintf(&b, "func zjRawFieldEqual(a, other any, path string) bool {\n")
		fmt.Fprintf(&b, "\tav, aok := zjRawFieldValue(reflect.ValueOf(a), strings.Split(path, \".\"))\n")
		fmt.Fprintf(&b, "\tbv, bok := zjRawFieldValue(reflect.ValueOf(other), strings.Split(path, \".\"))\n")
		fmt.Fprintf(&b, "\treturn aok == bok && reflect.DeepEqual(av, bv)\n")
		fmt.Fprintf(&b, "}\n\n")
		fmt.Fprintf(&b, "func zjRawFieldValue(v reflect.Value, path []string) (any, bool) {\n")
		fmt.Fprintf(&b, "\tfor _, name := range path {\n")
		fmt.Fprintf(&b, "\t\tfor v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) { if v.IsNil() { return nil, false }; v = v.Elem() }\n")
		fmt.Fprintf(&b, "\t\tif !v.IsValid() || v.Kind() != reflect.Struct { return nil, false }\n")
		fmt.Fprintf(&b, "\t\tv = v.FieldByName(name)\n")
		fmt.Fprintf(&b, "\t}\n")
		fmt.Fprintf(&b, "\tif !v.IsValid() || !v.CanInterface() { return nil, false }\n")
		fmt.Fprintf(&b, "\treturn v.Interface(), true\n")
		fmt.Fprintf(&b, "}\n\n")
	}
	// zjStdlibValueError classifies a stdlib json.Unmarshal failure: a
	// grammar-level SyntaxError is an allowed divergence (zerojson's trusted
	// path is deliberately lenient about leading zeros, raw control bytes in
	// values, etc.), but a VALUE-class failure — a number that does not fit
	// or isn't valid for its target type (UnmarshalTypeError whose value
	// reads "number ...", or an invalid bare json:",string" value) — is one
	// zerojson has NO leniency for and must reject too. A string-into-number
	// or bare-into-,string mismatch must be rejected by both decoders.
	fmt.Fprintf(&b, "func zjStdlibValueError(data []byte, err error, fields ...string) bool {\n")
	fmt.Fprintf(&b, "\tif zjHasCaseFoldOnlyKey(data, fields) { return false }\n")
	fmt.Fprintf(&b, "\tvar ute *json.UnmarshalTypeError\n")
	fmt.Fprintf(&b, "\tif errors.As(err, &ute) {\n")
	fmt.Fprintf(&b, "\t\treturn strings.HasPrefix(ute.Value, \"number\")\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\treturn strings.Contains(err.Error(), \"invalid use of ,string struct tag\")\n")
	fmt.Fprintf(&b, "}\n\n")
	// encoding/json matches struct keys case-insensitively while zerojson's
	// documented contract is exact-key matching. If stdlib reports a value
	// error beneath a key that exists only as a case-folded spelling, zerojson
	// correctly treats that member as unknown and may accept it. Walk an
	// interface decode solely in the fuzz oracle to recognize that narrow
	// condition; production decoding remains allocation-free.
	fmt.Fprintf(&b, "func zjHasCaseFoldOnlyKey(data []byte, fields []string) bool {\n")
	fmt.Fprintf(&b, "\tif len(fields) == 0 { return false }\n")
	fmt.Fprintf(&b, "\tvar v any\n")
	// UseNumber keeps syntactically valid, very large JSON numbers as text.
	// Decoding into any with json.Unmarshal would first coerce them to float64
	// and fail before the oracle could inspect the surrounding key spelling.
	fmt.Fprintf(&b, "\tdec := json.NewDecoder(bytes.NewReader(data))\n")
	fmt.Fprintf(&b, "\tdec.UseNumber()\n")
	fmt.Fprintf(&b, "\tif dec.Decode(&v) != nil { return false }\n")
	fmt.Fprintf(&b, "\tfor _, field := range fields { exact, folded := zjFindKeySpellings(v, field); if folded && !exact { return true } }\n")
	fmt.Fprintf(&b, "\treturn false\n")
	fmt.Fprintf(&b, "}\n\n")
	fmt.Fprintf(&b, "func zjFindKeySpellings(v any, field string) (exact, folded bool) {\n")
	fmt.Fprintf(&b, "\tswitch x := v.(type) {\n")
	fmt.Fprintf(&b, "\tcase map[string]any:\n")
	fmt.Fprintf(&b, "\t\tfor k, child := range x {\n")
	fmt.Fprintf(&b, "\t\t\tif k == field { exact = true } else if strings.EqualFold(k, field) { folded = true }\n")
	fmt.Fprintf(&b, "\t\t\te, f := zjFindKeySpellings(child, field); exact = exact || e; folded = folded || f\n")
	fmt.Fprintf(&b, "\t\t}\n")
	fmt.Fprintf(&b, "\tcase []any:\n")
	fmt.Fprintf(&b, "\t\tfor _, child := range x { e, f := zjFindKeySpellings(child, field); exact = exact || e; folded = folded || f }\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\treturn exact, folded\n")
	fmt.Fprintf(&b, "}\n\n")
	// Decimal libraries (shopspring-style) parse huge exponents compactly
	// but expand them to millions of digits when re-encoding — a property
	// of the decimal type shared by every codec, so the fuzzers skip such
	// inputs.
	// zjDecodeStricterAllowed is the allowlist hook for the decode-error
	// ORACLE DIRECTION (zerojson-error ⇒ stdlib-error): it reports whether
	// zerojson may reject `data` that encoding/json ACCEPTS as a DOCUMENTED
	// deliberate strictness. zerojson's divergences from stdlib are otherwise
	// all in the lenient direction (it accepts leading zeros, raw control
	// bytes in declared-field values, invalid escapes — MORE than stdlib), so
	// this returns false for the shapes generated here. []byte's uncommon
	// array-of-numbers form is seeded explicitly and is supported, so it needs
	// no exception here.
	// Trailing data is NOT stricter than stdlib (encoding/json rejects it
	// too), so it is deliberately not listed.
	fmt.Fprintf(&b, "func zjDecodeStricterAllowed(data []byte) bool { _ = data; return false }\n\n")
	fmt.Fprintf(&b, "func zjFuzzHugeExponent(data []byte) bool {\n")
	fmt.Fprintf(&b, "\tfor i, c := range data {\n")
	fmt.Fprintf(&b, "\t\tif c != 'e' && c != 'E' {\n\t\t\tcontinue\n\t\t}\n")
	fmt.Fprintf(&b, "\t\tj := i + 1\n")
	fmt.Fprintf(&b, "\t\tif j < len(data) && (data[j] == '+' || data[j] == '-') {\n\t\t\tj++\n\t\t}\n")
	fmt.Fprintf(&b, "\t\tn := 0\n")
	fmt.Fprintf(&b, "\t\tfor j < len(data) && data[j] >= '0' && data[j] <= '9' {\n\t\t\tn++\n\t\t\tj++\n\t\t}\n")
	fmt.Fprintf(&b, "\t\tif n >= 6 {\n\t\t\treturn true\n\t\t}\n")
	fmt.Fprintf(&b, "\t}\n\treturn false\n}\n\n")
	for _, ti := range g.types {
		n := ti.name
		if ti.rootField != nil {
			g.emitRootFuzz(&b, ti)
			g.emitPoolSequenceFuzz(&b, ti)
			continue
		}
		fmt.Fprintf(&b, "func Fuzz%sZJSON(f *testing.F) {\n", n)
		emitStructFuzzSeeds(&b, ti)
		fmt.Fprintf(&b, "\tf.Fuzz(func(t *testing.T, data []byte) {\n")
		fmt.Fprintf(&b, "\t\tif zjFuzzHugeExponent(data) { return }\n")
		g.emitRawValueComparison(&b, n, ti.fields, false)
		if g.typeContainsRaw(ti.name, map[string]bool{}) {
			emitRawGraphRoundTrip(&b, n)
			fmt.Fprintf(&b, "\t})\n}\n\n")
			g.emitPoolSequenceFuzz(&b, ti)
			continue
		}
		emitDecodeErrorDirection(&b, n)
		// Decode arm: canonicalize via stdlib, then zerojson must match.
		// When stdlib REJECTS data, a value-class failure (see
		// zjStdlibValueError) additionally requires zerojson to reject it too
		// — grammar leniency stays an allowed divergence.
		fmt.Fprintf(&b, "\t\tvar seed %s\n", n)
		emitStdlibRejectClassify(&b, n, ti.fields)
		fmt.Fprintf(&b, "\t\tcanon, err := json.Marshal(&seed)\n")
		fmt.Fprintf(&b, "\t\tif err != nil { return }\n")
		fmt.Fprintf(&b, "\t\tvar viaStd %s\n", n)
		fmt.Fprintf(&b, "\t\tif json.Unmarshal(canon, &viaStd) != nil { return }\n")
		fmt.Fprintf(&b, "\t\tvar viaZJ %s\n", n)
		fmt.Fprintf(&b, "\t\tif err := viaZJ.UnmarshalZJSON(canon); err != nil {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"zerojson rejected canonical %%q: %%v\", canon, err)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t\tx, _ := json.Marshal(&viaStd)\n")
		fmt.Fprintf(&b, "\t\ty, _ := json.Marshal(&viaZJ)\n")
		fmt.Fprintf(&b, "\t\tif !bytes.Equal(x, y) {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"decode divergence\\ninput: %%s\\nstdlib: %%s\\nzerojson: %%s\", canon, x, y)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		// Ordered-vs-fallback arm: the speculative ordered fast path
		// (exercised via the public UnmarshalZJSON call above) must
		// produce results identical to the fallback decoder called
		// directly — both are in this package, so zjDecodeFallback<T>
		// and zjArena<T> (always generated, regardless of whether this
		// type is ordered-eligible) are reachable here.
		fmt.Fprintf(&b, "\t\tvar viaFallback %s\n", n)
		fmt.Fprintf(&b, "\t\tlFallback := zerojson.NewLexer(canon, false)\n")
		fmt.Fprintf(&b, "\t\t%s(&lFallback, &viaFallback, &%s{})\n", fallbackDecodeName(n), arenaName(n))
		// Finish() lives in the public wrapper, not in zjDecodeFallback<T>
		// itself — applied here too so this fallback-only comparison uses
		// the same entry-point contract the public path does (canon never
		// has trailing content in practice, so this is a no-op today, but
		// keeps the comparison honest if that ever changes).
		fmt.Fprintf(&b, "\t\tlFallback.Finish()\n")
		fmt.Fprintf(&b, "\t\tif err := lFallback.Err(); err != nil {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"fallback-only decode rejected canonical %%q: %%v\", canon, err)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t\tif !reflect.DeepEqual(&viaFallback, &viaZJ) {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"ordered-vs-fallback divergence\\ninput: %%s\\nfallback-only: %%+v\\npublic (ordered+fallback): %%+v\", canon, &viaFallback, &viaZJ)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		// Encode arm: zerojson output must re-decode and be stdlib-parseable.
		fmt.Fprintf(&b, "\t\tenc, err := viaZJ.AppendZJSON(nil)\n")
		fmt.Fprintf(&b, "\t\tif err != nil { return }\n")
		fmt.Fprintf(&b, "\t\tvar reZJ %s\n", n)
		fmt.Fprintf(&b, "\t\tif err := reZJ.UnmarshalZJSON(enc); err != nil {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"zerojson could not re-decode its own output %%q: %%v\", enc, err)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t\tvar reStd %s\n", n)
		fmt.Fprintf(&b, "\t\tif err := json.Unmarshal(enc, &reStd); err != nil {\n")
		fmt.Fprintf(&b, "\t\t\tt.Fatalf(\"stdlib rejected zerojson output %%q: %%v\", enc, err)\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t})\n}\n\n")
		g.emitPoolSequenceFuzz(&b, ti)
	}
	return b.String()
}

// emitPoolSequenceFuzz generates the stateful complement to each stateless
// type fuzzer. Four operations run through one reusable Decoder with copy and
// borrow mode selected independently for each operation, including invalid
// inputs between valid ones. Each result/error is compared immediately with a
// fresh same-mode decode. Copy-mode input is then scribbled and the value is
// compared again, detecting accidental aliases as well as stale pointers,
// omitted-field residue, slice/map ghosts, and arena poisoning across mode
// transitions.
func (g *generator) emitPoolSequenceFuzz(b *strings.Builder, ti typeInfo) {
	if !g.pool || !g.explicit[ti.name] {
		return
	}
	n := ti.name
	seeds := fuzzSequenceSeeds(ti)
	base := "{}"
	if ti.rootField != nil && ti.rootField.kind != kMap {
		base = "[]"
	}
	fmt.Fprintf(b, "func Fuzz%sZJSONSequence(f *testing.F) {\n", n)
	for i, seed := range seeds {
		next := seeds[(i+1)%len(seeds)]
		third := seeds[(i+2)%len(seeds)]
		fmt.Fprintf(b, "\tf.Add([]byte(%q), []byte(%q), []byte(%q), []byte(%q), uint8(0x0))\n", seed, base, next, third)
		fmt.Fprintf(b, "\tf.Add([]byte(%q), []byte(%q), []byte(%q), []byte(%q), uint8(0xf))\n", base, seed, "{", next)
		fmt.Fprintf(b, "\tf.Add([]byte(%q), []byte(%q), []byte(%q), []byte(%q), uint8(0x5))\n", seed, next, base, "[")
		fmt.Fprintf(b, "\tf.Add([]byte(%q), []byte(%q), []byte(%q), []byte(%q), uint8(0xa))\n", "{", seed, third, base)
	}
	fmt.Fprintf(b, "\tf.Fuzz(func(t *testing.T, first, second, third, fourth []byte, modeMask uint8) {\n")
	fmt.Fprintf(b, "\t\tinputs := [4][]byte{first, second, third, fourth}\n")
	fmt.Fprintf(b, "\t\tfor _, data := range inputs { if zjFuzzHugeExponent(data) { return } }\n")
	fmt.Fprintf(b, "\t\tvar d %sDecoder\n", n)
	fmt.Fprintf(b, "\t\tfor op, data := range inputs {\n")
	fmt.Fprintf(b, "\t\t\tborrow := modeMask&(1<<uint(op)) != 0\n")
	fmt.Fprintf(b, "\t\t\tpooledInput := append([]byte(nil), data...)\n")
	fmt.Fprintf(b, "\t\t\tfreshInput := append([]byte(nil), data...)\n")
	fmt.Fprintf(b, "\t\t\tvar got, want %s\n", n)
	fmt.Fprintf(b, "\t\t\tvar gotErr, wantErr error\n")
	fmt.Fprintf(b, "\t\t\tif borrow { gotErr, wantErr = d.DecodeBorrow(pooledInput, &got), want.UnmarshalZJSONBorrow(freshInput) } else { gotErr, wantErr = d.Decode(pooledInput, &got), want.UnmarshalZJSON(freshInput) }\n")
	fmt.Fprintf(b, "\t\t\tif (gotErr != nil) != (wantErr != nil) { t.Fatalf(\"op=%%d borrow=%%v mask=0x%%x: decode error mismatch\\ninput: %%q\\npooled: %%v\\nfresh: %%v\", op, borrow, modeMask, data, gotErr, wantErr) }\n")
	fmt.Fprintf(b, "\t\t\tif gotErr == nil && !reflect.DeepEqual(&got, &want) { t.Fatalf(\"op=%%d borrow=%%v mask=0x%%x: pooled result differs from fresh\\ninput: %%q\\npooled: %%#v\\nfresh: %%#v\", op, borrow, modeMask, data, &got, &want) }\n")
	fmt.Fprintf(b, "\t\t\tif gotErr == nil && !borrow {\n")
	fmt.Fprintf(b, "\t\t\t\tfor i := range pooledInput { pooledInput[i] = 0xa5 }\n")
	fmt.Fprintf(b, "\t\t\t\tif !reflect.DeepEqual(&got, &want) { t.Fatalf(\"op=%%d mask=0x%%x: copy-mode result aliases caller input after scribble\\ninput: %%q\\npooled: %%#v\\nfresh: %%#v\", op, modeMask, data, &got, &want) }\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t})\n}\n\n")
}

// emitRootFuzz emits a differential fuzzer for a slice/map root type. It
// has a single decode path (no ordered/fallback split at the root), so it
// omits the ordered-vs-fallback arm the struct fuzzer carries: canonicalize
// through encoding/json, require zerojson's decode to match (via re-encode,
// which also normalizes a map root's range order against stdlib's sorted
// key order), then require zerojson's own output to re-decode through both
// zerojson and stdlib.
func (g *generator) emitRootFuzz(b *strings.Builder, ti typeInfo) {
	n := ti.name
	seed := "[]"
	if ti.rootField.kind == kMap {
		seed = "{}"
	}
	fmt.Fprintf(b, "func Fuzz%sZJSON(f *testing.F) {\n", n)
	fmt.Fprintf(b, "\tf.Add([]byte(`%s`))\n", seed)
	fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzSeedValue(*ti.rootField))
	if alternate, ok := fuzzAlternateSeedValue(*ti.rootField); ok {
		fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", alternate)
	}
	fmt.Fprintf(b, "\tf.Add([]byte(`null`))\n")
	fmt.Fprintf(b, "\tf.Fuzz(func(t *testing.T, data []byte) {\n")
	fmt.Fprintf(b, "\t\tif zjFuzzHugeExponent(data) { return }\n")
	if g.typeContainsRaw(ti.name, map[string]bool{}) {
		emitRawGraphRoundTrip(b, n)
		fmt.Fprintf(b, "\t})\n}\n\n")
		return
	}
	if g.fieldRawComparable(*ti.rootField, map[string]bool{}) {
		g.emitRawValueComparison(b, n, nil, true)
	}
	emitDecodeErrorDirection(b, n)
	fmt.Fprintf(b, "\t\tvar seed %s\n", n)
	emitStdlibRejectClassify(b, n, nil)
	fmt.Fprintf(b, "\t\tcanon, err := json.Marshal(&seed)\n")
	fmt.Fprintf(b, "\t\tif err != nil { return }\n")
	fmt.Fprintf(b, "\t\tvar viaStd %s\n", n)
	fmt.Fprintf(b, "\t\tif json.Unmarshal(canon, &viaStd) != nil { return }\n")
	fmt.Fprintf(b, "\t\tvar viaZJ %s\n", n)
	fmt.Fprintf(b, "\t\tif err := viaZJ.UnmarshalZJSON(canon); err != nil {\n")
	fmt.Fprintf(b, "\t\t\tt.Fatalf(\"zerojson rejected canonical %%q: %%v\", canon, err)\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tx, _ := json.Marshal(&viaStd)\n")
	fmt.Fprintf(b, "\t\ty, _ := json.Marshal(&viaZJ)\n")
	fmt.Fprintf(b, "\t\tif !bytes.Equal(x, y) {\n")
	fmt.Fprintf(b, "\t\t\tt.Fatalf(\"decode divergence\\ninput: %%s\\nstdlib: %%s\\nzerojson: %%s\", canon, x, y)\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tenc, err := viaZJ.AppendZJSON(nil)\n")
	fmt.Fprintf(b, "\t\tif err != nil { return }\n")
	fmt.Fprintf(b, "\t\tvar reZJ %s\n", n)
	fmt.Fprintf(b, "\t\tif err := reZJ.UnmarshalZJSON(enc); err != nil {\n")
	fmt.Fprintf(b, "\t\t\tt.Fatalf(\"zerojson could not re-decode its own output %%q: %%v\", enc, err)\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tvar reStd %s\n", n)
	fmt.Fprintf(b, "\t\tif err := json.Unmarshal(enc, &reStd); err != nil {\n")
	fmt.Fprintf(b, "\t\t\tt.Fatalf(\"stdlib rejected zerojson output %%q: %%v\", enc, err)\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t})\n}\n\n")
}
