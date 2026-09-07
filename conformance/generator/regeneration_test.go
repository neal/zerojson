package generator

import (
	"os"
	"os/exec"
	"path/filepath"
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
