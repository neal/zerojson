// zerojsongen generates append/read codecs for struct, named slice, and
// named string-keyed map roots. Reachable same-package structs are discovered
// automatically. See the repository README for supported shapes, tags,
// compatibility differences, and input/output ownership.
//
// Usage after installing the generator with go get -tool:
//
//	//go:generate go tool zerojsongen -dir . -types A,B -out zerojson_gen.go
//
// load.go classifies types; layout.go checks output/package boundaries;
// encode.go, decode.go, ordered.go, and root.go emit codecs; pool.go emits
// reusable decoders; compat.go emits wrappers; tests_emit.go emits fuzz tests.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	uuidPath    = "github.com/google/uuid"
	runtimePath = "github.com/neal/zerojson"
	// runtimeAlias is the qualifier the generated files import the runtime
	// under. It is emitted unconditionally (generate()), so no other import
	// may claim it — see importAlias.
	runtimeAlias = "zerojson"
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
	// Qualifiers are assigned by the generator's useLeaf, from the
	// package's declared name and de-duplicated against other imports.
	return typePath, &leafSpec{appendPkg: ap, appendFn: af, readPkg: rp, readFn: rf}, nil
}

func splitQualified(s string) (pkg, fn string, err error) {
	dot := strings.LastIndexByte(s, '.')
	if dot < 0 {
		return "", "", fmt.Errorf("%q: want pkgpath.Func", s)
	}
	return s[:dot], s[dot+1:], nil
}

func pkgQual(path string) string {
	name := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		name = path[i+1:]
	}
	return aliasFor(path, name)
}

// aliasFor is the qualifier a package is imported under: the runtime is
// always `zerojson` (generate() emits that import unconditionally), any
// other package its own name unless that collides with an identifier the
// generated code uses (safeAlias).
func aliasFor(path, name string) string {
	if path == runtimePath {
		return runtimeAlias
	}
	return safeAlias(name)
}

// generatedIdents are identifiers the emitted code declares (receivers,
// locals, loop variables, arena/decoder parameters) or the fuzz file
// imports. A field's package that happens to share one of these names —
// a package called `a`, `l`, `v`, `data`… — would shadow the local and
// fail to compile, so such a package is imported under an aliased name
// instead. Standard packages the codec file itself imports
// (encoding/binary, sync/atomic) are not listed: they go through
// importAlias like any other import, so a user package of the same name
// simply gets the alias de-duplicated whichever registers first. The
// runtime is the exception — it is imported unconditionally under
// runtimeAlias, which importAlias therefore never hands to anything else.
var generatedIdents = map[string]bool{
	"v": true, "dst": true, "n": true, "err": true, "raw": true, "first": true,
	"mk": true, "mv": true, "i": true, "l": true, "a": true, "data": true,
	"pos": true, "key": true, "e": true, "ea": true, "idx": true, "zjZero": true,
	"expected": true, "matched": true, "d": true, "p": true, "k": true,
	"start": true, "b": true, "w": true, "name": true, "isTopLevel": true,
	"json": true, "jwriter": true,
	"jlexer": true, "bytes": true, "reflect": true, "testing": true,
	"fmt": true, "strings": true, "f": true, "t": true, "seed": true,
	"canon": true, "enc": true, "x": true, "y": true,
	"src": true, "out": true, "rv": true,
	"borrow": true, "zero": true, "txt": true, "folded": true,
	"zjSlice": true, "zjIndex": true,
	"container": true,
}

