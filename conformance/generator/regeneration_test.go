package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegenerateWithPoolConsumerAfterRename pins the stub overlay: a
// -pool Decoder referenced by hand-written code in the same package must
// not block regeneration after a field rename.
func TestRegenerateWithPoolConsumerAfterRename(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjstalepool")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", "package p\n\ntype T struct {\n\tOld int `json:\"old\"`\n}\n")
	run := func() ([]byte, error) {
		cmd := exec.Command(gen, "-dir", dir, "-types", "T", "-pool", "-out", "zerojson_gen.go")
		cmd.Dir = moduleRoot
		return cmd.CombinedOutput()
	}
	if out, err := run(); err != nil {
		t.Fatalf("first generation failed: %v\n%s", err, out)
	}
	// A hand-written consumer of the generated -pool Decoder type,
	// written once the type exists (a consumer cannot precede the very
	// first generation, since it references a type nothing declares yet).
	write("consumer.go", "package p\n\nvar sharedDecoder TDecoder\n\nfunc Use(data []byte) (T, error) {\n\tvar v T\n\terr := sharedDecoder.Decode(data, &v)\n\treturn v, err\n}\n")
	// Rename the field: the previous zerojson_gen.go now references T.Old,
	// which no longer exists, while consumer.go still needs TDecoder.
	write("types.go", "package p\n\ntype T struct {\n\tNew int `json:\"new\"`\n}\n")
	if out, err := run(); err != nil {
		t.Fatalf("regeneration after a rename failed with a -pool consumer present: %v\n%s", err, out)
	}
	rel, _ := filepath.Rel(moduleRoot, dir)
	vet := exec.Command("go", "vet", "./"+filepath.ToSlash(rel)+"/...")
	vet.Dir = moduleRoot
	if out, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("regenerated package does not vet:\n%s", out)
	}
}

// TestRegenerateWithPoolConsumerAfterTypeRename pins the pruning tier of
// the overlay: renaming a NESTED generated type leaves the previous
// output's stub with methods and arena fields that name the old type.
// Those declarations must be dropped one by one — not the whole file —
// so the -pool Decoder for the (unrenamed) root that hand-written code
// references still type-checks and regeneration proceeds.
func TestRegenerateWithPoolConsumerAfterTypeRename(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjrenametype")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", "package p\n\ntype Cell struct {\n\tX int `json:\"x\"`\n}\n\ntype Outer struct {\n\tC Cell `json:\"c\"`\n\tL []Cell `json:\"l\"`\n}\n")
	run := func() ([]byte, error) {
		cmd := exec.Command(gen, "-dir", dir, "-types", "Outer", "-pool", "-out", "zerojson_gen.go")
		cmd.Dir = moduleRoot
		return cmd.CombinedOutput()
	}
	if out, err := run(); err != nil {
		t.Fatalf("first generation failed: %v\n%s", err, out)
	}
	write("consumer.go", "package p\n\nvar sharedDecoder OuterDecoder\n\nfunc Use(data []byte) (Outer, error) {\n\tvar v Outer\n\terr := sharedDecoder.Decode(data, &v)\n\treturn v, err\n}\n")
	// Rename the nested type: the old output's methods on Cell, and the
	// arena cells typed []Cell, no longer resolve — but OuterDecoder must.
	write("types.go", "package p\n\ntype Cell2 struct {\n\tX int `json:\"x\"`\n}\n\ntype Outer struct {\n\tC Cell2 `json:\"c\"`\n\tL []Cell2 `json:\"l\"`\n}\n")
	if out, err := run(); err != nil {
		t.Fatalf("regeneration after a nested type rename failed with a -pool consumer present: %v\n%s", err, out)
	}
	rel, _ := filepath.Rel(moduleRoot, dir)
	vet := exec.Command("go", "vet", "./"+filepath.ToSlash(rel)+"/...")
	vet.Dir = moduleRoot
	if out, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("regenerated package does not vet:\n%s", out)
	}
}

// TestGeneratedNamedMapMethodsAreReplaced pins the classification of a
// same-package named map whose JSON methods are generated output: like a
// struct's, they are what this run replaces, so a field of that type gets
// the inline map codec instead of delegating to the methods. Before this,
// regenerating a -compat package that hand-written code depends on (so the
// previous output cannot be blanked) flipped such fields to MarshalJSON /
// UnmarshalJSON delegation and pushed the root slice decode onto
// encoding/json, while easyjson-generated methods were delegated to on the
// first cutover run.
func TestGeneratedNamedMapMethodsAreReplaced(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	const types = "package p\n\ntype Dict map[string]any\n\ntype Op struct {\n\tName string `json:\"name\"`\n\tSet  Dict   `json:\"set,omitempty\"`\n}\n\ntype Ops []Op\n"
	setup := func(t *testing.T, files map[string]string) (dir string, generate func(roots string, flags ...string) string) {
		dir, err := os.MkdirTemp(moduleRoot, "zjnamedmap")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir, func(roots string, flags ...string) string {
			args := append([]string{"-dir", dir, "-types", roots, "-out", "zerojson_gen.go"}, flags...)
			cmd := exec.Command(gen, args...)
			cmd.Dir = moduleRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate %v: %v\n%s", args, err, out)
			}
			out, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range []string{"v.Set.MarshalJSON", "v.Set.UnmarshalJSON"} {
				if strings.Contains(string(out), call) {
					t.Errorf("generated code delegates to %s; the generated methods should be replaced by the inline map codec", call)
				}
			}
			return string(out)
		}
	}
	vet := func(t *testing.T, dir string) {
		rel, _ := filepath.Rel(moduleRoot, dir)
		cmd := exec.Command("go", "vet", "./"+filepath.ToSlash(rel)+"/...")
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated package does not vet:\n%s", out)
		}
	}

	t.Run("compat_regeneration_is_stable", func(t *testing.T) {
		dir, generate := setup(t, map[string]string{"types.go": types})
		first := generate("Ops,Dict", "-compat")
		// Hand-written code that needs the -compat methods keeps the
		// package from type-checking without the previous output, so the
		// regeneration below loads with those methods visible.
		if err := os.WriteFile(filepath.Join(dir, "consumer.go"), []byte("package p\n\nfunc Encode(o Op, d Dict) ([]byte, []byte) {\n\ta, _ := o.MarshalJSON()\n\tb, _ := d.MarshalJSON()\n\treturn a, b\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if second := generate("Ops,Dict", "-compat"); second != first {
			t.Fatal("regeneration with the previous -compat output visible changed the generated code")
		}
		vet(t, dir)
	})

	t.Run("easyjson_methods_are_replaced", func(t *testing.T) {
		dir, generate := setup(t, map[string]string{
			"types.go": types,
			"dict_easyjson.go": "// Code generated by easyjson for marshaling/unmarshaling. DO NOT EDIT.\n\npackage p\n\nimport \"encoding/json\"\n\ntype dictView map[string]any\n\n" +
				"func (v Dict) MarshalJSON() ([]byte, error) { return json.Marshal(dictView(v)) }\n\nfunc (v *Dict) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, (*dictView)(v)) }\n",
		})
		generate("Ops")
		vet(t, dir)
	})
}
