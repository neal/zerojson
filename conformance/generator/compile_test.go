package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testLeafSpec = "github.com/neal/zerojson/conformance/internal/testleaf.Decimal=github.com/neal/zerojson/conformance/internal/testleaf.Append,github.com/neal/zerojson/conformance/internal/testleaf.Read"

// compile_matrix_test.go closes the class the named-slice-root-of-a-leaf bug
// (the missing &cell[idx]) belonged to: a generator emit path that produced
// code the compiler rejects, invisible to any test that never generated that
// exact shape. It programmatically generates a codec for the cross product
// of root kinds (struct, []T, []*T, map[string]T) x element kinds (struct,
// pointer struct, string, int, time, uuid, Raw, decimal leaf, Marshaler) and
// asserts each generated package COMPILES (go build in a temp dir under this
// module, so the runtime/uuid/decimal deps resolve via the existing go.work).
// Combinations the generator legitimately refuses (a []*T of a non-pointer
// leaf element) assert the refusal instead of a compile.
//
// A small flag matrix (-pool / -compat / -no-ordered / -tests) is run over a
// representative combo so the flag-driven emit paths are compile-checked too.

type matrixElem struct {
	name    string
	typ     string   // element / field / map-value Go type expression
	imports []string // import paths the source type references
	decls   string   // extra local declarations (struct / Marshaler definitions)
	ptrOK   bool     // may appear as a []*T element (struct or Marshaler only)
}

const matrixMarshDecl = `
type Marsh struct{ V int }

func (m Marsh) MarshalJSON() ([]byte, error)  { return []byte("null"), nil }
func (m *Marsh) UnmarshalJSON(b []byte) error { return nil }
`

const matrixStructDecl = "\ntype Elem struct {\n\tA int    `json:\"a\"`\n\tB string `json:\"b\"`\n}\n"

// matrixWrongMarshDecl declares a type with a WRONG-signature MarshalJSON
// (returns string, not ([]byte, error)) — it does NOT satisfy json.Marshaler.
// The generator must ignore the method and emit a STRUCTURAL codec; if it
// instead misclassified the type as a Marshaler, the emitted
// `raw, err := ...MarshalJSON()` would fail to type-check (one return value,
// not two), so a successful `go build` of the generated package is itself
// the proof that the wrong-signature method was correctly ignored. The
// fixture lives only in this temp package, so `go vet ./...` on the repo
// never sees the deliberately-wrong method.
const matrixWrongMarshDecl = "\ntype WrongMarsh struct{ V int `json:\"v\"` }\n\nfunc (m WrongMarsh) MarshalJSON() string { return \"nope\" }\n"

func matrixElems() []matrixElem {
	return []matrixElem{
		{"struct", "Elem", nil, matrixStructDecl, true},
		{"string", "string", nil, "", false},
		{"int32", "int32", nil, "", false},
		{"int", "int", nil, "", false},   // platform-width: ReadInt path
		{"uint", "uint", nil, "", false}, // platform-width: ReadUint path
		{"time", "time.Time", []string{"time"}, "", false},
		{"uuid", "uuid.UUID", []string{"github.com/google/uuid"}, "", false},
		{"raw", "zerojson.Raw", []string{"github.com/neal/zerojson"}, "", false},
		{"decimal", "decimal.Decimal", []string{"github.com/neal/zerojson/conformance/internal/testleaf"}, "", false},
		{"marshaler", "Marsh", nil, matrixMarshDecl, true},
		// A wrong-signature MarshalJSON must be ignored and the type
		// generated structurally. Compiling the result is the assertion.
		{"wrongmarshaler", "WrongMarsh", nil, matrixWrongMarshDecl, true},
	}
}

// matrixRoot builds the root type declaration for a given kind and element
// type, and reports whether the generator is expected to refuse it.
func matrixRoot(kind, elemType string, ptrOK bool) (decl string, refuse bool) {
	switch kind {
	case "sliceT":
		return "type Root []" + elemType, false
	case "slicePtrT":
		// []*T is supported only for pointer-struct and pointer-Marshaler
		// elements; a []*<leaf> is refused.
		return "type Root []*" + elemType, !ptrOK
	case "mapT":
		return "type Root map[string]" + elemType, false
	default: // structT: a struct whose single field is the element kind
		return "type Root struct {\n\tF " + elemType + " `json:\"f\"`\n}", false
	}
}