// safeAlias returns the qualifier to import a package under: its own name
// unless that would collide with an identifier the generated code uses.
func safeAlias(name string) string {
	if generatedIdents[name] || types.Universe.Lookup(name) != nil {
		return name + "_"
	}
	return name
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
	kJSONNumber    // encoding/json.Number: the literal itself, verbatim (never a quoted enum)
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
	zeroOnValue      bool // IsZero belongs to the value method set
	ptr              bool
	kind             kind
	typeStr          string // Go type expr for the (non-pointer) field type
	byteElem         string // defined uint8 element: []E cannot convert to/from []byte
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
	// "str", "bool", "len" for slice/map, "array", or "" for other underlyings) so omitempty
	// on a Marshaler/Appender/TextMarshaler value field can gate on the Go
	// zero value the way encoding/json's isEmptyValue does (it inspects the
	// underlying kind, not the Marshaler output) — a scalar-underlying
	// custom type omits its zero, a struct-underlying one is always emitted.
	// The slice/map category also preserves null-clearing through Appender
	// and TextMarshaler classification (see isNullableField).
	scalarEmpty string
	// embedDepth and explicitName feed resolveDominance (load.go): the
	// embedding depth this candidate was collected at (0 = direct field)
	// and whether its JSON name came from a valid explicit tag.
	embedDepth   int
	explicitName bool
	// appenderHasUnmarshalJSON: a kAppender type that also implements
	// UnmarshalJSON — a JSON null is delivered to it as encoding/json
	// would (isNullDelivered). encoding/json routes a null through
	// UnmarshalJSON on the strength of that method alone, so this does
	// NOT also require MarshalJSON.
	appenderHasUnmarshalJSON bool
	// invalidTag: the json tag named the field with characters classic
	// encoding/json rejects, so the Go field name is used instead; the
	// emitted fuzzers skip such types under Go 1.27's v2-backed
	// encoding/json, which truncates the name instead (documented drift).
	invalidTag bool
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
	pkg            *packages.Package
	outPath        string // codec file being replaced; its old declarations do not reserve aliases
	testNames      map[string]token.Position
	generatedNames map[string]bool
	// replaceableMethodFiles contains only codec output produced by zerojsongen
	// or easyjson. An arbitrary generated file can still deliberately define a
	// custom wire format, so ast.IsGenerated alone is too broad.
	replaceableMethodFiles map[string]bool
	explicit               map[string]bool // types named in -types: always generated
	genSet                 map[string]bool
	leaves                 map[string]*leafSpec // type path -> leaf codec
	queue                  []string             // discovered types pending field collection
	types                  []typeInfo
	imports                map[string]string      // import path -> qualifier used in emitted code
	aliasOwner             map[string]string      // qualifier -> import path that claimed it (importAlias)
	leafPkgNames           map[string]string      // -leaf codec package path -> declared package name
	enumHelpers            map[string]*enumHelper // emitted zjReadEnum helpers, keyed by type expression
	buf                    strings.Builder

	compat         bool // emit json.Marshaler/Unmarshaler wrappers
	compatEasyjson bool // also emit easyjson Marshaler/Unmarshaler wrappers
	pool           bool // emit reset-first Decoder types
	ordered        bool // emit the speculative ordered-decode fast path (ordered.go)
}

