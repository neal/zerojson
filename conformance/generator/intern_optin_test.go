package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMapKeyInterningOptIn pins that generated map decoders intern keys only
// when the map field carries zerojson:"intern". Map keys are
// attacker-controlled and unbounded in cardinality, so interning them by
// default is a resource hazard; the default must be a plain copy. This is a
// source-level guard because interning is invisible to value-level oracles —
// a regression to unconditional interning would pass every differential test.
func TestMapKeyInterningOptIn(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjintern")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	types := "package p\n\n" +
		"type Mode string\n\n" +
		"const ModeKnown Mode = \"known\"\n\n" +
		"type Tagged struct {\n\tM map[string]int `json:\"m\" zerojson:\"intern\"`\n}\n\n" +
		"type Untagged struct {\n\tM map[string]int `json:\"m\"`\n}\n\n" +
		"type TaggedEnum struct {\n\tE Mode `json:\"e\" zerojson:\"intern\"`\n}\n\n" +
		"type UntaggedEnum struct {\n\tE Mode `json:\"e\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(types), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gen, "-dir", dir, "-types", "Tagged,Untagged,TaggedEnum,UntaggedEnum", "-out", filepath.Join(dir, "zerojson_gen.go"))
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	src, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)

	// Associate each behavior with its type. Global occurrence counts would
	// miss an inverted condition because one copied and one interned path would
	// still exist.
	taggedMap := mapDecoders(t, code, "Tagged")
	untaggedMap := mapDecoders(t, code, "Untagged")
	if !strings.Contains(taggedMap, "zerojson.Intern(l.ReadKey())") {
		t.Error("tagged map key is not interned")
	}
	if strings.Contains(taggedMap, "string(l.ReadKey())") {
		t.Error("tagged map key is copied instead of interned")
	}
	if !strings.Contains(untaggedMap, "string(l.ReadKey())") {
		t.Error("untagged map key is not copied")
	}
	if strings.Contains(untaggedMap, "zerojson.Intern(l.ReadKey())") {
		t.Error("untagged map key is interned by default")
	}

	// Known enum values always use declared constants. Unknown values are
	// arbitrary input and are copied unless the field explicitly opts in.
	taggedEnum := keyedLoop(t, code, "TaggedEnum")
	untaggedEnum := keyedLoop(t, code, "UntaggedEnum")
	if !strings.Contains(taggedEnum, "Mode(zerojson.Intern(b))") {
		t.Error("tagged enum unknown-value fallback is not interned")
	}
	if strings.Contains(taggedEnum, "Mode(string(b))") {
		t.Error("tagged enum unknown-value fallback is copied")
	}
	if !strings.Contains(untaggedEnum, "Mode(string(b))") {
		t.Error("untagged enum unknown-value fallback is not copied")
	}
	if strings.Contains(untaggedEnum, "Mode(zerojson.Intern(b))") {
		t.Error("untagged enum unknown-value fallback is interned by default")
	}
}

// keyedLoop returns the generated function holding typ's keyed member loop:
// zjDecodeMembers_<T> when the type has an ordered decoder that can hand off
// to it, otherwise zjDecodeFallback_<T> itself.
func keyedLoop(t *testing.T, code, typ string) string {
	t.Helper()
	if strings.Contains(code, "func zjDecodeMembers_"+typ+"(") {
		return generatedFunction(t, code, "zjDecodeMembers_"+typ)
	}
	return generatedFunction(t, code, "zjDecodeFallback_"+typ)
}

// mapDecoders returns the zjReadMap helper typ's keyed loop calls, with
// its spill helper when it has one: map keys are read in both.
func mapDecoders(t *testing.T, code, typ string) string {
	t.Helper()
	call := regexp.MustCompile(`zjReadMap_(\w+)\(`).FindStringSubmatch(keyedLoop(t, code, typ))
	if call == nil {
		t.Fatalf("%s decodes no map through a zjReadMap helper", typ)
	}
	src := generatedFunction(t, code, "zjReadMap_"+call[1])
	if strings.Contains(code, "func zjSpillMap_"+call[1]+"(") {
		src += generatedFunction(t, code, "zjSpillMap_"+call[1])
	}
	return src
}

func generatedFunction(t *testing.T, code, name string) string {
	t.Helper()
	marker := "func " + name + "("
	start := strings.Index(code, marker)
	if start < 0 {
		t.Fatalf("generated function %s not found", name)
	}
	rest := code[start:]
	if next := strings.Index(rest[len(marker):], "\nfunc "); next >= 0 {
		rest = rest[:len(marker)+next]
	}
	return rest
}
