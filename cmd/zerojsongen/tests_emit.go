package main

import (
	stdjson "encoding/json"
	"fmt"
	"go/ast"
	"strings"
)

// Go rejects Fuzz followed by a lowercase rune. Prefix every unexported
// type (including underscore-prefixed names) to keep the mapping injective:
// hidden, Hidden, and _hidden must never share a generated fuzz name.
func fuzzTypeName(name string) string {
	if ast.IsExported(name) {
		return name
	}
	return "_" + name
}

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
	case kInt, kUint, kFloat32, kFloat64, kJSONNumber:
		return `1`
	case kQuotedInt:
		return `"1"`
	case kTime:
		return `"2000-01-01T00:00:00Z"`
	case kUUID:
		return `"00000000-0000-0000-0000-000000000000"`
	case kByteSlice:
		return `""`
	case kCustomLeaf:
		if isRawLeaf(f) {
			return `{ "x": [1, "y"] }`
		}
		return `null`
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

// fuzzSecondSeedValue returns a valid value for f distinguishable from
// fuzzSeedValue's, for duplicate-member seeds; false when the kind has no
// second cheap spelling worth seeding.
func fuzzSecondSeedValue(f fieldInfo) (string, bool) {
	if f.stringOpt {
		return `"2"`, true
	}
	switch f.kind {
	case kString, kEnum:
		return `"y"`, true
	case kBool:
		return `false`, true
	case kInt, kUint, kFloat32, kFloat64, kJSONNumber:
		return `2`, true
	case kQuotedInt:
		return `"2"`, true
	case kTime:
		return `"2001-02-03T04:05:06Z"`, true
	case kUUID:
		return `"11111111-1111-1111-1111-111111111111"`, true
	case kLeafSlice, kSlice:
		return `[]`, true
	case kMap:
		return `{}`, true
	}
	return "", false
}

// fuzzInvalidSeedValue returns a syntactically valid JSON value that the
// field's parser or custom method is expected to reject, for the
// stdlib-rejects arm; false for kinds with no such cheap spelling.
func fuzzInvalidSeedValue(f fieldInfo) (string, bool) {
	switch f.kind {
	case kTime, kUUID, kTextMarshaler, kMarshaler, kAppender:
		return `"bad"`, true
	case kCustomLeaf:
		if !isRawLeaf(f) {
			return `"bad"`, true
		}
	case kByteSlice:
		return `"!!"`, true
	case kQuotedInt:
		return `"bad"`, true
	case kInt, kUint, kFloat32, kFloat64:
		if f.stringOpt {
			return `"bad"`, true
		}
		return `"1"`, true
	case kBool:
		return `"true"`, true
	case kString, kEnum:
		return `1`, true
	case kLeafSlice, kMap:
		if f.valInfo != nil {
			if v, ok := fuzzInvalidSeedValue(*f.valInfo); ok {
				if f.kind == kMap {
					return `{"k":` + v + `}`, true
				}
				return `[` + v + `]`, true
			}
		}
	}
	return "", false
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
	case kCustomLeaf:
		if isRawLeaf(f) {
			return `null`, true
		}
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
	// Trailing data after the document: encoding/json's "after top-level
	// value" SyntaxError, which Finish must mirror.
	fmt.Fprintf(b, "\tf.Add([]byte(`{} x`))\n")
	for _, field := range ti.fields {
		fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeed(field))
		// A literal the field's own parser or method should reject: the
		// stdlib-rejects arm then requires zerojson's decode to fail too
		// (a dropped AddError after UnmarshalText, or a timestamp parser
		// that swallows its error, is invisible without such a seed).
		if invalid, ok := fuzzInvalidSeedValue(field); ok {
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, invalid))
		}
		// A duplicated member with two distinguishable values: last wins in
		// both decoders, and Go's mutator does not invent a repeated key
		// on its own.
		if second, ok := fuzzSecondSeedValue(field); ok {
			key, _ := stdjson.Marshal(field.jsonName)
			fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", "{"+string(key)+":"+fuzzSeedValue(field)+","+string(key)+":"+second+"}")
		}
		// Integer boundaries just outside each width: encoding/json rejects
		// them with an UnmarshalTypeError, which the stdlib-rejects arm
		// requires zerojson to mirror; the mutator rarely lands on them.
		if !field.stringOpt {
			switch field.kind {
			case kInt:
				for _, v := range []string{"128", "32768", "2147483648", "9223372036854775808", "-129", "-9223372036854775809"} {
					fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, v))
				}
			case kUint:
				for _, v := range []string{"256", "65536", "4294967296", "18446744073709551616", "-1"} {
					fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzObjectSeedValue(field, v))
				}
			}
		}
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