func main() {
	dir := flag.String("dir", ".", "package directory")
	typeList := flag.String("types", "", "comma-separated root type names: structs, named slices ([]T / []*T), or named string-keyed maps; nested same-package structs are discovered automatically")
	out := flag.String("out", "zerojson_gen.go", "output file (relative to -dir unless absolute)")
	var leafFlag leafFlags
	flag.Var(&leafFlag, "leaf", "custom leaf codec: 'typepath=appendpkg.Fn,readpkg.Fn' (repeatable)")
	compat := flag.Bool("compat", false, "emit json.Marshaler/Unmarshaler wrappers (replaces existing MarshalJSON)")
	compatEasyjson := flag.Bool("compat-easyjson", false, "also emit easyjson Marshaler/Unmarshaler wrappers")
	testsOut := flag.String("tests", "", "also emit a differential fuzz test file at this path (a _test.go file, relative to -dir; not combinable with -compat)")
	pool := flag.Bool("pool", false, "emit reset-first Decoder types with fresh per-call storage")
	noOrdered := flag.Bool("no-ordered", false, "disable the speculative ordered-decode fast path (ordered.go)")
	force := flag.Bool("force", false, "overwrite -out/-tests even if the existing file is not zerojsongen (or easyjson) output")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: zerojsongen -types T1,T2 [-dir DIR] [-out FILE] [flags]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	names := strings.Split(*typeList, ",")
	if len(names) == 0 || *typeList == "" {
		log.Fatal("-types is required")
	}
	if *testsOut != "" && !strings.HasSuffix(*testsOut, "_test.go") {
		log.Fatalf("-tests %s must name a _test.go file; otherwise the fuzz tests would compile into the production package", *testsOut)
	}
	if *testsOut != "" && (*compat || *compatEasyjson) {
		log.Fatalf("-tests cannot be combined with -compat/-compat-easyjson: the fuzzers use encoding/json as an independent oracle, and the compat wrappers would route json.Marshal/Unmarshal through the generated codec itself — keep the wrapper-free fixture types that get -tests in a separate package (see the guide: do not alternate flags over the same files)")
	}
	for i, n := range names {
		names[i] = strings.TrimSpace(n)
		if names[i] == "" {
			log.Fatalf("-types has an empty name (%q); use a comma-separated list of type names with no trailing comma", *typeList)
		}
	}

	// zerojson.Raw is always registered because it is a core-runtime type;
	// -leaf adds or overrides external custom leaf codecs.
	leaves := map[string]*leafSpec{}
	leaves[runtimePath+".Raw"] = &leafSpec{
		appendPkg: runtimePath, appendQual: runtimeAlias, appendFn: "AppendRaw",
		readPkg: runtimePath, readQual: runtimeAlias, readFn: "ReadRaw",
	}
	for _, spec := range leafFlag {
		tp, ls, err := parseLeaf(spec)
		if err != nil {
			log.Fatal(err)
		}
		leaves[tp] = ls
	}

	// Output paths are relative to -dir (the package they belong to), so a
	// manual `-dir events -out zerojson_gen.go` lands in events/, not in
	// the working directory — where go build would find a stray
	// `package events` file. Absolute paths are used as given.
	*out = resolveOutput(*dir, *out)
	if *testsOut != "" {
		*testsOut = resolveOutput(*dir, *testsOut)
		if samePath(*out, *testsOut) {
			log.Fatalf("-out and -tests name the same file (%s); the fuzz tests must go in a separate _test.go file", *out)
		}
	}
	guardOutputLayout(*dir, *out, *testsOut)
	pkg := loadPackage(*dir, *out)
	// The generated files import the runtime under the fixed runtimeAlias
	// and name it throughout. A package-scope declaration of that name in
	// the target package would collide with the import, so refuse with a
	// clear message rather than writing a file that cannot compile. (A
	// DEPENDENCY package of that name is fine — importAlias aliases it.)
	if obj := pkg.Types.Scope().Lookup(runtimeAlias); obj != nil {
		log.Fatalf("%s declares %s at package scope (%s), which would collide with the runtime import the generated file needs under that name — rename it",
			pkg.PkgPath, runtimeAlias, pkg.Fset.Position(obj.Pos()))
	}

	g := &generator{pkg: pkg, outPath: *out, replaceableMethodFiles: map[string]bool{}, explicit: map[string]bool{}, genSet: map[string]bool{}, leaves: leaves, imports: map[string]string{},
		aliasOwner: map[string]string{}, leafPkgNames: resolveLeafPkgNames(*dir, leaves), enumHelpers: map[string]*enumHelper{},
		compat: *compat || *compatEasyjson, compatEasyjson: *compatEasyjson, pool: *pool, ordered: !*noOrdered}
	g.testNames = loadTestNames(*dir, pkg.Name)
	if pos, ok := g.testNames[runtimeAlias]; ok {
		log.Fatalf("test declaration %s at %s collides with the runtime import", runtimeAlias, pos)
	}
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
		if _, isAlias := obj.Type().(*types.Alias); isAlias {
			// Methods cannot be declared on an alias: generating for it
			// would duplicate the aliased type's methods (or put them on
			// a type from another package).
			log.Fatalf("type %s is an alias of %s — list the aliased type instead (an alias of a type from another package cannot be generated for)", n, types.Unalias(obj.Type()))
		}
		named, ok := types.Unalias(obj.Type()).(*types.Named)
		if !ok {
			log.Fatalf("type %s is not a named type", n)
		}
		rejectGeneric(named, n)
		if m := g.handwrittenEasyjsonMethod(named); m != "" {
			log.Fatalf("%s: type has a hand-written %s method — easyjson calls it, so it defines this type's wire format, and zerojsongen will not silently replace a custom wire format; remove it from -types and give it a MarshalJSON/UnmarshalJSON (or AppendZJSON/ReadZJSON) pair producing the same bytes, so containing types delegate to it", n, m)
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

	guardOverwrite(*out, *force)
	if *testsOut != "" {
		guardOverwrite(*testsOut, *force)
	}
	src := g.generate()
	g.guardGeneratedNames(src, *out)
	var tsrc string
	if *testsOut != "" {
		tsrc = g.generateTests()
		g.guardGeneratedNames(tsrc, *testsOut)
		// Imports also conflict with declarations in another generated
		// file in the same package, including test-only helpers.
		src = g.aliasGeneratedImports(src, tsrc)
		tsrc = g.aliasGeneratedImports(tsrc, src)
	}
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

// loadMode is what the generator needs from go/packages: types and type
// info for classification, syntax for the generated-file-header check.
const loadMode = packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
	packages.NeedSyntax | packages.NeedImports | packages.NeedDeps

// loadPackage type-checks the package in dir. Its own previous output at
// outPath is first blanked out through a go/packages overlay: a stale
// generated file (one that references a field the user has since renamed
// or removed) would otherwise make the package fail to type-check and
// block the very regeneration that fixes it, and the previous output's
// generated methods are exactly what this run replaces, so nothing is
// lost by not seeing them. If the package does not type-check WITHOUT the
// old file either (hand-written code in the package depends on something
// it declared, e.g. a -pool Decoder type), the ordinary load is used so
// the errors reported are the ones the user would see from go build.
func loadPackage(dir, outPath string) *packages.Package {
	base := packages.Config{Mode: loadMode, Dir: dir}
	if abs, err := filepath.Abs(outPath); err == nil {
		if _, err := os.Stat(abs); err == nil {
			if pkg := loadWithBlankedOutput(base, abs); pkg != nil {
				return pkg
			}
		}
	}
	pkgs, err := packages.Load(&base, ".")
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatal("package has errors")
	}
	return pkgs[0]
}

// guardOverwrite refuses to clobber a file at path that zerojsongen (or
// easyjson, whose output zerojsongen is designed to supersede) did not
// write: a hand-written file that happens to share the -out name would
// otherwise be silently destroyed. The decision uses the same test as
// method-replaceability (isReplaceableCodecOutput): the file must parse
// and carry the exact generated-file marker in its HEADER comment, before
// the package clause — merely mentioning the marker text somewhere in a
// hand-written file does not qualify. -force overrides.
func guardOverwrite(path string, force bool) {
	if force {
		return
	}
	if overwriteAllowed(path) {
		return
	}
	log.Fatalf("refusing to overwrite %s: it does not carry a zerojsongen (or easyjson) generated-file header, so it looks hand-written — choose another -out, remove the file, or pass -force", path)
}

// overwriteAllowed reports whether path is absent or is a file zerojsongen
// (or easyjson) generated, judged by the header comment alone.
func overwriteAllowed(path string) bool {
	existing, err := os.ReadFile(path)
	if err != nil {
		return true // nothing there (or unreadable, in which case the write will fail loudly)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, existing, parser.ParseComments|parser.PackageClauseOnly)
	return err == nil && isReplaceableCodecOutput(f)
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
	g.emitEnumHelpers()
	body := g.buf.String()
	used := usedQualifiers(body)

	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by zerojsongen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&out, "package %s\n\n", g.pkg.Types.Name())
	fmt.Fprintf(&out, "import (\n")
	fmt.Fprintf(&out, "\t%s %q\n", runtimeAlias, runtimePath)
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
	return g.aliasGeneratedImports(out.String())
}

func arenaName(t string) string  { return "zjArena" + t }
func decodeName(t string) string { return "zjDecode_" + t }

// qualify returns the emitted-code qualifier ("pkg.") for a package and
// records the import under a unique alias (see importAlias).
func (g *generator) qualify(p *types.Package) string {
	if p == nil || p == g.pkg.Types {
		return ""
	}
	return g.importAlias(p.Path(), p.Name()) + "."
}

// importAlias returns the identifier a package is imported under in the
// generated file and records the import. It starts from aliasFor (the
// package's own name unless that collides with an identifier the generated
// code uses) and appends underscores until the alias is not already
// claimed by a DIFFERENT import path — so two packages both called `data`,
// or one called `a` next to one called `a_`, get distinct aliases instead
// of a duplicate import that only fails at go build.
func (g *generator) importAlias(path, name string) string {
	if alias, ok := g.imports[path]; ok {
		return alias
	}
	alias := aliasFor(path, name)
	if path != runtimePath {
		for {
			// The runtime import is emitted unconditionally under
			// runtimeAlias, whether or not anything registered it here
			// first, so a dependency package that happens to be named
			// `zerojson` must not claim that qualifier: the file would
			// then carry two imports under one name and not compile.
			if alias == runtimeAlias {
				alias += "_"
				continue
			}
			if g.packageNameTaken(alias) {
				alias += "_"
				continue
			}
			if other, taken := g.aliasOwner[alias]; !taken || other == path {
				break
			}
			alias += "_"
		}
	}
	g.imports[path] = alias
	g.aliasOwner[alias] = path
	return alias
}

func (g *generator) packageNameTaken(name string) bool {
	if _, test := g.testNames[name]; test {
		return true
	}
	if g.pkg == nil || g.pkg.Types == nil {
		return false
	}
	obj := g.pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return false
	}
	// The final AST pass accounts for the new output's declarations. Reserving
	// the old output's names here would change qualifier-derived helper names
	// between a clean generation and regeneration.
	return g.outPath == "" || !samePath(g.outPath, g.pkg.Fset.Position(obj.Pos()).Filename)
}