// writeMatrixPkg writes a package p source file into dir declaring the given
// element helpers and root type.
func writeMatrixPkg(t *testing.T, dir string, e matrixElem, rootDecl string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package p\n\n")
	if len(e.imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range e.imports {
			switch imp {
			case "github.com/neal/zerojson/conformance/internal/testleaf":
				b.WriteString("\tdecimal \"github.com/neal/zerojson/conformance/internal/testleaf\"\n")
			default:
				b.WriteString("\t\"" + imp + "\"\n")
			}
		}
		b.WriteString(")\n\n")
	}
	b.WriteString(e.decls)
	b.WriteString("\n")
	b.WriteString(rootDecl)
	b.WriteString("\n")
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildGenerator compiles the generator once into a temp binary and returns
// its path; reused across every combo so the matrix isn't paying `go run`
// recompilation per case.
func buildGenerator(t *testing.T, moduleRoot string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "zerojsongen")
	args := []string{"build"}
	if os.Getenv("ZEROJSON_GENERATOR_COVERAGE") != "" {
		args = append(args, "-cover")
	}
	args = append(args, "-o", bin, "github.com/neal/zerojson/cmd/zerojsongen")
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building generator: %v\n%s", err, out)
	}
	return bin
}

func TestGeneratorCompileMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("compile matrix spawns the generator + go build per combo")
	}
	moduleRoot, err := filepath.Abs("..") // conformance module root
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)

	// One parent temp dir under the module so every generated package
	// resolves the runtime/uuid/decimal deps through the existing module.
	base, err := os.MkdirTemp(moduleRoot, "zjmatrix")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	runGen := func(dir string, args ...string) (string, error) {
		full := append([]string{"-dir", dir, "-out", filepath.Join(dir, "zj_gen.go"), "-leaf", testLeafSpec}, args...)
		cmd := exec.Command(gen, full...)
		cmd.Dir = moduleRoot
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	goBuild := func(dir string) (string, error) {
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	rootKinds := []string{"sliceT", "slicePtrT", "mapT", "structT"}
	n := 0
	for _, e := range matrixElems() {
		for _, rk := range rootKinds {
			rootDecl, refuse := matrixRoot(rk, e.typ, e.ptrOK)
			name := e.name + "/" + rk
			t.Run(name, func(t *testing.T) {
				dir := filepath.Join(base, e.name+"_"+rk)
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeMatrixPkg(t, dir, e, rootDecl)

				out, genErr := runGen(dir, "-types", "Root")
				if refuse {
					if genErr == nil {
						t.Fatalf("generator accepted a combination it should refuse\noutput: %s", out)
					}
					if !strings.Contains(out, "unsupported pointer-element slice") {
						t.Fatalf("refusal message not as expected:\n%s", out)
					}
					return
				}
				if genErr != nil {
					t.Fatalf("generator failed: %v\n%s", genErr, out)
				}
				if bout, err := goBuild(dir); err != nil {
					t.Fatalf("generated package does not compile: %v\n%s", err, bout)
				}
			})
			n++
		}
	}
	t.Logf("compile matrix: %d combinations", n)

	// Flag matrix over a representative combo ([]Elem): every flag-driven
	// emit path (pool arena, compat wrappers, ordered-off, and the -tests
	// fuzz file) must also compile. -tests emits a _test.go, so those combos
	// compile with `go test` (which type-checks test files) running nothing.
	flagSets := []struct {
		name  string
		flags []string
		tests bool
	}{
		{"default", nil, false},
		{"pool", []string{"-pool"}, false},
		{"compat", []string{"-compat"}, false},
		{"no-ordered", []string{"-no-ordered"}, false},
		{"tests", []string{"-pool"}, true},
	}
	for _, fs := range flagSets {
		t.Run("flags/"+fs.name, func(t *testing.T) {
			dir := filepath.Join(base, "flags_"+fs.name)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeMatrixPkg(t, dir, matrixElem{name: "struct", typ: "Elem", decls: matrixStructDecl, ptrOK: true}, "type Root []Elem")

			args := append([]string{"-types", "Root"}, fs.flags...)
			if fs.tests {
				args = append(args, "-tests", filepath.Join(dir, "zj_fuzz_test.go"))
			}
			if out, err := runGen(dir, args...); err != nil {
				t.Fatalf("generator failed: %v\n%s", err, out)
			}
			if fs.tests {
				cmd := exec.Command("go", "test", "-run", "^$", "-count=1", "./...")
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("generated package (with -tests) does not compile: %v\n%s", err, out)
				}
				return
			}
			if out, err := goBuild(dir); err != nil {
				t.Fatalf("generated package does not compile: %v\n%s", err, out)
			}
		})
	}
}

