package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestScalarReuseKinds checks the specialization's complete kind whitelist.
// Composite controls exercise the neighboring delegated reuse path. The
// oracle uses methodless twins even when generating -compat wrappers.
func TestScalarReuseKinds(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles scalar reuse fixtures")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, root)
	var types, tests strings.Builder
	types.WriteString("package scalar\nimport \"encoding/json\"\ntype Row struct { X int }\n")
	tests.WriteString("package scalar\nimport (\"encoding/json\"; \"reflect\"; \"testing\")\n")
	var names []string
	for i, c := range []struct{ typ, values, input string }{
		{"string", `"old", "hidden", "tail"`, `["new","\u0000","\\"]`},
		{"bool", "true, false, true", `[true,false]`},
		{"int8", "7, 8, 9", `[-128,127]`},
		{"int64", "7, 8, 9", `[-9223372036854775808,9223372036854775807]`},
		{"uint64", "7, 8, 9", `[1,18446744073709551615]`},
		{"float32", "7, 8, 9", `[1.401298464324817e-45,3.4028234663852886e38]`},
		{"float64", "7, 8, 9", `[5e-324,1.7976931348623157e308]`},
		{"json.Number", `"7", "8", "9"`, `["2.5",1e1000]`},
		{"Row", "{X:7}, {X:8}, {X:9}", `[{"X":1},{"X":2},{"X":3}]`},
		{"*Row", "{X:7}, {X:8}, {X:9}", `[{"X":1},{"X":2},{"X":3}]`},
		{"any", `"old", "hidden", "tail"`, `["new",2,true]`},
	} {
		n := fmt.Sprintf("S%d", i)
		names = append(names, n, n+"Root")
		fmt.Fprintf(&types, "type %s struct { F []%s; P *[]%s }; type %sRoot []%s\n", n, c.typ, c.typ, n, c.typ)
		docs := []string{`[null,null,null]`, c.input, `[null]`, `[]`, c.input, `null`, `[null,null,null,null]`, c.input}
		var quoted []string
		for _, d := range docs {
			quoted = append(quoted, strconv.Quote(d))
		}
		tests.WriteString(strings.NewReplacer("$N", n, "$T", c.typ, "$VALUES", c.values, "$WARM", strconv.Quote(`{"F":[null],"P":[null],"F":`+c.input+`,"P":`+c.input+`}`), "$DOCS", strings.Join(quoted, ",")).Replace(scalarReuseFixture))
	}
	for _, mode := range []string{"ordered", "fallback", "compat"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := os.MkdirTemp(root, "zjscalarreuse")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			for name, source := range map[string]string{"types.go": types.String(), "reuse_test.go": tests.String()} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-dir", dir, "-types", strings.Join(names, ","), "-pool"}
			if mode == "fallback" {
				args = append(args, "-no-ordered")
			}
			if mode == "compat" {
				args = append(args, "-compat")
			}
			cmd := exec.Command(gen, args...)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate: %v\n%s", err, out)
			}
			args = []string{"test", ".", "-count=1", "-timeout=2m"}
			if os.Getenv("ZEROJSON_CHALLENGE_RACE") != "" {
				args = append(args, "-race", "-gcflags=all=-d=checkptr=2")
			}
			cmd = exec.Command("go", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("scalar reuse: %v\n%s", err, out)
			}
		})
	}
}

const scalarReuseFixture = `
func Test$N(t *testing.T) {
 for _, mode := range []string{"copy", "borrow", "poolcopy", "poolborrow"} {
  t.Run(mode, func(t *testing.T) {
   pooled := mode == "poolcopy" || mode == "poolborrow"
   type oracle $N
   a, b := []$T{$VALUES}, []$T{$VALUES}
   pa, pb := []$T{$VALUES}, []$T{$VALUES}
   p, q := pa[:1], pb[:1]
   got, want := $N{F:a[:1], P:&p}, oracle{F:b[:1], P:&q}
   var d $NDecoder
   for _, input := range []string{$DOCS} {
    if pooled {
     // Grow through a duplicate header: the arena cell may still have
     // length one even though the shared backing array contains more.
     var err error
     if mode == "poolcopy" { err = d.Decode([]byte($WARM), &got) } else { err = d.DecodeBorrow([]byte($WARM), &got) }
     if err != nil { t.Fatal(err) }
    }
    // Duplicate names test reuse within one document as well as across calls.
    data := []byte("{\"F\":"+input+",\"P\":"+input+",\"F\":[null,null,null],\"P\":[null,null,null]}")
    original := string(data)
    if pooled { want = oracle{} }
    var err error
    switch mode {
    case "copy": err = got.UnmarshalZJSON(data)
    case "borrow": err = got.UnmarshalZJSONBorrow(data)
    case "poolcopy": err = d.Decode(data, &got)
    case "poolborrow": err = d.DecodeBorrow(data, &got)
    }
    if err != nil { t.Fatal(err) }
    if string(data) != original { t.Fatal("decoder changed input") }
    if err := json.Unmarshal([]byte(original), &want); err != nil { t.Fatal(err) }
    if !reflect.DeepEqual(oracle(got), want) || !reflect.DeepEqual(a,b) || !reflect.DeepEqual(pa,pb) {
     t.Fatalf("scalar field reuse %s: got=%#v want=%#v backing=%v/%v pointer backing=%v/%v", input, got, want, a,b,pa,pb)
    }
    if !pooled && got.P != nil && got.P != &p && want.P == &q { t.Fatal("existing pointer replaced") }
   }
   ra, rb := []$T{$VALUES}, []$T{$VALUES}
   gr, wr := $NRoot(ra[:1]), rb[:1]
   var rd $NRootDecoder
   for _, input := range []string{$DOCS} {
    if pooled { wr = nil }
    data := []byte(input)
    original := string(data)
    var err error
    switch mode {
    case "copy": err = gr.UnmarshalZJSON(data)
    case "borrow": err = gr.UnmarshalZJSONBorrow(data)
    case "poolcopy": err = rd.Decode(data, &gr)
    case "poolborrow": err = rd.DecodeBorrow(data, &gr)
    }
    if err != nil { t.Fatal(err) }
    if string(data) != original { t.Fatal("decoder changed input") }
    if err := json.Unmarshal([]byte(original), &wr); err != nil { t.Fatal(err) }
    if !reflect.DeepEqual([]$T(gr),wr) || !reflect.DeepEqual(ra,rb) { t.Fatalf("scalar root reuse %s: got=%v want=%v backing=%v/%v", input, gr,wr,ra,rb) }
    if input == "[]" && cap(gr) != 0 { t.Fatalf("empty array retained capacity: %d", cap(gr)) }
    if pooled {
     var zero $T
     for _, x := range gr[len(gr):cap(gr)] {
      if !reflect.DeepEqual(x,zero) { t.Fatalf("pooled root hidden capacity %s: %v", input,x) }
     }
    }
   }
  })
 }
}
`
