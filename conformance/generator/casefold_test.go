package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCaseFoldOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and mutates a fuzz fixture")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, root)
	dir, err := os.MkdirTemp(root, "zjcasefold")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", strings.ReplaceAll(`package p
type Child struct { X int ~json:"x"~ }
type Root struct { N int ~json:"n"~; Child Child ~json:"child"~; Items []Child ~json:"items"~; Index map[string]Child ~json:"index"~; Dict map[string]int ~json:"dict"~ }
type List []Child
type Index map[string]Child
`, "~", "`"))
	cmd := exec.Command(gen, "-dir", dir, "-types", "Root,List,Index", "-tests", "codec_fuzz_test.go")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	for name, data := range map[string]string{
		"mixed":             `{"n":1,"N":2,"n":3,"N":4}`,
		"folded_type_error": `{"n":1,"N":"bad"}`,
		"nested":            `{"child":{"X":2},"items":[{"X":3,"x":4}],"index":{"K":{"X":1,"x":2}},"dict":{"N":1,"n":2}}`,
		"escaped":           `{"\u004e":1,"n":2,"child":{"\u0058":3,"x":4}}`,
		"unrelated_error":   `{"unknown":{"N":1},"child":{"x":"bad"}}`,
	} {
		write("testdata/fuzz/FuzzRootZJSON/"+name, "go test fuzz v1\n[]byte("+strconv.Quote(data)+")\n")
	}
	write("testdata/fuzz/FuzzListZJSON/nested", "go test fuzz v1\n[]byte(\"[{\\\"X\\\":1,\\\"x\\\":2}]\")\n")
	write("testdata/fuzz/FuzzIndexZJSON/nested", "go test fuzz v1\n[]byte(\"{\\\"X\\\":{\\\"X\\\":1,\\\"x\\\":2}}\")\n")
	write("matching_test.go", strings.ReplaceAll(`package p
import("encoding/json"; "reflect"; "testing")
func TestMatching(t *testing.T) {
 input:=[]byte(~{"n":1,"N":2,"n":3,"child":{"X":1,"x":2},"unknown":{"N":7},"dict":{"N":8},"items":[{"X":1,"x":4}],"index":{"X":{"X":1,"x":5}}}~)
 var got, want Root
 if err:=got.UnmarshalZJSON(input);err!=nil {t.Fatal(err)}
 if err:=json.Unmarshal(input,&want);err!=nil {t.Fatal(err)}
 if !reflect.DeepEqual(got,want) {t.Fatalf("matching got=%+v want=%+v",got,want)}
 var v Root
 err:=json.Unmarshal([]byte(~{"unknown":{"N":1},"child":{"x":"bad"}}~),&v)
 if err==nil || !zjStdlibValueError(err) {t.Fatalf("unrelated value error suppressed: %v",err)}
}
`, "~", "`"))
	run := func(pattern string) ([]byte, error) {
		cmd := exec.Command("go", "test", ".", "-count=1", "-run", pattern)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run("."); err != nil {
		t.Fatalf("case-fold baseline: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "zerojson_gen.go")
	code, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "l.ReadInt()") {
		t.Fatal("missing mutation anchor")
	}
	write("zerojson_gen.go", strings.ReplaceAll(string(code), "l.ReadInt()", "zjWrongInt(l)"))
	write("mutant.go", `package p
import "github.com/neal/zerojson"
func zjWrongInt(l *zerojson.Lexer) int { if l.IsDelim('"') { l.ReadString(); return 0 }; return l.ReadInt() }
`)
	out, err := run("^FuzzRootZJSON$/^unrelated_error$")
	if err == nil || !strings.Contains(string(out), "stdlib rejected value") {
		t.Fatalf("case-fold oracle missed wrong-kind mutant: %v\n%s", err, out)
	}
}
