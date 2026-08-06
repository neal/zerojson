// zerojsongen generates zerojson codecs (AppendZJSON / UnmarshalZJSON)
// for the requested struct types. Emitted code encodes leaf types directly
// into an append buffer and decodes with a single arena allocation, enum
// switch dispatch, and tag-driven interning.
//
// Usage:
//
//	go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types A,B,C -out zerojson_gen.go
//
// Rules (validate with byte-identity tests against your current encoder):
//   - field order = struct declaration order
//   - the json tag carries ONLY standard encoding/json options: the name,
//     `-`, omitempty, omitzero, and string. Any other option (e.g.
//     `,intern`) is a generate-time error naming the field and telling you
//     to move it to a `zerojson:"..."` tag instead — this is enforced, not
//     just documented, so a stray option can't silently do nothing.
//   - zerojson:"..." is a comma-separated list of custom options; `intern`
//     is the only one today. Unknown zerojson options are also a
//     generate-time error.
//   - omitempty gates: ptr != nil; string/enum != ""; named-int != 0;
//     bool; slices len != 0. Value structs / time.Time / uuid.UUID cannot
//     be gated by omitempty (matching easyjson and encoding/json v1, which
//     cannot check their emptiness either) — use omitzero for those.
//   - omitzero (Go 1.24 stdlib semantics) gates on the zero value: an
//     IsZero() bool method is honored if the type has one (time.Time and
//     any user type that defines one); nil for
//     pointers/slices/maps/interfaces; the usual scalar zero otherwise.
//     easyjson ignores omitzero, so byte-identity does not extend to
//     fields that use it — the one intentional divergence.
//   - string (stdlib "quoted" convention) is implemented for plain
//     int/uint fields today: it encodes quoted and requires a quoted JSON
//     token on decode. Other kinds are a generate-time "not yet supported"
//     error, not silent mishandling.
//   - non-omitempty, non-omitzero pointer fields always emit the key,
//     null when nil.
//   - named string types are treated as enums: decode switches over the
//     type's declared constants. Unknown values are copied unless the field
//     explicitly carries zerojson:"intern".
//   - named int64 types implementing MarshalJSON may opt into the direct
//     quoted-string fast path with a ZeroJSONQuotedInt64() marker method.
//     Without that explicit promise their methods are delegated normally.
//   - other types with MarshalJSON/UnmarshalJSON delegate to those
//     methods (always correct, slower). NOTE: their output is appended
//     verbatim, matching easyjson; encoding/json would re-compact it.
//
// Source layout: this file holds flags, main(), and the generator driver.
// load.go collects and classifies struct fields from the loaded package.
// encode.go/decode.go emit AppendZJSON/UnmarshalZJSON bodies (and,
// for decode, the ordered speculative fast path in ordered.go). compat.go
// emits json.Marshaler/easyjson wrappers, pool.go the reusable-arena
// Decoder, and tests_emit.go the differential fuzz tests.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/scanner"
	"go/token"
	"go/types"
	"log"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	uuidPath    = "github.com/google/uuid"
	runtimePath = "github.com/neal/zerojson"
)

// leafSpec describes a custom leaf type: a Go type serialized by a pair of
// externally-provided functions rather than by built-in handling. The
// append function must have signature func([]byte, *T) []byte and the read
// function func(*zerojson.Lexer, *T).
type leafSpec struct {
	appendPkg  string // import path of the append func's package
	appendQual string // package qualifier used in emitted code
	appendFn   string
	readPkg    string
	readQual   string
	readFn     string
}

// leafFlags collects repeated -leaf specs from the command line.
type leafFlags []string

func (f *leafFlags) String() string { return strings.Join(*f, " ") }
func (f *leafFlags) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// parseLeaf parses "typepath=appendpkg.Fn,readpkg.Fn" into a type path and
// its spec. Package path and function name split on the final dot, which is
// unambiguous because import paths use slashes.
func parseLeaf(spec string) (typePath string, ls *leafSpec, err error) {
	before, after, ok := strings.Cut(spec, "=")
	if !ok {
		return "", nil, fmt.Errorf("-leaf %q: missing '='", spec)
	}
	typePath = before
	fns := strings.Split(after, ",")
	if len(fns) != 2 {
		return "", nil, fmt.Errorf("-leaf %q: want append,read functions", spec)
	}
	ap, af, err := splitQualified(fns[0])
	if err != nil {
		return "", nil, err
	}
	rp, rf, err := splitQualified(fns[1])
	if err != nil {
		return "", nil, err
	}
	return typePath, &leafSpec{
		appendPkg: ap, appendQual: pkgQual(ap), appendFn: af,
		readPkg: rp, readQual: pkgQual(rp), readFn: rf,
	}, nil
}

