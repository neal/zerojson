package generator

import (
	"bytes"
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
	depDir := filepath.Join(base, "dep")
	write(filepath.Join(depDir, "types.go"), "package dep\ntype hidden struct { N int `json:\"n\"` }; type Embedded struct { hidden }; type private int; type Public struct { N private `json:\"n\"` }; type Alias = private; type ViaAlias struct { N Alias `json:\"n\"` }\n")
	depPath := "github.com/neal/zerojson/conformance/" + filepath.Base(base) + "/dep"
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