// TestGeneratorRefusesUintptr pins the generate-time rejection of a uintptr
// field: a raw machine-pointer value has no portable JSON representation, so
// the generator must refuse it (like a non-string map key) rather than emit a
// 64-bit read that would silently wrap on a 32-bit target. Asserted for a
// uintptr both as a struct field and as a slice element.
func TestGeneratorRefusesUintptr(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the generator per case")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	base, err := os.MkdirTemp(moduleRoot, "zjuintptr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	cases := []struct{ name, root string }{
		{"field", "type Root struct {\n\tP uintptr `json:\"p\"`\n}"},
		{"slice", "type Root []uintptr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, tc.name)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeMatrixPkg(t, dir, matrixElem{name: tc.name}, tc.root)
			cmd := exec.Command(gen, "-dir", dir, "-out", filepath.Join(dir, "zj_gen.go"), "-types", "Root")
			cmd.Dir = moduleRoot
			out, genErr := cmd.CombinedOutput()
			if genErr == nil {
				t.Fatalf("generator accepted a uintptr field it should refuse\noutput: %s", out)
			}
			if !strings.Contains(string(out), "uintptr field is not supported") {
				t.Fatalf("refusal message not as expected:\n%s", out)
			}
		})
	}
}

func TestGeneratorInteractionCompileGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	base, err := os.MkdirTemp(moduleRoot, "zjinteractions")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	run := func(t *testing.T, name, src string, wantRefusal string) {
		t.Helper()
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte("package p\n\n"+src+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(gen, "-dir", dir, "-types", "Root", "-out", filepath.Join(dir, "zj_gen.go"))
		cmd.Dir = moduleRoot
		out, genErr := cmd.CombinedOutput()
		if wantRefusal != "" {
			if genErr == nil || !strings.Contains(string(out), wantRefusal) {
				t.Fatalf("expected refusal containing %q, err=%v\n%s", wantRefusal, genErr, out)
			}
			return
		}
		if genErr != nil {
			t.Fatalf("generator failed: %v\n%s", genErr, out)
		}
		build := exec.Command("go", "build", "./...")
		build.Dir = dir
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("generated package does not compile: %v\n%s", err, out)
		}
	}

	t.Run("embedded arena name collision", func(t *testing.T) {
		run(t, "collision", "type A struct { B *int `json:\"b\"` }\ntype Root struct { A; A_B *int `json:\"direct\"` }", "")
	})
	t.Run("recursive schema refused", func(t *testing.T) {
		run(t, "recursive", "type Root struct { Next *Root `json:\"next\"` }", "recursive generated type graph")
	})
	t.Run("wrong Lexer package not accepted", func(t *testing.T) {
		run(t, "lexer", "type Lexer struct{}\ntype Almost int\nfunc (Almost) AppendZJSON(dst []byte) ([]byte, error) { return dst, nil }\nfunc (*Almost) ReadZJSON(*Lexer) {}\ntype Root struct { Value Almost `json:\"value\"` }", "has only one of AppendZJSON/ReadZJSON")
	})
}
