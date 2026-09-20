package generator

import (
	"embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Cross-field aliases are a graph property: independently initialized field
// fixtures and DeepEqual of the visible result alone cannot cover them.
func TestSharedStorageReplay(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, root)
	dir, err := os.MkdirTemp(root, "zjsharedstorage")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	source := "package p\ntype Row struct { N int }\n"
	tests := "package p\nimport (\"encoding/json\";\"reflect\";\"testing\";zj \"github.com/neal/zerojson\")\n"
	for _, c := range []struct{ name, elem, seed, values string }{
		{"Ints", "int", "9,8,7", "`null`,`[]`,`[null]`,`[1]`,`[null,null]`,`[2,3]`,`[null,3,4]`"},
		{"Bytes", "byte", "9,8,7", "`null`,`[]`,`[null]`,`[1]`,`[null,null]`,`[2,3]`,`[null,3,4]`"},
		{"Rows", "Row", "{9},{8},{7}", "`null`,`[]`,`[null]`,`[{\"N\":1}]`,`[null,null]`,`[{\"N\":2},{\"N\":3}]`,`[null,{\"N\":3},{\"N\":4}]`"},
	} {
		source += "type " + c.name + " struct { A []" + c.elem + "; B []" + c.elem + "; C int }; type Outer" + c.name + " struct { Child " + c.name + " }\n"
		tests += strings.NewReplacer("$NAME", c.name, "$ELEM", c.elem, "$SEED", c.seed, "$VALUES", c.values).Replace(sharedStorageFixture)
	}
	for name, data := range map[string]string{"types.go": source, "shared_test.go": tests} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := sharedStorageSources.ReadDir("testdata/shared_storage")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		data, err := sharedStorageSources.ReadFile("testdata/shared_storage/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	pkg := "github.com/neal/zerojson/conformance/" + filepath.Base(dir)
	cmd := exec.Command(gen, "-dir", dir, "-types", "Ints,Bytes,Rows,OuterInts,OuterBytes,OuterRows,Graph,Callbacks,Leaves", "-pool",
		"-leaf", pkg+".Leaf="+pkg+".AppendLeaf,"+pkg+".ReadLeaf")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	args := []string{"test", ".", "-count=1"}
	if os.Getenv("ZEROJSON_CHALLENGE_RACE") != "" {
		args = append(args, "-race", "-gcflags=all=-d=checkptr=2")
	}
	cmd = exec.Command("go", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shared storage: %v\n%s", err, out)
	}
}

//go:embed testdata/shared_storage/slices.go.tmpl
var sharedStorageFixture string

//go:embed testdata/shared_storage/*.go
var sharedStorageSources embed.FS