func splitQualified(s string) (pkg, fn string, err error) {
	dot := strings.LastIndexByte(s, '.')
	if dot < 0 {
		return "", "", fmt.Errorf("%q: want pkgpath.Func", s)
	}
	return s[:dot], s[dot+1:], nil
}

func pkgQual(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

type kind int

const (
	kInvalid kind = iota
	kString       // basic string
	kEnum         // named type, underlying string, with declared consts
	kBool
	kQuotedInt // named int64 marshaled as quoted string
	kTime
	kUUID
	kCustomLeaf // type registered through -leaf
	kInt        // signed integer kinds (incl. named, e.g. time.Duration)
	kUint       // unsigned integer kinds
	kFloat32
	kFloat64
	kStruct        // struct in the generation set
	kAppender      // hand-written AppendZJSON/ReadZJSON pair (see interface.go)
	kMarshaler     // has own MarshalJSON/UnmarshalJSON
	kTextMarshaler // has MarshalText/UnmarshalText (no MarshalJSON)
	kByteSlice     // []byte / named byte-slice: base64 string or numeric array
	kSlice         // slice of kStruct (value or pointer elements)
	kLeafSlice     // slice of leaf-kind elements ([]string, []enum.X, []int...)
	kMap           // map with string-kind keys and leaf-kind values
	kEmptyStruct   // struct{} (set-valued maps): encodes as {}
	kInterface     // interface{} / any: json.Marshal fallback like easyjson
)

type fieldInfo struct {
	goName           string
	access           string // selector after "v." (e.g. "Embedded.Field"); defaults to goName
	cellName         string // arena-unique id (e.g. "Embedded_Field"); defaults to goName
	jsonName         string
	omitempty        bool
	omitzero         bool // json:",omitzero" (Go 1.24 stdlib semantics)
	stringOpt        bool // json:",string" (implemented for kInt/kUint only)
	intern           bool // zerojson:"intern"
	hasIsZero        bool // named field type has an IsZero() bool method
	ptr              bool
	kind             kind
	typeStr          string // Go type expr for the (non-pointer) field type
	elem             string // struct type name for kStruct / kSlice
	enumConsts       []enumConst
	marshalerIsSlice bool       // for omitempty gating of kMarshaler
	marshalerIsMap   bool       // for omitempty gating of kMarshaler (map-underlying)
	marshalOnValue   bool       // MarshalJSON is in T's value method set (map values are not addressable)
	textOnValue      bool       // MarshalText is in T's value method set (map values are not addressable)
	elemPtr          bool       // kSlice: elements are pointers
	keyTypeStr       string     // kMap: key type expression
	valInfo          *fieldInfo // kMap: value classification
	leaf             *leafSpec  // kCustomLeaf: append/read funcs
	intRead          string     // kInt/kUint/kQuotedInt: width-aware read call (e.g. "l.ReadInt32()")
	// scalarEmpty records a named type's underlying scalar category ("num",
	// "str", "bool", or "" for struct/array/other underlyings) so omitempty
	// on a Marshaler/Appender/TextMarshaler value field can gate on the Go
	// zero value the way encoding/json's isEmptyValue does (it inspects the
	// underlying kind, not the Marshaler output) — a scalar-underlying
	// custom type omits its zero, a struct-underlying one is always emitted.
	scalarEmpty string
}

type enumConst struct {
	name string // qualified const name, e.g. enum.Buy
	val  string // string value
}

type typeInfo struct {
	name   string
	fields []fieldInfo
	// rootField is non-nil for named slice/map root types (type X []T,
	// []*T, or map[string]T): it classifies the element (slice) or value
	// (map) exactly as a slice/map FIELD would be, so the root reuses the
	// same element/value codecs. nil for ordinary struct roots, which use
	// fields instead. See root.go.
	rootField *fieldInfo
}

type generator struct {
	pkg *packages.Package
	// replaceableMethodFiles contains only codec output produced by zerojsongen
	// or easyjson. An arbitrary generated file can still deliberately define a
	// custom wire format, so ast.IsGenerated alone is too broad.
	replaceableMethodFiles map[string]bool
	explicit               map[string]bool // types named in -types: always generated
	genSet                 map[string]bool
	leaves                 map[string]*leafSpec // type path -> leaf codec
	queue                  []string             // discovered types pending field collection
	types                  []typeInfo
	imports                map[string]string // import path -> qualifier used in emitted code
	buf                    strings.Builder

	compat         bool // emit json.Marshaler/Unmarshaler wrappers
	compatEasyjson bool // also emit easyjson Marshaler/Unmarshaler wrappers
	pool           bool // emit reusable-arena Decoder types
	ordered        bool // emit the speculative ordered-decode fast path (ordered.go)
}

func main() {
	dir := flag.String("dir", ".", "package directory")
	typeList := flag.String("types", "", "comma-separated struct names")
	out := flag.String("out", "zerojson_gen.go", "output file")
	var leafFlag leafFlags
	flag.Var(&leafFlag, "leaf", "custom leaf codec: 'typepath=appendpkg.Fn,readpkg.Fn' (repeatable)")
	compat := flag.Bool("compat", false, "emit json.Marshaler/Unmarshaler wrappers (replaces existing MarshalJSON)")
	compatEasyjson := flag.Bool("compat-easyjson", false, "also emit easyjson Marshaler/Unmarshaler wrappers")
	testsOut := flag.String("tests", "", "also emit a differential fuzz test file at this path")
	pool := flag.Bool("pool", false, "emit reusable-arena Decoder types for zero-alloc decode loops")
	noOrdered := flag.Bool("no-ordered", false, "disable the speculative ordered-decode fast path (ordered.go)")
	flag.Parse()

	names := strings.Split(*typeList, ",")
	if len(names) == 0 || *typeList == "" {
		log.Fatal("-types is required")
	}

	// zerojson.Raw is always registered because it is a core-runtime type;
	// -leaf adds or overrides external custom leaf codecs.
	leaves := map[string]*leafSpec{}
	leaves[runtimePath+".Raw"] = &leafSpec{
		appendPkg: runtimePath, appendQual: "zerojson", appendFn: "AppendRaw",
		readPkg: runtimePath, readQual: "zerojson", readFn: "ReadRaw",
	}
	for _, spec := range leafFlag {
		tp, ls, err := parseLeaf(spec)
		if err != nil {
			log.Fatal(err)
		}
		leaves[tp] = ls
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir: *dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatal("package has errors")
	}
	pkg := pkgs[0]

	g := &generator{pkg: pkg, replaceableMethodFiles: map[string]bool{}, explicit: map[string]bool{}, genSet: map[string]bool{}, leaves: leaves, imports: map[string]string{},
		compat: *compat || *compatEasyjson, compatEasyjson: *compatEasyjson, pool: *pool, ordered: !*noOrdered}
	for _, f := range pkg.Syntax {
		if isReplaceableCodecOutput(f) {
			g.replaceableMethodFiles[pkg.Fset.Position(f.FileStart).Filename] = true
		}
	}
	for _, n := range names {
		g.explicit[strings.TrimSpace(n)] = true
		g.genSet[strings.TrimSpace(n)] = true
	}

	for _, n := range names {
		g.queue = append(g.queue, strings.TrimSpace(n))
	}
	done := map[string]bool{}
	for len(g.queue) > 0 {
		n := g.queue[0]
		g.queue = g.queue[1:]
		if done[n] {
			continue
		}
		done[n] = true
		obj := pkg.Types.Scope().Lookup(n)
		if obj == nil {
			log.Fatalf("type %s not found in %s", n, pkg.PkgPath)
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			log.Fatalf("type %s is not a named type", n)
		}
		if g.explicit[n] && !g.replaceableMethods(named) {
			log.Fatalf("%s: explicitly listed type has hand-written JSON/Text methods — zerojsongen will not silently replace a custom wire format; remove it from -types and let containing types delegate to it (implement AppendZJSON/ReadZJSON on the custom type as well if that delegated path must be allocation-free)", n)
		}
		switch under := obj.Type().Underlying().(type) {
		case *types.Struct:
			g.types = append(g.types, typeInfo{name: n, fields: g.collectFields(n, under)})
		case *types.Slice, *types.Map:
			g.types = append(g.types, typeInfo{name: n, rootField: g.collectRoot(n, under)})
		default:
			log.Fatalf("type %s is not a struct, a named slice ([]T / []*T), or a named string-keyed map (map[string]T) — those are the only root shapes zerojsongen generates", n)
		}
	}
	g.rejectRecursiveTypes()

	src := g.generate()
	formatted, err := format.Source([]byte(src))
	if err != nil {
		// Write unformatted output for debugging, then fail.
		_ = os.WriteFile(*out, []byte(src), 0o644)
		log.Fatalf("gofmt: %v (unformatted output written to %s)", err, *out)
	}
	if err := os.WriteFile(*out, formatted, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d types)\n", *out, len(g.types))

	if *testsOut != "" {
		tsrc := g.generateTests()
		tf, err := format.Source([]byte(tsrc))
		if err != nil {
			_ = os.WriteFile(*testsOut, []byte(tsrc), 0o644)
			log.Fatalf("gofmt tests: %v (unformatted output written to %s)", err, *testsOut)
		}
		if err := os.WriteFile(*testsOut, tf, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s (%d fuzz tests)\n", *testsOut, len(g.types))
	}
}

// isReplaceableCodecOutput reports whether f was emitted by a codec generator
// whose methods zerojsongen is explicitly designed to supersede. Merely having
// Go's standard "Code generated" marker is insufficient: generated model or
// schema files may intentionally define custom JSON/Text methods whose wire
// format must be preserved.
func isReplaceableCodecOutput(f *ast.File) bool {
	if !ast.IsGenerated(f) {
		return false
	}
	for _, group := range f.Comments {
		// A generated-file marker only has its standard meaning in the file
		// header. A handwritten file may legitimately quote either marker in a
		// doc comment after the package clause.
		if group.End() >= f.Package {
			continue
		}
		for _, comment := range group.List {
			text := strings.TrimSpace(comment.Text)
			if text == "// Code generated by zerojsongen. DO NOT EDIT." ||
				text == "// Code generated by easyjson for marshaling/unmarshaling. DO NOT EDIT." {
				return true
			}
		}
	}
	return false
}

func (g *generator) p(format string, args ...any) {
	fmt.Fprintf(&g.buf, format+"\n", args...)
}

// usedQualifiers returns the set of identifiers that appear as the base of a
// selector (an IDENT immediately followed by '.') anywhere in src's CODE,
// with comments excluded via the Go scanner. An import is needed iff its
// package qualifier is one of these — a substring scan of the raw body would
// wrongly count a qualifier mentioned only in a doc comment (e.g. the word
// "time." in prose), pulling in an unused import that fails to compile.
func usedQualifiers(src string) map[string]bool {
	used := map[string]bool{}
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), nil, 0) // mode 0: do not emit comment tokens
	prevIdent := ""
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.PERIOD && prevIdent != "" {
			used[prevIdent] = true
		}
		if tok == token.IDENT {
			prevIdent = lit
		} else {
			prevIdent = ""
		}
	}
	return used
}

