package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLeafPackageQualifierUsesDeclaredName pins that a -leaf codec package
// is qualified in the generated file by its DECLARED package name, not
// the last element of its import path (a directory `weirdfolder` may
// declare `package money`), and that enum slice/map elements decode
// through a per-type helper that copies unknown values instead of
// interning them (interning is a per-field opt-in).
func TestLeafPackageQualifierUsesDeclaredName(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjleafname")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	rel, err := filepath.Rel(moduleRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	importBase := "github.com/neal/zerojson/conformance/" + filepath.ToSlash(rel)

	if err := os.MkdirAll(filepath.Join(dir, "weirdfolder"), 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := "package money\n\nimport \"github.com/neal/zerojson\"\n\n" +
		"type Money int64\n\n" +
		"func Append(dst []byte, v *Money) []byte { return zerojson.AppendInt64(dst, int64(*v)) }\n" +
		"func Read(l *zerojson.Lexer, v *Money) { *v = Money(l.ReadInt64()) }\n"
	if err := os.WriteFile(filepath.Join(dir, "weirdfolder", "money.go"), []byte(leaf), 0o644); err != nil {
		t.Fatal(err)
	}
	types := "package p\n\nimport money \"" + importBase + "/weirdfolder\"\n\n" +
		"type Level string\n\nconst LevelHigh Level = \"high\"\n\n" +
		"type T struct {\n\tM  money.Money       `json:\"m\"`\n\tLs []Level           `json:\"ls\"`\n\tLm map[string]Level `json:\"lm\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(types), 0o644); err != nil {
		t.Fatal(err)
	}
	leafSpec := importBase + "/weirdfolder.Money=" + importBase + "/weirdfolder.Append," + importBase + "/weirdfolder.Read"
	cmd := exec.Command(gen, "-dir", dir, "-types", "T", "-leaf", leafSpec, "-out", filepath.Join(dir, "zerojson_gen.go"))
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	src, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if strings.Contains(code, "weirdfolder.") {
		t.Error("leaf codec qualified by the import path's last element instead of the declared package name")
	}
	if !strings.Contains(code, "money.Append(") || !strings.Contains(code, "money.Read(") {
		t.Error("leaf codec calls not qualified by the declared package name `money`")
	}
	if strings.Contains(code, "ReadInternedString()") {
		t.Error("enum elements are interned without an opt-in")
	}
	if !strings.Contains(code, "zjReadEnum_Level(l)") {
		t.Error("enum elements do not decode through the per-type helper")
	}
	build := exec.Command("go", "vet", "./"+filepath.ToSlash(rel)+"/...")
	build.Dir = moduleRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated package does not vet: %v\n%s", err, out)
	}
}