// emitStdlibRejectClassify emits the decode-arm handling of a stdlib
// json.Unmarshal that REJECTS data: syntax and value rejections require
// zerojson to reject the same bytes too. n is the
// type being fuzzed. Assumes `data []byte` and `t *testing.T` are in scope.
func emitStdlibRejectClassify(b *strings.Builder, n string) {
	fmt.Fprintf(b, "\t\tif serr := json.Unmarshal(oracleData, &seed); serr != nil {\n")
	// Mirror syntax and value rejections. The two documented exceptions —
	// a null on a value-typed
	// quoted-int64 marker, and an escaped spelling of a direct leaf that
	// zerojson unescapes before parsing — are exempted on the raw input.
	fmt.Fprintf(b, "\t\t\tif zjStdlibMustMirror(serr) && !zjNullOnValueUnmarshaler%s(data) && !zjEscapedDirectLeaf%s(data) {\n", n, n)
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
// versus empty containers. Opaque custom-codec fields are excluded; case-fold
// differences are normalized only on the stdlib side using the schema.
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
	fmt.Fprintf(b, "\t\t\tstdErr := json.Unmarshal(oracleData, &stdRaw)\n")
	fmt.Fprintf(b, "\t\t\tzjErr := zjRaw.UnmarshalZJSON(data)\n")
	fmt.Fprintf(b, "\t\t\tif stdErr == nil && zjErr == nil && (%s) {\n", strings.Join(differences, " || "))
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
	case kCustomLeaf:
		// zerojson.Raw stores the exact value bytes and, through its own
		// UnmarshalJSON, stdlib stores the same span — comparable. Other
		// custom leaves own opaque state.
		return isRawLeaf(f)
	case kAppender, kMarshaler, kTextMarshaler:
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
// zerojson.Raw, in addition to (not instead of) the independent stdlib
// comparisons. Raw implements MarshalJSON/UnmarshalJSON, so both decoders
// capture the same value span. Round-trip equality is semantic: stdlib
// marshaling normalizes map order and Raw whitespace before comparison.
func emitRawGraphRoundTrip(b *strings.Builder, n string) {
	fmt.Fprintf(b, `
		{
			var viaZJ %[1]s
			if err := viaZJ.UnmarshalZJSON(data); err == nil {
				if enc, err := viaZJ.AppendZJSON(nil); err == nil {
					if !json.Valid(enc) { t.Fatalf("zerojson emitted invalid JSON after Raw decode: %%q", enc) }
					var again %[1]s
					if err := again.UnmarshalZJSON(enc); err != nil { t.Fatalf("zerojson could not re-decode Raw output %%q: %%v", enc, err) }
					before, err := json.Marshal(&viaZJ)
					if err != nil { t.Fatal(err) }
					after, err := json.Marshal(&again)
					if err != nil { t.Fatal(err) }
					if !bytes.Equal(before, after) { t.Fatalf("Raw round-trip changed value\nfirst: %%q\nsecond: %%q", before, after) }
				}
			}
		}
`, n)
}

// emitTrustedCheck emits zjCheckTrusted<T> under -trusted. It needs no
// stdlib oracle, so it runs for every fuzz input, before any oracle check
// can return: input zerojson.Valid accepts must decode exactly as
// UnmarshalZJSON decodes it, into storage of its own (the trusted decode
// reads a private copy that is overwritten before the comparison), and any
// other input may decode or fail but must not panic. The fuzzer's own
// timeout catches a decode that never returns.
func (g *generator) emitTrustedCheck(b *strings.Builder, n string) {
	if !g.trusted {
		return
	}
	fmt.Fprintf(b, "func zjCheckTrusted%s(t *testing.T, data []byte) {\n", n)
	fmt.Fprintf(b, "\tinput := append([]byte(nil), data...)\n")
	fmt.Fprintf(b, "\tvar trusted %s\n", n)
	fmt.Fprintf(b, "\ttrustedErr := trusted.UnmarshalZJSONTrusted(input)\n")
	fmt.Fprintf(b, "\tif zerojson.Valid(data) != nil { return }\n")
	fmt.Fprintf(b, "\tfor i := range input { input[i] = 'x' }\n")
	fmt.Fprintf(b, "\tvar checked %s\n", n)
	fmt.Fprintf(b, "\tcheckedErr := checked.UnmarshalZJSON(data)\n")
	fmt.Fprintf(b, "\tif (trustedErr == nil) != (checkedErr == nil) || trustedErr != nil && trustedErr.Error() != checkedErr.Error() {\n")
	fmt.Fprintf(b, "\t\tt.Fatalf(\"trusted decode error %%v, checked decode error %%v, for %%q\", trustedErr, checkedErr, data)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tif !reflect.DeepEqual(&trusted, &checked) {\n")
	fmt.Fprintf(b, "\t\tt.Fatalf(\"trusted decode differs for %%q\\ntrusted: %%+v\\nchecked: %%+v\", data, &trusted, &checked)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// emitTrustedCall runs zjCheckTrusted<T> from a fuzz target under -trusted.
func (g *generator) emitTrustedCall(b *strings.Builder, n string) {
	if g.trusted {
		fmt.Fprintf(b, "\t\tzjCheckTrusted%s(t, data)\n", n)
	}
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
	fmt.Fprintf(b, "\t\t\tif zjErr := zjRaw.UnmarshalZJSON(data); zjErr != nil && !zjDecodeStricterAllowed%s(data) {\n", n)
	fmt.Fprintf(b, "\t\t\t\tvar stdRaw %s\n", n)
	fmt.Fprintf(b, "\t\t\t\tif json.Unmarshal(oracleData, &stdRaw) == nil {\n")
	fmt.Fprintf(b, "\t\t\t\t\tt.Fatalf(\"zerojson rejected %%q that encoding/json accepted: %%v\", data, zjErr)\n")
	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t}\n")
}

// emitNoOracleCall emits the guard that hands a v2-backed run to the
// oracle-free checks. See emitNoOracleFuzz.
func emitNoOracleCall(b *strings.Builder, n string, invalidTag bool) {
	if !invalidTag {
		return
	}
	fmt.Fprintf(b, "\t\tif zjStdlibJSONV2 {\n")
	fmt.Fprintf(b, "\t\t\tzjFuzzNoOracle%s(t, data)\n", n)
	fmt.Fprintf(b, "\t\t\treturn\n")
	fmt.Fprintf(b, "\t\t}\n")
}

// emitNoOracleFuzz emits the oracle-free half of a fuzz target, used for a
// type whose graph carries a field tag naming it with characters classic
// encoding/json rejects (the Go field name is used instead). Go 1.27's
// v2-backed encoding/json truncates such a name, so the two implementations
// read and write DIFFERENT members for that field — a documented drift that
// invalidates every stdlib comparison for the type, and only those. What
// does not involve the oracle still runs on the v2-backed toolchain:
// zerojson must not panic on arbitrary input, its own output must be valid
// JSON, must re-decode, and (for struct roots) the speculative ordered fast
// path and the fallback decoder must agree on it.
func (g *generator) emitNoOracleFuzz(b *strings.Builder, ti typeInfo) {
	n := ti.name
	fmt.Fprintf(b, "// zjFuzzNoOracle%s runs the checks that do not compare against\n", n)
	fmt.Fprintf(b, "// encoding/json: %s has a field whose tag name classic encoding/json\n", n)
	fmt.Fprintf(b, "// rejects (the Go field name is used), and Go 1.27's v2-backed\n")
	fmt.Fprintf(b, "// implementation truncates it instead — a documented drift, so only the\n")
	fmt.Fprintf(b, "// stdlib oracle is dropped for this type there, not the whole target.\n")
	fmt.Fprintf(b, "func zjFuzzNoOracle%s(t *testing.T, data []byte) {\n", n)
	fmt.Fprintf(b, "\tvar viaZJ %s\n", n)
	fmt.Fprintf(b, "\tif err := viaZJ.UnmarshalZJSON(data); err != nil { return }\n")
	fmt.Fprintf(b, "\tenc, err := viaZJ.AppendZJSON(nil)\n")
	fmt.Fprintf(b, "\tif err != nil { return }\n")
	// json.Valid is a pure grammar check: it reads no struct tags, so it is
	// unaffected by the drift that disqualifies the typed comparisons.
	fmt.Fprintf(b, "\tif !json.Valid(enc) { t.Fatalf(\"zerojson emitted invalid JSON %%q\", enc) }\n")
	fmt.Fprintf(b, "\tvar reZJ %s\n", n)
	fmt.Fprintf(b, "\tif err := reZJ.UnmarshalZJSON(enc); err != nil {\n")
	fmt.Fprintf(b, "\t\tt.Fatalf(\"zerojson could not re-decode its own output %%q: %%v\", enc, err)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tbefore, beforeErr := json.Marshal(&viaZJ)\n")
	fmt.Fprintf(b, "\tafter, afterErr := json.Marshal(&reZJ)\n")
	fmt.Fprintf(b, "\tif beforeErr == nil && afterErr == nil { zjAssertJSONEqual(t, before, after) }\n")
	if ti.rootField != nil {
		// Slice/map roots have a single decode path: no ordered/fallback
		// split to compare (mirrors emitRootFuzz).
		fmt.Fprintf(b, "}\n\n")
		return
	}
	fmt.Fprintf(b, "\tvar viaFallback %s\n", n)
	fmt.Fprintf(b, "\tlFallback := zerojson.NewLexer(enc, false)\n")
	fmt.Fprintf(b, "\t%s(&lFallback, &viaFallback, &%s{})\n", fallbackDecodeName(n), arenaName(n))
	fmt.Fprintf(b, "\tlFallback.Finish()\n")
	fmt.Fprintf(b, "\tif err := lFallback.Err(); err != nil {\n")
	fmt.Fprintf(b, "\t\tt.Fatalf(\"fallback-only decode rejected zerojson output %%q: %%v\", enc, err)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tif !reflect.DeepEqual(&viaFallback, &reZJ) {\n")
	fmt.Fprintf(b, "\t\tt.Fatalf(\"ordered-vs-fallback divergence\\ninput: %%s\\nfallback-only: %%+v\\npublic (ordered+fallback): %%+v\", enc, &viaFallback, &reZJ)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
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
	// raw value comparisons for comparable roots, including zerojson.Raw.
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
	if g.trusted {
		// zjCheckTrusted<T> compares with reflect and calls zerojson.Valid.
		needsReflect = true
	}
	fmt.Fprintf(&b, "import (\n\t\"bytes\"\n\t\"encoding/json\"\n\t\"errors\"\n\t\"math/big\"\n")
	if needsReflect {
		fmt.Fprintf(&b, "\t\"reflect\"\n")
	}
	fmt.Fprintf(&b, "\t\"strings\"\n\t\"testing\"\n")
	if hasStruct || g.trusted {
		fmt.Fprintf(&b, "\n\t%q\n", runtimePath)
	}
	fmt.Fprintf(&b, ")\n\n")
	b.WriteString(jsonValueOracleSource)
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
	// Keep value-error classification available for focused oracle tests.
	// Both value and syntax errors must be mirrored by public decoding.
	fmt.Fprintf(&b, "func zjStdlibValueError(err error) bool {\n")
	fmt.Fprintf(&b, "\tvar ute *json.UnmarshalTypeError\n")
	fmt.Fprintf(&b, "\tif errors.As(err, &ute) {\n")
	fmt.Fprintf(&b, "\t\treturn true\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\treturn strings.Contains(err.Error(), \"invalid use of ,string struct tag\")\n")
	fmt.Fprintf(&b, "}\n\n")
	// Decimal libraries (shopspring-style) parse huge exponents compactly
	// but expand them to millions of digits when re-encoding — a property
	// of the decimal type shared by every codec, so the fuzzers skip such
	// inputs.
	// zjDecodeStricterAllowed is the allowlist hook for the decode-error
	// ORACLE DIRECTION (zerojson-error ⇒ stdlib-error): it reports whether
	// zerojson may reject `data` that encoding/json ACCEPTS as a DOCUMENTED
	// deliberate strictness. []byte's uncommon
	// array-of-numbers form is seeded explicitly and is supported, so it needs
	// no exception here.
	// Trailing data is NOT stricter than stdlib (encoding/json rejects it
	// too), so it is deliberately not listed.
	// One toolchain-dependent exception: Go 1.27 re-based encoding/json on
	// the v2 implementation, which accepts a '+' sign inside a `,string`
	// quoted number (`"+00"`) that classic v1, easyjson, and zerojson all
	// reject. Detected at init by probing the oracle rather than by Go
	// version, so the test keeps working under GOEXPERIMENT=nojsonv2.
	fmt.Fprintf(&b, "// zjStdlibJSONV2 reports whether encoding/json is the Go 1.27+ v2-backed\n")
	fmt.Fprintf(&b, "// implementation (which encodes an invalid UTF-8 byte as a raw U+FFFD\n")
	fmt.Fprintf(&b, "// rather than the classic \\ufffd escape), probed rather than assumed.\n")
	fmt.Fprintf(&b, "var zjStdlibJSONV2 = func() bool {\n")
	fmt.Fprintf(&b, "\tb, _ := json.Marshal(\"\\x80\")\n")
	fmt.Fprintf(&b, "\treturn !bytes.Contains(b, []byte(`\\ufffd`))\n")
	fmt.Fprintf(&b, "}()\n\n")
	// The exemption is generated PER FUZZ TARGET (zjDecodeStricterAllowed<T>)
	// and scoped to the exact `,string` fields reachable from that type,
	// each matched only as an object member at the nesting depth where the
	// field actually lives — so `{"unknown":{"count":"+00"}}` earns no pass
	// for a type whose `count` is not a `,string` field at depth 1, and a
	// `"+` beginning any other string value never does.
	fmt.Fprintf(&b, "// zjMemberValueStarts appends to out the start index of the value of every\n")
	fmt.Fprintf(&b, "// object member named key at exactly the given nesting depth (1 = a member\n")
	fmt.Fprintf(&b, "// of the top-level object). Keys are compared after JSON-unescaping; strings\n")
	fmt.Fprintf(&b, "// are consumed whole so braces inside them never affect the depth; malformed\n")
	fmt.Fprintf(&b, "// input yields no sites.\n")
	fmt.Fprintf(&b, "func zjMemberValueStarts(data []byte, key string, depth int, out []int) []int {\n")
	fmt.Fprintf(&b, "\tskipWS := func(p int) int {\n")
	fmt.Fprintf(&b, "\t\tfor p < len(data) && (data[p] == ' ' || data[p] == '\\t' || data[p] == '\\n' || data[p] == '\\r') {\n\t\t\tp++\n\t\t}\n\t\treturn p\n\t}\n")
	fmt.Fprintf(&b, "\td := 0\n")
	fmt.Fprintf(&b, "\tfor p := 0; p < len(data); {\n")
	fmt.Fprintf(&b, "\t\tswitch data[p] {\n")
	fmt.Fprintf(&b, "\t\tcase '{', '[':\n\t\t\td++\n\t\t\tp++\n")
	fmt.Fprintf(&b, "\t\tcase '}', ']':\n\t\t\td--\n\t\t\tp++\n")
	fmt.Fprintf(&b, "\t\tcase '\"':\n")
	fmt.Fprintf(&b, "\t\t\tend := zjStringEnd(data, p)\n\t\t\tif end < 0 {\n\t\t\t\treturn out\n\t\t\t}\n")
	fmt.Fprintf(&b, "\t\t\tq := skipWS(end)\n")
	fmt.Fprintf(&b, "\t\t\tif d == depth && q < len(data) && data[q] == ':' {\n")
	fmt.Fprintf(&b, "\t\t\t\tvar name string\n")
	fmt.Fprintf(&b, "\t\t\t\tif json.Unmarshal(data[p:end], &name) == nil && name == key {\n")
	fmt.Fprintf(&b, "\t\t\t\t\tout = append(out, skipWS(q+1))\n")
	fmt.Fprintf(&b, "\t\t\t\t}\n\t\t\t}\n")
	fmt.Fprintf(&b, "\t\t\tp = end\n")
	fmt.Fprintf(&b, "\t\tdefault:\n\t\t\tp++\n")
	fmt.Fprintf(&b, "\t\t}\n\t}\n\treturn out\n}\n\n")
	fmt.Fprintf(&b, "// zjStringEnd returns the index just past the string token starting at p\n")
	fmt.Fprintf(&b, "// (data[p] == '\"'), or -1 if unterminated.\n")
	fmt.Fprintf(&b, "func zjStringEnd(data []byte, p int) int {\n")
	fmt.Fprintf(&b, "\tfor q := p + 1; q < len(data); q++ {\n")
	fmt.Fprintf(&b, "\t\tswitch data[q] {\n\t\tcase '\\\\':\n\t\t\tq++\n\t\tcase '\"':\n\t\t\treturn q + 1\n\t\t}\n\t}\n\treturn -1\n}\n\n")
	fmt.Fprintf(&b, "// zjValueEnd returns the index just past the JSON value starting at p\n")
	fmt.Fprintf(&b, "// (object, array, string, or scalar), or len(data) if it runs off the end.\n")
	fmt.Fprintf(&b, "func zjValueEnd(data []byte, p int) int {\n")
	fmt.Fprintf(&b, "\tif p >= len(data) {\n\t\treturn len(data)\n\t}\n")
	fmt.Fprintf(&b, "\tswitch data[p] {\n")
	fmt.Fprintf(&b, "\tcase '\"':\n\t\tif e := zjStringEnd(data, p); e >= 0 {\n\t\t\treturn e\n\t\t}\n\t\treturn len(data)\n")
	fmt.Fprintf(&b, "\tcase '{', '[':\n")
	fmt.Fprintf(&b, "\t\td := 0\n")
	fmt.Fprintf(&b, "\t\tfor q := p; q < len(data); q++ {\n")
	fmt.Fprintf(&b, "\t\t\tswitch data[q] {\n")
	fmt.Fprintf(&b, "\t\t\tcase '{', '[':\n\t\t\t\td++\n")
	fmt.Fprintf(&b, "\t\t\tcase '}', ']':\n\t\t\t\td--\n\t\t\t\tif d == 0 {\n\t\t\t\t\treturn q + 1\n\t\t\t\t}\n")
	fmt.Fprintf(&b, "\t\t\tcase '\"':\n\t\t\t\tif e := zjStringEnd(data, q); e < 0 {\n\t\t\t\t\treturn len(data)\n\t\t\t\t} else {\n\t\t\t\t\tq = e - 1\n\t\t\t\t}\n")
	fmt.Fprintf(&b, "\t\t\t}\n\t\t}\n\t\treturn len(data)\n")
	fmt.Fprintf(&b, "\tdefault:\n")
	fmt.Fprintf(&b, "\t\tq := p\n\t\tfor q < len(data) && data[q] != ',' && data[q] != '}' && data[q] != ']' && data[q] != ' ' && data[q] != '\\n' && data[q] != '\\t' && data[q] != '\\r' {\n\t\t\tq++\n\t\t}\n\t\treturn q\n")
	fmt.Fprintf(&b, "\t}\n}\n\n")
	fmt.Fprintf(&b, "// zjPlusStringMemberSites appends to out the index of the '+' byte of every\n")
	fmt.Fprintf(&b, "// member named key at the given depth whose value is a string beginning '+'.\n")
	fmt.Fprintf(&b, "func zjPlusStringMemberSites(data []byte, key string, depth int, out []int) []int {\n")
	fmt.Fprintf(&b, "\tfor _, v := range zjMemberValueStarts(data, key, depth, nil) {\n")
	fmt.Fprintf(&b, "\t\tif v+1 < len(data) && data[v] == '\"' && data[v+1] == '+' {\n\t\t\tout = append(out, v+1)\n\t\t}\n")
	fmt.Fprintf(&b, "\t}\n\treturn out\n}\n\n")
	fmt.Fprintf(&b, "// zjMemberIsNull / zjMemberContainsNull report whether some member named key\n")
	fmt.Fprintf(&b, "// at the given depth has the value null, or has null anywhere inside its\n")
	fmt.Fprintf(&b, "// value (an array element or map value).\n")
	fmt.Fprintf(&b, "func zjMemberIsNull(data []byte, key string, depth int) bool {\n")
	fmt.Fprintf(&b, "\tfor _, v := range zjMemberValueStarts(data, key, depth, nil) {\n")
	fmt.Fprintf(&b, "\t\tif bytes.HasPrefix(data[v:], []byte(\"null\")) {\n\t\t\treturn true\n\t\t}\n")
	fmt.Fprintf(&b, "\t}\n\treturn false\n}\n\n")
	fmt.Fprintf(&b, "func zjMemberContainsNull(data []byte, key string, depth int) bool {\n")
	fmt.Fprintf(&b, "\tfor _, v := range zjMemberValueStarts(data, key, depth, nil) {\n")
	fmt.Fprintf(&b, "\t\tif bytes.Contains(data[v:zjValueEnd(data, v)], []byte(\"null\")) {\n\t\t\treturn true\n\t\t}\n")
	fmt.Fprintf(&b, "\t}\n\treturn false\n}\n\n")
	fmt.Fprintf(&b, "// zjMemberHasEscape reports whether the value of member key at object depth\n")
	fmt.Fprintf(&b, "// depth (or any string inside it) is spelled with a JSON escape.\n")
	fmt.Fprintf(&b, "func zjMemberHasEscape(data []byte, key string, depth int) bool {\n")
	fmt.Fprintf(&b, "\tfor _, v := range zjMemberValueStarts(data, key, depth, nil) {\n")
	fmt.Fprintf(&b, "\t\tif bytes.IndexByte(data[v:zjValueEnd(data, v)], '\\\\') >= 0 {\n\t\t\treturn true\n\t\t}\n")
	fmt.Fprintf(&b, "\t}\n\treturn false\n}\n\n")
	// Public decode validates grammar before reading values. Syntax errors
	// must be mirrored too; no blanket malformed-value exemption remains.
	fmt.Fprintf(&b, "func zjStdlibMustMirror(err error) bool {\n")
	fmt.Fprintf(&b, "\treturn err != nil\n}\n\n")
	fmt.Fprintf(&b, "// zjDeleteBytes returns data with the bytes at the given indices removed.\n")
	fmt.Fprintf(&b, "func zjDeleteBytes(data []byte, at []int) []byte {\n")
	fmt.Fprintf(&b, "\tdrop := make(map[int]bool, len(at))\n\tfor _, i := range at {\n\t\tdrop[i] = true\n\t}\n")
	fmt.Fprintf(&b, "\tout := make([]byte, 0, len(data))\n\tfor i, c := range data {\n\t\tif !drop[i] {\n\t\t\tout = append(out, c)\n\t\t}\n\t}\n\treturn out\n}\n\n")
	for _, ti := range g.types {
		g.emitStricterAllowed(&b, ti)
		g.emitEscapedLeafGuard(&b, ti)
		g.emitTrustedCheck(&b, ti.name)
		if g.typeHasInvalidTag(ti.name, map[string]bool{}) {
			g.emitNoOracleFuzz(&b, ti)
		}
	}
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
		fmt.Fprintf(&b, "func Fuzz%sZJSON(f *testing.F) {\n", fuzzTypeName(n))
		emitStructFuzzSeeds(&b, ti)
		fmt.Fprintf(&b, "\tf.Fuzz(func(t *testing.T, data []byte) {\n")
		fmt.Fprintf(&b, "\t\tif zjFuzzHugeExponent(data) { return }\n")
		g.emitTrustedCall(&b, n)
		emitNoOracleCall(&b, n, g.typeHasInvalidTag(n, map[string]bool{}))
		b.WriteString("\t\toracleData := data\n")
		g.emitRawValueComparison(&b, n, ti.fields, false)
		emitDecodeErrorDirection(&b, n)
		if g.typeContainsRaw(ti.name, map[string]bool{}) {
			emitRawGraphRoundTrip(&b, n)
		}
		// Decode arm: canonicalize via stdlib, then zerojson must match.
		// When stdlib rejects data, zerojson must reject the original too.
		fmt.Fprintf(&b, "\t\tvar seed %s\n", n)
		emitStdlibRejectClassify(&b, n)
		fmt.Fprintf(&b, "\t\tcanon, err := json.Marshal(&seed)\n")
		fmt.Fprintf(&b, "\t\tif err != nil { return }\n")
		fmt.Fprintf(&b, "\t\tif zjNullOnValueUnmarshaler%s(canon) { return }\n", n)
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
		// directly — both are in this package, so zjDecodeFallback_<T>
		// and zjArena<T> (always generated, regardless of whether this
		// type is ordered-eligible) are reachable here.
		fmt.Fprintf(&b, "\t\tvar viaFallback %s\n", n)
		fmt.Fprintf(&b, "\t\tlFallback := zerojson.NewLexer(canon, false)\n")
		fmt.Fprintf(&b, "\t\t%s(&lFallback, &viaFallback, &%s{})\n", fallbackDecodeName(n), arenaName(n))
		// Finish() lives in the public wrapper, not in zjDecodeFallback_<T>
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
		fmt.Fprintf(&b, "\t\texpected, err := json.Marshal(&viaZJ)\n")
		fmt.Fprintf(&b, "\t\tif err == nil { zjAssertJSONEqual(t, expected, enc) }\n")
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
	return g.aliasGeneratedImports(b.String())
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
	fmt.Fprintf(b, "func Fuzz%sZJSONSequence(f *testing.F) {\n", fuzzTypeName(n))
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
	fmt.Fprintf(b, "func Fuzz%sZJSON(f *testing.F) {\n", fuzzTypeName(n))
	fmt.Fprintf(b, "\tf.Add([]byte(`%s`))\n", seed)
	fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", fuzzSeedValue(*ti.rootField))
	if alternate, ok := fuzzAlternateSeedValue(*ti.rootField); ok {
		fmt.Fprintf(b, "\tf.Add([]byte(%q))\n", alternate)
	}
	fmt.Fprintf(b, "\tf.Add([]byte(`null`))\n")
	fmt.Fprintf(b, "\tf.Add([]byte(`%s x`))\n", seed)
	fmt.Fprintf(b, "\tf.Fuzz(func(t *testing.T, data []byte) {\n")
	fmt.Fprintf(b, "\t\tif zjFuzzHugeExponent(data) { return }\n")
	g.emitTrustedCall(b, n)
	emitNoOracleCall(b, n, g.typeHasInvalidTag(n, map[string]bool{}))
	b.WriteString("\t\toracleData := data\n")
	if g.fieldRawComparable(*ti.rootField, map[string]bool{}) {
		g.emitRawValueComparison(b, n, nil, true)
	}
	emitDecodeErrorDirection(b, n)
	if g.typeContainsRaw(ti.name, map[string]bool{}) {
		emitRawGraphRoundTrip(b, n)
	}
	fmt.Fprintf(b, "\t\tvar seed %s\n", n)
	emitStdlibRejectClassify(b, n)
	fmt.Fprintf(b, "\t\tcanon, err := json.Marshal(&seed)\n")
	fmt.Fprintf(b, "\t\tif err != nil { return }\n")
	fmt.Fprintf(b, "\t\tif zjNullOnValueUnmarshaler%s(canon) { return }\n", n)
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
	fmt.Fprintf(b, "\t\texpected, err := json.Marshal(&viaZJ)\n")
	fmt.Fprintf(b, "\t\tif err == nil { zjAssertJSONEqual(t, expected, enc) }\n")
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

// stringOptSite is one `,string` field reachable from a fuzz target: its
// JSON name and the object nesting depth at which it appears in a
// document for that target (1 = member of the top-level object).
type stringOptSite struct {
	key   string
	depth int
}

// stringOptSites walks the generated type graph from name, recording every
// `,string` field with the depth it sits at: a nested struct field adds
// one level, a slice or map of structs adds two (the array/object plus
// the element object). The graph is acyclic (recursive schemas are
// refused), so no cycle guard is needed.
func (g *generator) stringOptSites(name string, depth int, out *[]stringOptSite) {
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		if ti.rootField != nil {
			g.fieldStringOptSites(*ti.rootField, depth-1, out)
			return
		}
		for _, f := range ti.fields {
			g.fieldStringOptSites(f, depth, out)
		}
		return
	}
}

func (g *generator) fieldStringOptSites(f fieldInfo, depth int, out *[]stringOptSite) {
	if f.stringOpt {
		*out = append(*out, stringOptSite{key: f.jsonName, depth: depth})
	}
	switch f.kind {
	case kStruct:
		g.stringOptSites(f.elem, depth+1, out)
	case kSlice:
		g.stringOptSites(f.elem, depth+2, out)
	case kMap:
		if f.valInfo != nil && f.valInfo.kind == kStruct {
			g.stringOptSites(f.valInfo.elem, depth+2, out)
		}
	}
}

// emitStricterAllowed emits, for fuzz target ti, three functions:
//
//	zjStripStringOptPlus<T>(data) ([]byte, bool) — data with the '+' removed
//	    from every `,string` member of T (at its own depth); false if none
//	zjStricterCausedByPlus<T>(data) bool — true iff such a '+' existed AND
//	    zerojson accepts the stripped document: the '+' was the sole cause
//	    of zerojson's rejection, so the documented v1-on-v2 drift explains it
//	zjDecodeStricterAllowed<T>(data) bool — the above, gated on zjStdlibJSONV2
//
// Proving causality (rather than mere presence of a "+" member) means a
// document that zerojson also rejects for some OTHER reason is never
// exempted, and makes the exact object path irrelevant: stripping a '+'
// that was not the cause leaves zerojson rejecting, and the comparison
// runs. The first two are toolchain-independent and tested negatively.
func (g *generator) emitStricterAllowed(b *strings.Builder, ti typeInfo) {
	n := ti.name
	var sites []stringOptSite
	g.stringOptSites(n, 1, &sites)
	fmt.Fprintf(b, "// zjStripStringOptPlus%s returns data with the leading '+' removed from every\n", n)
	fmt.Fprintf(b, "// quoted value on one of %s's `,string` fields (at that field's own nesting\n", n)
	fmt.Fprintf(b, "// depth), and whether there was any to remove.\n")
	fmt.Fprintf(b, "func zjStripStringOptPlus%s(data []byte) ([]byte, bool) {\n", n)
	if len(sites) == 0 {
		fmt.Fprintf(b, "\treturn data, false // %s has no `,string` fields\n}\n\n", n)
	} else {
		fmt.Fprintf(b, "\tvar at []int\n")
		for _, st := range sites {
			fmt.Fprintf(b, "\tat = zjPlusStringMemberSites(data, %q, %d, at)\n", st.key, st.depth)
		}
		fmt.Fprintf(b, "\tif len(at) == 0 {\n\t\treturn data, false\n\t}\n")
		fmt.Fprintf(b, "\treturn zjDeleteBytes(data, at), true\n}\n\n")
	}
	fmt.Fprintf(b, "// zjStricterCausedByPlus%s reports whether zerojson's rejection of data is\n", n)
	fmt.Fprintf(b, "// explained by a leading '+' on a `,string` member alone: with those '+'\n")
	fmt.Fprintf(b, "// bytes removed (and nothing else changed) zerojson must accept the document.\n")
	fmt.Fprintf(b, "func zjStricterCausedByPlus%s(data []byte) bool {\n", n)
	fmt.Fprintf(b, "\tfixed, ok := zjStripStringOptPlus%s(data)\n", n)
	fmt.Fprintf(b, "\tif !ok {\n\t\treturn false\n\t}\n")
	fmt.Fprintf(b, "\tvar v %s\n", n)
	fmt.Fprintf(b, "\treturn v.UnmarshalZJSON(fixed) == nil\n}\n\n")
	g.emitNullDivergenceGuard(b, ti)
	fmt.Fprintf(b, "// zjDecodeStricterAllowed%s is the decode-error oracle-direction allowlist for\n", n)
	fmt.Fprintf(b, "// %s: only the documented Go 1.27 v1-on-v2 `,string` leading-'+' drift, and\n", n)
	fmt.Fprintf(b, "// only when that drift alone accounts for the rejection.\n")
	fmt.Fprintf(b, "func zjDecodeStricterAllowed%s(data []byte) bool {\n", n)
	fmt.Fprintf(b, "\treturn zjStdlibJSONV2 && zjStricterCausedByPlus%s(data)\n}\n\n", n)
}

// nullSite is one place in a fuzz target's document where a JSON null
// reaches a VALUE-typed ZeroJSONQuotedInt64 marker type: encoding/json
// invokes the type's own UnmarshalJSON("null") there (which conventionally
// errors), while zerojson decodes such a type through the quoted-int64
// fast path and leaves the value untouched on null — the documented
// divergence. Value-typed json.Unmarshalers are delivered the null by
// both decoders and need no exemption. exact sites are struct fields (the
// member value itself is null); contains sites are slices/maps of such
// values (a null element or map value).
type nullSite struct {
	key      string
	depth    int
	contains bool
}

func (g *generator) nullSites(name string, depth int, out *[]nullSite) {
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		if ti.rootField != nil {
			g.fieldNullSites(*ti.rootField, depth-1, out)
			return
		}
		for _, f := range ti.fields {
			g.fieldNullSites(f, depth, out)
		}
		return
	}
}

func (g *generator) fieldNullSites(f fieldInfo, depth int, out *[]nullSite) {
	switch f.kind {
	case kQuotedInt:
		if !f.ptr {
			*out = append(*out, nullSite{key: f.jsonName, depth: depth})
		}
	case kLeafSlice:
		if f.valInfo != nil && f.valInfo.kind == kQuotedInt && !f.elemPtr {
			*out = append(*out, nullSite{key: f.jsonName, depth: depth, contains: true})
		}
	case kMap:
		if f.valInfo != nil && f.valInfo.kind == kQuotedInt {
			*out = append(*out, nullSite{key: f.jsonName, depth: depth, contains: true})
		}
		if f.valInfo != nil && f.valInfo.kind == kStruct {
			g.nullSites(f.valInfo.elem, depth+2, out)
		}
	case kStruct:
		g.nullSites(f.elem, depth+1, out)
	case kSlice:
		g.nullSites(f.elem, depth+2, out)
	}
}

// emitNullDivergenceGuard emits zjNullOnValueUnmarshaler<T>(data) bool: true
// when the canonical document delivers a null to a value-typed
// json.Unmarshaler reachable from T, where the two decoders are documented
// to differ; the decode-arm comparison is skipped for such inputs rather
// than reporting the documented divergence as a failure.
func (g *generator) emitNullDivergenceGuard(b *strings.Builder, ti typeInfo) {
	n := ti.name
	var sites []nullSite
	g.nullSites(n, 1, &sites)
	fmt.Fprintf(b, "// zjNullOnValueUnmarshaler%s reports whether data delivers a JSON null to a\n", n)
	fmt.Fprintf(b, "// value-typed ZeroJSONQuotedInt64 marker type reachable from %s — the\n", n)
	fmt.Fprintf(b, "// documented divergence where encoding/json calls the type's UnmarshalJSON\n")
	fmt.Fprintf(b, "// (\"null\") and zerojson's quoted-int64 fast path leaves the value untouched.\n")
	fmt.Fprintf(b, "func zjNullOnValueUnmarshaler%s(data []byte) bool {\n", n)
	if len(sites) == 0 {
		fmt.Fprintf(b, "\t_ = data\n\treturn false // no value-typed quoted-int64 marker reachable from %s\n}\n\n", n)
		return
	}
	fmt.Fprintf(b, "\treturn false")
	for _, st := range sites {
		fn := "zjMemberIsNull"
		if st.contains {
			fn = "zjMemberContainsNull"
		}
		fmt.Fprintf(b, " ||\n\t\t%s(data, %q, %d)", fn, st.key, st.depth)
	}
	fmt.Fprintf(b, "\n}\n\n")
}

// escapedLeafSite is one direct-leaf field reachable from a fuzz target
// whose reader unescapes the literal before parsing it (timestamps, UUIDs,
// quoted-int64 markers, registered leaves), with the depth it sits at —
// the same depth model as stringOptSite.
type escapedLeafSite struct {
	key   string
	depth int
}

func (g *generator) escapedLeafSites(name string, depth int, out *[]escapedLeafSite) {
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		if ti.rootField != nil {
			g.fieldEscapedLeafSites(*ti.rootField, depth-1, out)
			return
		}
		for _, f := range ti.fields {
			g.fieldEscapedLeafSites(f, depth, out)
		}
		return
	}
}

func unescapingLeaf(f *fieldInfo) bool {
	if f == nil {
		return false
	}
	switch f.kind {
	case kTime, kUUID, kQuotedInt:
		return true
	case kCustomLeaf:
		return !isRawLeaf(*f)
	}
	return false
}

func (g *generator) fieldEscapedLeafSites(f fieldInfo, depth int, out *[]escapedLeafSite) {
	if unescapingLeaf(&f) || ((f.kind == kLeafSlice || f.kind == kMap) && unescapingLeaf(f.valInfo)) {
		*out = append(*out, escapedLeafSite{key: f.jsonName, depth: depth})
	}
	switch f.kind {
	case kStruct:
		g.escapedLeafSites(f.elem, depth+1, out)
	case kSlice:
		g.escapedLeafSites(f.elem, depth+2, out)
	case kMap:
		if f.valInfo != nil && f.valInfo.kind == kStruct {
			g.escapedLeafSites(f.valInfo.elem, depth+2, out)
		}
	}
}

// emitEscapedLeafGuard emits zjEscapedDirectLeaf<T>(data) bool: true when
// data spells a direct-leaf value reachable from T with a JSON escape.
// zerojson unescapes such a literal before parsing it, while encoding/json
// hands the raw bytes to the type's own UnmarshalJSON, which may reject
// the escaped spelling — a documented divergence, so a stdlib rejection
// of such a document is not one zerojson must mirror.
func (g *generator) emitEscapedLeafGuard(b *strings.Builder, ti typeInfo) {
	n := ti.name
	var sites []escapedLeafSite
	g.escapedLeafSites(n, 1, &sites)
	fmt.Fprintf(b, "// zjEscapedDirectLeaf%s reports whether data spells a timestamp, UUID,\n", n)
	fmt.Fprintf(b, "// quoted-int64 marker, or registered leaf reachable from %s with a JSON\n", n)
	fmt.Fprintf(b, "// escape — the documented divergence where zerojson unescapes before parsing\n")
	fmt.Fprintf(b, "// and encoding/json's UnmarshalJSON sees the raw bytes.\n")
	fmt.Fprintf(b, "func zjEscapedDirectLeaf%s(data []byte) bool {\n", n)
	if len(sites) == 0 {
		fmt.Fprintf(b, "\t_ = data\n\treturn false // no unescaping direct leaf reachable from %s\n}\n\n", n)
		return
	}
	fmt.Fprintf(b, "\treturn false")
	for _, st := range sites {
		fmt.Fprintf(b, " ||\n\t\tzjMemberHasEscape(data, %q, %d)", st.key, st.depth)
	}
	fmt.Fprintf(b, "\n}\n\n")
}

// typeHasInvalidTag reports whether name or any struct type reachable from
// it has a field whose tag name fell back to the Go field name.
func (g *generator) typeHasInvalidTag(name string, visiting map[string]bool) bool {
	if visiting[name] {
		return false
	}
	visiting[name] = true
	for _, ti := range g.types {
		if ti.name != name {
			continue
		}
		fields := ti.fields
		if ti.rootField != nil {
			fields = []fieldInfo{*ti.rootField}
		}
		for _, f := range fields {
			if f.invalidTag {
				return true
			}
			switch f.kind {
			case kStruct, kSlice:
				if g.typeHasInvalidTag(f.elem, visiting) {
					return true
				}
			case kMap:
				if f.valInfo != nil && f.valInfo.kind == kStruct && g.typeHasInvalidTag(f.valInfo.elem, visiting) {
					return true
				}
			}
		}
	}
	return false
}