func (g *generator) generate() string {
	// Body first: import usage is derived from what the body references.
	for _, ti := range g.types {
		if ti.rootField != nil {
			g.genRootArena(ti)
			g.genRootEncode(ti)
			g.genRootDecode(ti)
		} else {
			g.genArena(ti)
			g.genEncode(ti)
			g.genDecode(ti)
		}
		if g.compat {
			g.genCompat(ti)
		}
		if g.pool && g.explicit[ti.name] {
			g.genPool(ti)
		}
	}
	body := g.buf.String()
	used := usedQualifiers(body)

	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by zerojsongen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&out, "package %s\n\n", g.pkg.Types.Name())
	fmt.Fprintf(&out, "import (\n")
	fmt.Fprintf(&out, "\tzerojson %q\n", runtimePath)
	paths := make([]string, 0, len(g.imports))
	for p := range g.imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == runtimePath {
			// Already emitted unconditionally above; the Raw leaf's
			// spec names it like any other leaf package, but it's the
			// runtime itself, not a separate import.
			continue
		}
		alias := g.imports[p]
		// Include only imports the emitted body actually references in CODE:
		// value-typed leaf fields encode via runtime helpers and never name
		// their package. usedQualifiers looks for a real `alias.` selector
		// (an IDENT immediately followed by '.'), not a raw substring —
		// otherwise a comment mentioning e.g. "every time." would spuriously
		// pull in (and leave unused) the "time" import.
		if !used[alias] {
			continue
		}
		if strings.HasSuffix(p, "/"+alias) || p == alias {
			fmt.Fprintf(&out, "\t%q\n", p)
		} else {
			fmt.Fprintf(&out, "\t%s %q\n", alias, p)
		}
	}
	fmt.Fprintf(&out, ")\n")
	out.WriteString(body)
	return out.String()
}

func arenaName(t string) string  { return "zjArena" + t }
func decodeName(t string) string { return "zjDecode" + t }

// qualify returns the emitted-code qualifier ("pkg.") for a package and
// records the import. Distinct packages sharing a name are not supported
// yet; the gofmt pass catches the resulting duplicate imports.
func (g *generator) qualify(p *types.Package) string {
	if p == nil || p == g.pkg.Types {
		return ""
	}
	alias := p.Name()
	g.imports[p.Path()] = alias
	return alias + "."
}

func (g *generator) typeExpr(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		q := g.qualify(p)
		return strings.TrimSuffix(q, ".")
	})
}

// useLeaf registers the imports a leaf codec's functions require.
func (g *generator) useLeaf(ls *leafSpec) {
	g.imports[ls.appendPkg] = ls.appendQual
	g.imports[ls.readPkg] = ls.readQual
}
