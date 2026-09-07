package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedRawOracles proves the emitted tests reject stable-but-wrong
// decoders, not just invalid output. Raw implements JSON methods, so it has
// a real stdlib oracle in structs, root containers, and nested graphs.
func TestGeneratedRawOracles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and mutates a generated fixture")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjraworacle")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", "package p\nimport \"github.com/neal/zerojson\"\n"+
		"type Holder struct { R zerojson.Raw `json:\"r\"`; N int `json:\"n\"` }\n"+
		"type List []zerojson.Raw\ntype Index map[string]zerojson.Raw\n"+
		"type Nested []Holder\n"+
		"type Graph struct { Items []Holder `json:\"items\"`; Index map[string]zerojson.Raw `json:\"index\"` }\n")
	cmd := exec.Command(gen, "-dir", dir, "-types", "Holder,List,Index,Nested,Graph", "-out", "zerojson_gen.go", "-tests", "zerojson_gen_test.go")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate Raw oracle fixture: %v\n%s", err, out)
	}
	codec, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	fuzzBytes, err := os.ReadFile(filepath.Join(dir, "zerojson_gen_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fuzz := string(fuzzBytes)
	shapes := []struct{ name, doc string }{
		{"Holder", `{"r": { "a":1, "a":2 },"n":3}`},
		{"List", `[null, { "x": [1,2] }, "\u0061"]`},
		{"Index", `{"a":1,"b": [2,3],"c":null,"d":"x","e":{},"f":[]}`},
		{"Nested", `[{"r": { "x":1 }, "n":3}]`},
		{"Graph", `{"items":[{"r": [1,2],"n":3}],"index":{"a":1,"b":2,"c":3}}`},
	}
	for _, shape := range shapes {
		anchor := fmt.Sprintf("func Fuzz%sZJSON(f *testing.F) {", shape.name)
		if strings.Count(fuzz, anchor) != 1 {
			t.Fatalf("missing unique fuzzer anchor %s", shape.name)
		}
		// Include exact Raw spans and a numeric root type error. Neither
		// may disappear through canonicalization or early Raw returns.
		fuzz = strings.Replace(fuzz, anchor, anchor+fmt.Sprintf("\n f.Add([]byte(%q))\n f.Add([]byte(`1`))\n", shape.doc), 1)
	}
	write("zerojson_gen_test.go", fuzz)
	run := func(name string, count int) ([]byte, error) {
		cmd := exec.Command("go", "test", "./"+filepath.Base(dir), "-run", name, fmt.Sprintf("-count=%d", count), "-timeout=60s")
		cmd.Dir = moduleRoot
		return cmd.CombinedOutput()
	}
	// Repetition also catches false byte-order assertions on map output.
	if out, err := run("^Fuzz", 10); err != nil {
		t.Fatalf("unmodified Raw fuzzers failed: %v\n%s", err, out)
	}
	for _, shape := range shapes {
		for _, mutant := range []struct{ name, inject, diagnostic string }{
			{"wrong-value", fmt.Sprintf("defer func(){ var zero %s; *v=zero }()", shape.name), "raw decode value divergence"},
			{"reject-valid", "if len(data)>0 { return zerojson.ErrMalformedDecimal }", "that encoding/json accepted"},
			{"accept-invalid-value", fmt.Sprintf("if string(data)==\"1\" { var zero %s; *v=zero; return nil }", shape.name), "stdlib rejected value"},
		} {
			t.Run(shape.name+"/"+mutant.name, func(t *testing.T) {
				anchor := fmt.Sprintf("func (v *%s) UnmarshalZJSON(data []byte) error {", shape.name)
				if strings.Count(string(codec), anchor) != 1 {
					t.Fatalf("missing unique decoder anchor %s", shape.name)
				}
				write("zerojson_gen.go", strings.Replace(string(codec), anchor, anchor+"\n"+mutant.inject+"\n", 1))
				defer write("zerojson_gen.go", string(codec))
				out, err := run("^Fuzz"+shape.name+"ZJSON$", 1)
				if err == nil {
					t.Fatal("emitted oracle missed the deliberately broken decoder")
				}
				if !strings.Contains(string(out), mutant.diagnostic) {
					t.Fatalf("mutant failed for the wrong reason, want %q:\n%s", mutant.diagnostic, out)
				}
			})
		}
	}
}
