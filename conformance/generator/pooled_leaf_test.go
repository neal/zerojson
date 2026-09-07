package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPooledPointerLeafAndSamePackageLeaf pins two generator gaps found by
// review: (1) a pointer custom-leaf field under -pool must seed its arena
// cell from the target (or zero it) before the leaf Read runs, so a Read
// that MERGES into its target cannot accumulate across pooled decodes of
// fresh targets; (2) a -leaf codec that lives in the package being
// generated must be called unqualified rather than through a self-import.
func TestPooledPointerLeafAndSamePackageLeaf(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjpoolleaf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	rel, err := filepath.Rel(moduleRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	importPath := "github.com/neal/zerojson/conformance/" + filepath.ToSlash(rel)

	src := `package p

import "github.com/neal/zerojson"

// Acc is a custom leaf whose Read MERGES (appends) into its target.
type Acc []int64

func AppendAcc(dst []byte, v *Acc) []byte {
	if len(*v) == 0 {
		return append(dst, "null"...)
	}
	return zerojson.AppendInt64(dst, (*v)[len(*v)-1])
}

func ReadAcc(l *zerojson.Lexer, v *Acc) { *v = append(*v, l.ReadInt64()) }

type T struct {
	A *Acc ` + "`json:\"a\"`" + `
}
`
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	test := `package p

import "testing"

func TestPooledMergeLeafDoesNotAccumulate(t *testing.T) {
	var d TDecoder
	for i := 0; i < 3; i++ {
		var v T
		if err := d.Decode([]byte(` + "`{\"a\":1}`" + `), &v); err != nil {
			t.Fatal(err)
		}
		if v.A == nil || len(*v.A) != 1 || (*v.A)[0] != 1 {
			t.Fatalf("decode #%d: A=%v, want [1] — the pooled cell leaked previous state", i, *v.A)
		}
	}
	// An existing target is merged into (the leaf's own semantics), once.
	existing := Acc{5}
	v := T{A: &existing}
	if err := v.UnmarshalZJSON([]byte(` + "`{\"a\":1}`" + `)); err != nil {
		t.Fatal(err)
	}
	if len(*v.A) != 2 || (*v.A)[0] != 5 || (*v.A)[1] != 1 {
		t.Fatalf("existing target: A=%v, want [5 1]", *v.A)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	leaf := importPath + ".Acc=" + importPath + ".AppendAcc," + importPath + ".ReadAcc"
	cmd := exec.Command(gen, "-dir", dir, "-types", "T", "-pool", "-leaf", leaf, "-out", filepath.Join(dir, "zerojson_gen.go"))
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	run := exec.Command("go", "test", "./"+filepath.ToSlash(rel)+"/...")
	run.Dir = moduleRoot
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("generated package failed (self-import cycle or pooled leak):\n%s", out)
	}
}
