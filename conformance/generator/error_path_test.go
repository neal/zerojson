package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOrderedPathErrorCarriesEnclosingField pins the field path for an
// error inside a nested struct decoded on the ORDERED fast path (a
// callback-free schema): the enclosing field's name must be recorded even
// though the nested ordered decoder returns early on a real error.
func TestOrderedPathErrorCarriesEnclosingField(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjorderr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	src := "package p\n\ntype Inner struct {\n\tN int `json:\"n\"`\n}\n\ntype Deep struct {\n\tIn Inner `json:\"in\"`\n}\n\ntype Outer struct {\n\tA    int    `json:\"a\"`\n\tIn   Inner  `json:\"in\"`\n\tPtr  *Inner `json:\"ptr\"`\n\tDeep Deep   `json:\"deep\"`\n}\n"
	test := "package p\n\nimport (\n\t\"errors\"\n\t\"testing\"\n\n\t\"github.com/neal/zerojson\"\n)\n\nfunc TestPaths(t *testing.T) {\n\tfor _, c := range []struct{ doc, path string }{\n\t\t{`{\"a\":1,\"in\":{\"n\":\"x\"}}`, \"field in.n\"},\n\t\t{`{\"a\":1,\"ptr\":{\"n\":\"x\"}}`, \"field ptr.n\"},\n\t\t{`{\"a\":1,\"deep\":{\"in\":{\"n\":\"x\"}}}`, \"field deep.in.n\"},\n\t\t{`{\"in\":{\"n\":\"x\"},\"a\":1}`, \"field in.n\"},\n\t\t{`{ \"in\" : {\"n\":\"x\"}}`, \"field in.n\"},\n\t} {\n\t\tvar v Outer\n\t\terr := v.UnmarshalZJSON([]byte(c.doc))\n\t\tvar fe *zerojson.FieldError\n\t\tif err == nil || !errors.As(err, &fe) || fe.Path != c.path {\n\t\t\tt.Errorf(\"%s: err=%v want path %q\", c.doc, err, c.path)\n\t\t}\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gen, "-dir", dir, "-types", "Outer", "-out", "zerojson_gen.go")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	rel, _ := filepath.Rel(moduleRoot, dir)
	run := exec.Command("go", "test", "./"+filepath.ToSlash(rel)+"/...")
	run.Dir = moduleRoot
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("ordered-path error paths wrong:\n%s", out)
	}
}
