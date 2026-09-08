package generator

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageLayoutBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("generates multi-package codec fixtures")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	base, err := os.MkdirTemp(moduleRoot, "zjlayouts")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir, roots, out, tests string) (string, error) {
		args := []string{"-dir", dir, "-types", roots, "-out", out, "-pool"}
		if tests != "" {
			args = append(args, "-tests", tests)
		}
		cmd := exec.Command(gen, args...)
		cmd.Dir = moduleRoot
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	build := func(dir string) {
		t.Helper()
		cmd := exec.Command("go", "test", ".", "-count=1")
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated package: %v\n%s", err, b)
		}
	}
	t.Run("predeclared_dependency_names", func(t *testing.T) {
		dir := filepath.Join(base, "predeclared")
		imports, fields := "import (\n", "type T struct { S []int; M map[string]int; V any\n"
		for i, name := range types.Universe.Names() {
			dep := fmt.Sprintf("builtin%d", i)
			write(filepath.Join(base, dep, "types.go"), "package "+name+"\ntype Value int\n")
			imports += fmt.Sprintf("d%d %q\n", i, "github.com/neal/zerojson/conformance/"+filepath.Base(base)+"/"+dep)
			fields += fmt.Sprintf("N%d d%d.Value\n", i, i)
		}
		write(filepath.Join(dir, "types.go"), "package p\n"+imports+")\n"+fields+"}\n")
		var previous []byte
		for attempt := 0; attempt < 2; attempt++ {
			if out, err := run(dir, "T", "zerojson_gen.go", "codec_fuzz_test.go"); err != nil {
				t.Fatalf("generate: %v %s", err, out)
			}
			build(dir)
			var combined []byte
			for _, name := range []string{"zerojson_gen.go", "codec_fuzz_test.go"} {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				file, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
				if err != nil {
					t.Fatal(err)
				}
				ast.Inspect(file, func(n ast.Node) bool {
					if im, ok := n.(*ast.ImportSpec); ok && im.Name != nil && types.Universe.Lookup(im.Name.Name) != nil {
						t.Errorf("predeclared import qualifier: %s", im.Name.Name)
					}
					return true
				})
				combined = append(combined, data...)
			}
			if attempt > 0 && !bytes.Equal(previous, combined) {
				t.Fatal("regeneration changed output")
			}
			previous = combined
		}
	})
	depDir := filepath.Join(base, "dep")
	write(filepath.Join(depDir, "types.go"), "package dep\ntype hidden struct { N int `json:\"n\"` }; type Embedded struct { hidden }; type private int; type Public struct { N private `json:\"n\"` }; type Alias = private; type ViaAlias struct { N Alias `json:\"n\"` }\n")
	depPath := "github.com/neal/zerojson/conformance/" + filepath.Base(base) + "/dep"
	helperPath := "github.com/neal/zerojson/conformance/" + filepath.Base(base) + "/helper"
	write(filepath.Join(base, "helper", "types.go"), "package zjDecode_T\ntype Value int\ntype Mode string\nconst One Mode = \"one\"\n")
	oraclePath := "github.com/neal/zerojson/conformance/" + filepath.Base(base) + "/oraclehelper"
	write(filepath.Join(base, "oraclehelper", "types.go"), "package zjOracleData\ntype Value int\ntype Mode string\nconst One Mode = \"one\"\n")
	for _, tc := range []struct{ name, source, tests, roots, flags, refusal string }{
		{"helper_import", "import dep \"" + helperPath + "\"; type T struct { N dep.Value; Modes []dep.Mode }", "", "T", "", ""},
		{"test_helper_import", "import dep \"" + oraclePath + "\"; type T struct { N dep.Value; Modes []dep.Mode }", "", "T", "", ""},
		{"decoder_names", "type T struct { N int }; type OrderedT struct { M int }; type FallbackT struct { N int }; type OrderedFieldT struct { N int }", "", "T,OrderedT,FallbackT,OrderedFieldT", "", ""},
		{"field_method", "type T struct { AppendZJSON string }", "", "T", "", "method T.AppendZJSON"},
		{"test_fuzzer", "type T struct { N int }", "import \"testing\"; func FuzzTZJSON(f *testing.F) {}", "T", "", "test declaration"},
		{"test_alias", "type T struct { N int; V any }", "var json, json_, reflect, binary, big int", "T", "", ""},
		{"compat_embedding", "type Inner struct { N int }; type Outer struct { Inner }", "", "Outer,Inner", "-compat", ""},
		{"easyjson_alias", "var jwriter, jwriter_, jlexer, jlexer_ int; type T struct { N int }", "", "T", "-compat-easyjson", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, tc.name)
			write(filepath.Join(dir, "types.go"), "package p\n"+tc.source+"\n")
			if tc.tests != "" {
				write(filepath.Join(dir, "manual_test.go"), "package p\n"+tc.tests+"\n")
			}
			var previous []byte
			for attempt := 0; attempt < 2; attempt++ {
				args := []string{"-dir", dir, "-types", tc.roots}
				if tc.flags != "" {
					args = append(args, tc.flags)
				} else {
					args = append(args, "-tests", "codec_fuzz_test.go")
				}
				cmd := exec.Command(gen, args...)
				cmd.Dir = moduleRoot
				out, err := cmd.CombinedOutput()
				if tc.refusal != "" {
					if err == nil || !strings.Contains(string(out), tc.refusal) {
						t.Fatalf("want refusal %q: %v %s", tc.refusal, err, out)
					}
					if _, err := os.Stat(filepath.Join(dir, "zerojson_gen.go")); !os.IsNotExist(err) {
						t.Fatal("refusal wrote output")
					}
					return
				}
				if err != nil {
					t.Fatalf("generation %d: %v %s", attempt, err, out)
				}
				build(dir)
				b, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
				if err != nil {
					t.Fatal(err)
				}
				if tc.flags == "" {
					fuzz, err := os.ReadFile(filepath.Join(dir, "codec_fuzz_test.go"))
					if err != nil {
						t.Fatal(err)
					}
					b = append(b, fuzz...)
				}
				if attempt > 0 && !bytes.Equal(previous, b) {
					t.Fatal("regeneration changed output")
				}
				previous = b
			}
		})
	}
	t.Run("unexported_fuzz_names", func(t *testing.T) {
		dir := filepath.Join(base, "fuzznames")
		write(filepath.Join(dir, "types.go"), "package p\ntype hidden struct { N int }; type Hidden struct { N int }; type _hidden struct { N int }; type é struct { N int }; type É struct { N int }; type 猫 struct { N int }; type list []int; type index map[string]int\n")
		out, err := run(dir, "hidden,Hidden,_hidden,é,É,猫,list,index", "codec.go", "codec_fuzz_test.go")
		if err != nil {
			t.Fatalf("generate fuzz names: %v %s", err, out)
		}
		build(dir) // Includes vet and seed execution for struct and root fuzzers.
	})
	for _, tc := range []struct{ name, embedded, wantError string }{
		{"hidden_embed", "Embedded", "inaccessible embedded field"},
		{"hidden_type", "Public", "cannot name unexported type"},
		{"exported_alias", "ViaAlias", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, tc.name)
			write(filepath.Join(dir, "types.go"), "package p\nimport \""+depPath+"\"\ntype Root struct { dep."+tc.embedded+" }\n")
			out, err := run(dir, "Root", "codec.go", "")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(out, tc.wantError) {
					t.Fatalf("want refusal %q: %v %s", tc.wantError, err, out)
				}
				if _, err := os.Stat(filepath.Join(dir, "codec.go")); !os.IsNotExist(err) {
					t.Fatal("refused shape wrote output")
				}
			} else {
				if err != nil {
					t.Fatalf("exported alias should work: %v %s", err, out)
				}
				build(dir)
			}
		})
	}
	t.Run("package_identifiers", func(t *testing.T) {
		dir := filepath.Join(base, "identifiers")
		write(filepath.Join(dir, "types.go"), `package p
var binary, atomic, json, json_, bytes, errors, strings, reflect, testing int
type Root struct { V any; N *int }
`)
		out, err := run(dir, "Root", "codec.go", "codec_fuzz_test.go")
		if err != nil {
			t.Fatalf("alias package identifiers: %v %s", err, out)
		}
		build(dir)
		// Repeated generation must reserve names consistently as well.
		out, err = run(dir, "Root", "codec.go", "codec_fuzz_test.go")
		if err != nil {
			t.Fatalf("regenerate: %v %s", err, out)
		}
		build(dir)
	})
	t.Run("stale_tests_output", func(t *testing.T) {
		// Dropping -tests after a run that passed it must not leave the
		// generated fuzz file behind: it would keep testing the old codec
		// and stop compiling after a rename, while looking like output.
		dir := filepath.Join(base, "staletests")
		write(filepath.Join(dir, "types.go"), "package p\ntype Rec struct { X int `json:\"x\"` }\n")
		if out, err := run(dir, "Rec", "codec.go", "codec_fuzz_test.go"); err != nil {
			t.Fatalf("generate with tests: %v %s", err, out)
		}
		write(filepath.Join(dir, "types.go"), "package p\ntype Record struct { X int `json:\"x\"` }\n")
		before, _ := os.ReadFile(filepath.Join(dir, "codec.go"))
		out, err := run(dir, "Record", "codec.go", "")
		if err == nil || !strings.Contains(out, "previous -tests run") {
			t.Fatalf("stale generated tests not refused: %v %s", err, out)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "codec.go"))
		if !bytes.Equal(before, after) {
			t.Fatal("refusal modified the codec output")
		}
		if out, err := run(dir, "Record", "codec.go", "codec_fuzz_test.go"); err != nil {
			t.Fatalf("regenerate with tests: %v %s", err, out)
		}
		build(dir)
	})
	t.Run("handwritten_name_collisions", func(t *testing.T) {
		// A generated declaration that a hand-written one already claims
		// is refused with its location, not written into a file that
		// cannot compile ("redeclared in this block").
		for _, tc := range []struct{ name, src, flag, want string }{
			{"builtin_append", "package p\nvar append = 1\ntype T struct { X int }\n", "-pool", "predeclared name append is already declared by hand"},
			{"builtin_len", "package p\nvar len = 1\ntype T struct { X []int }\n", "-pool", "predeclared name len is already declared by hand"},
			{"builtin_type", "package p\ntype byte string\ntype T struct { X int }\n", "-pool", "predeclared name byte is already declared by hand"},
			{"pool_decoder_type", "package p\ntype T struct { X int `json:\"x\"` }\ntype TDecoder struct{}\n", "-pool", "type TDecoder is already declared by hand"},
			{"easyjson_method", "package p\nimport \"github.com/mailru/easyjson/jwriter\"\ntype T struct { X int `json:\"x\"` }\nfunc (t T) MarshalEasyJSON(w *jwriter.Writer) { w.RawString(\"{}\") }\n", "-compat-easyjson", "method T.MarshalEasyJSON is already declared by hand"},
			{"arena_type", "package p\ntype T struct { X int `json:\"x\"` }\ntype zjArenaT int\n", "-pool", "type zjArenaT is already declared by hand"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := filepath.Join(base, "collide_"+tc.name)
				write(filepath.Join(dir, "types.go"), tc.src)
				cmd := exec.Command(gen, "-dir", dir, "-types", "T", "-out", "codec.go", tc.flag)
				cmd.Dir = moduleRoot
				b, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(b), tc.want) {
					t.Fatalf("want refusal %q: %v %s", tc.want, err, b)
				}
				if _, err := os.Stat(filepath.Join(dir, "codec.go")); !os.IsNotExist(err) {
					t.Fatal("refusal wrote output")
				}
			})
		}
		// The previous output's own declarations are replaceable, so
		// regenerating in place with the same flags keeps working.
		dir := filepath.Join(base, "collide_regen")
		write(filepath.Join(dir, "types.go"), "package p\ntype T struct { X int `json:\"x\"` }\n")
		for i := 0; i < 2; i++ {
			cmd := exec.Command(gen, "-dir", dir, "-types", "T", "-out", "codec.go", "-pool", "-compat-easyjson")
			cmd.Dir = moduleRoot
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("regeneration %d: %v %s", i, err, b)
			}
		}
		build(dir)
	})
	t.Run("foreign_plain_struct_message", func(t *testing.T) {
		// A struct from another package with no codec is refused with the
		// actual remedy (generate its codec there), not only the leaf/
		// Marshaler suggestions.
		write(filepath.Join(depDir, "plain.go"), "package dep\ntype Money struct { Units int64 `json:\"units\"` }\n")
		dir := filepath.Join(base, "foreignplain")
		write(filepath.Join(dir, "types.go"), "package p\nimport \""+depPath+"\"\ntype Root struct { M dep.Money `json:\"m\"` }\n")
		out, err := run(dir, "Root", "codec.go", "")
		if err == nil || !strings.Contains(out, "run zerojsongen in "+depPath+" with -types Money") {
			t.Fatalf("want remedy in refusal: %v %s", err, out)
		}
	})
	t.Run("multiple_outputs", func(t *testing.T) {
		dir := filepath.Join(base, "multiple")
		write(filepath.Join(dir, "types.go"), "package p\ntype Shared struct { N int }; type A struct { S Shared }; type B struct { S Shared }\n")
		out, err := run(dir, "A", "first.go", "first_test.go")
		if err != nil {
			t.Fatalf("first output: %v %s", err, out)
		}
		before, _ := os.ReadFile(filepath.Join(dir, "first.go"))
		out, err = run(dir, "B", "second.go", "second_test.go")
		if err == nil || !strings.Contains(out, "multiple zerojson") {
			t.Fatalf("split outputs not refused: %v %s", err, out)
		}
		if _, err := os.Stat(filepath.Join(dir, "second.go")); !os.IsNotExist(err) {
			t.Fatal("refusal wrote second output")
		}
		out, err = run(dir, "A,B", "first.go", "second_test.go")
		if err == nil || !strings.Contains(out, "multiple zerojson test outputs") {
			t.Fatalf("split fuzz helpers not refused: %v %s", err, out)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "first.go"))
		if !bytes.Equal(before, after) {
			t.Fatal("refused tests output modified codec")
		}
		// The supported way to add roots is updating the same output pair.
		out, err = run(dir, "A,B", "first.go", "first_test.go")
		if err != nil {
			t.Fatalf("combined output: %v %s", err, out)
		}
		build(dir)
	})
}
