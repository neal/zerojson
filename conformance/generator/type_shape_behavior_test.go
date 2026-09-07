package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Compile real codecs, then compare them with stdlib on boundary type shapes
// and pre-existing alias graphs. Byte fuzzing on zero-valued fixtures cannot
// discover either a missing Go type combination or a shared caller pointer.
func TestTypeShapeBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and runs boundary-shape codecs")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	for _, mode := range []string{"ordered", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := os.MkdirTemp(moduleRoot, "zjtypeshapes")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			for _, name := range []string{"types.go", "shapes_test.go"} {
				data, err := os.ReadFile(filepath.Join("testdata", "type_shapes", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-dir", dir, "-out", "zerojson_gen.go", "-pool", "-types",
				"MapHolder,TokenIndex,TextIndex,PointerIndex,Named,Options,Ignored,ByteHolder,Arrays,Refs,CustomRefs,Interfaces,TextHolder,TextList"}
			if mode == "fallback" {
				args = append(args, "-no-ordered")
			}
			cmd := exec.Command(gen, args...)
			cmd.Dir = moduleRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate: %v\n%s", err, out)
			}
			args = []string{"test", "-race", "-count=1", "./" + filepath.Base(dir)}
			if os.Getenv("ZEROJSON_CHECKPTR") != "" {
				args = append(args, "-gcflags=all=-d=checkptr=2")
			}
			cmd = exec.Command("go", args...)
			cmd.Dir = moduleRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("boundary shapes: %v\n%s", err, out)
			}
		})
	}
}