// resolveOutput returns path relative to dir unless it is absolute.
func resolveOutput(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}

func (g *generator) typeExpr(t types.Type) string {
	g.checkTypeVisibility(t)
	return types.TypeString(t, func(p *types.Package) string {
		q := g.qualify(p)
		return strings.TrimSuffix(q, ".")
	})
}

// useLeaf registers the imports a leaf codec's functions require and fixes
// the qualifiers the emitted calls use — resolved through importAlias so
// they are unique, and from the package's DECLARED name (leafPkgNames)
// rather than its import path's last element, which need not match.
func (g *generator) useLeaf(ls *leafSpec) {
	ls.appendQual = g.leafQual(ls.appendPkg)
	ls.readQual = g.leafQual(ls.readPkg)
}

// leafQual is the qualifier for a codec function's package: empty when
// the codec lives in the package being generated (importing it would be
// an import cycle), otherwise a unique import alias.
func (g *generator) leafQual(path string) string {
	if path == g.pkg.PkgPath {
		return ""
	}
	return g.importAlias(path, g.leafPkgName(path))
}

// leafPkgName returns the declared package name for a -leaf codec
// package, resolved once through go list (resolveLeafPkgNames); it falls
// back to the import path's last element if the package could not be
// listed (the compile of the generated file then reports the mismatch).
func (g *generator) leafPkgName(path string) string {
	if name, ok := g.leafPkgNames[path]; ok && name != "" {
		return name
	}
	return pkgQual(path)
}

// resolveLeafPkgNames lists every -leaf codec package from dir to learn
// its declared name. A codec package is typically not imported by the
// package being generated (only the generated file imports it), so it is
// not among the loaded package's dependencies and must be asked for
// explicitly.
func resolveLeafPkgNames(dir string, leaves map[string]*leafSpec) map[string]string {
	names := map[string]string{}
	var paths []string
	for _, ls := range leaves {
		for _, p := range []string{ls.appendPkg, ls.readPkg} {
			if p != runtimePath {
				paths = append(paths, p)
			}
		}
	}
	if len(paths) == 0 {
		return names
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: dir}, paths...)
	if err != nil {
		return names
	}
	for _, p := range pkgs {
		if len(p.Errors) == 0 && p.Name != "" {
			names[p.PkgPath] = p.Name
		}
	}
	return names
}
