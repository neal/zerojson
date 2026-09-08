package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An encoder that emits valid but wrong JSON must die through a value
// assertion, not a compile failure, panic, or syntax check.
func TestGeneratedEncodingOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and mutates a generated fixture")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, root)
	dir, err := os.MkdirTemp(root, "zjencodeoracle")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", "package p\ntype Root struct { N int `json:\"n\"` }; type List []int; type Index map[string]int\n")
	cmd := exec.Command(gen, "-dir", dir, "-types", "Root,List,Index", "-tests", "codec_fuzz_test.go")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, out)
	}
	write("oracle_test.go", `package p
import ("encoding/json"; "testing")
func TestNumberOracle(t *testing.T) {
 for _,pair:=range [][2]string{{"1","1.00"},{"0","-0.0e99999999999999999999"},{"1e100000000000000000000","10e99999999999999999999"}} {
  if !zjJSONValueEqual(json.Number(pair[0]),json.Number(pair[1])) { t.Fatalf("equivalent numbers differ: %v",pair) }
 }
 for _,pair:=range [][2]string{{"9007199254740992","9007199254740993"},{"1","1.00000000000000000000001"},{"1e999999","1e999998"}} {
  if zjJSONValueEqual(json.Number(pair[0]),json.Number(pair[1])) { t.Fatalf("distinct numbers compare equal: %v",pair) }
 }
 zjAssertJSONEqual(t,[]byte("{\"x\":1,\"y\":\"a\"}"),[]byte("{\"y\":\"\\u0061\",\"x\":1e0}"))
}
func TestWrongValueKindsAreClassified(t *testing.T) {
 for _,doc:=range []string{"{\"n\":\"text\"}","{\"n\":true}","{\"n\":[]}","{\"n\":{}}"} {
  var v Root; err:=json.Unmarshal([]byte(doc),&v)
  if err==nil || !zjStdlibValueError(err) { t.Fatalf("missed wrong value kind: %s (%v)",doc,err) }
 }
}
`)
	data, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	run := func(pattern string) ([]byte, error) {
		cmd := exec.Command("go", "test", ".", "-run", pattern, "-count=1", "-timeout=60s")
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run("TestNumberOracle|TestWrongValueKindsAreClassified|^Fuzz.*ZJSON$"); err != nil {
		t.Fatalf("baseline: %v %s", err, out)
	}
	re := regexp.MustCompile(`zerojson.AppendInt64\(dst, int64\([^\n]+\)\)`)
	if len(re.FindAllString(original, -1)) != 3 {
		t.Fatal("encoder mutation anchors changed")
	}
	mutated := re.ReplaceAllString(original, "zerojson.AppendInt64(dst, 0)")
	mutated = strings.Replace(mutated, "dst = zerojson.AppendString(dst, string(mk))", "_ = mv; dst = zerojson.AppendString(dst, string(mk))", 1)
	write("zerojson_gen.go", mutated)
	for _, name := range []string{"Root", "List", "Index"} {
		t.Run(name, func(t *testing.T) {
			out, err := run("^Fuzz" + name + "ZJSON$")
			if err == nil || !strings.Contains(string(out), "encode value divergence") {
				t.Fatalf("wrong-value mutant survived or died for wrong reason: %v %s", err, out)
			}
		})
	}
	start := strings.Index(original, "func (v *Root) AppendZJSON(")
	if start < 0 {
		t.Fatal("missing Root encoder")
	}
	end := strings.Index(original[start:], "\n}")
	if end < 0 {
		t.Fatal("missing encoder end")
	}
	end += start + 2
	write("zerojson_gen.go", original[:start]+"func (v *Root) AppendZJSON(dst []byte) ([]byte,error) { return append(dst, `{}`...),nil }"+original[end:])
	if out, err := run("^FuzzRootZJSON$"); err == nil || !strings.Contains(string(out), "encode value divergence") {
		t.Fatalf("dropped-field mutant survived or died for wrong reason: %v %s", err, out)
	}
}
