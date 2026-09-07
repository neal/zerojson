package generator

import (
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Cross method sets with underlying kinds: choosing a fast codec must not
// hide the Go container kind that determines what a JSON null does.
func TestNamedContainerNullPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator and a method-set matrix")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjnullpolicy")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var src, tests strings.Builder
	src.WriteString("package p\nimport (\"encoding/json\"; \"github.com/neal/zerojson\")\nvar calls int\n")
	tests.WriteString(`package p
import (
 "encoding/json"
 "reflect"
 "testing"
 "github.com/neal/zerojson"
 "github.com/neal/zerojson/conformance/internal/oracle"
)
`)
	var roots []string
	for _, shape := range []struct{ name, base, initial, replacement, wire string }{
		{"Slice", "[]int", "[]int{7}", "[]int{9}", "[8]"},
		{"Map", "map[string]int", "map[string]int{\"old\":7}", "map[string]int{\"null\":9}", `{"new":8}`},
		{"Scalar", "int", "7", "9", "8"},
		{"Array", "[2]int", "[2]int{7,7}", "[2]int{9,9}", "[8,8]"},
		{"Struct", "struct{ N int }", "struct{ N int }{7}", "struct{ N int }{9}", `{"N":8}`},
	} {
		for _, mode := range []string{"Appender", "Text", "AppenderJSON", "JSON"} {
			n := mode + shape.name
			root := n + "Holder"
			roots = append(roots, root)
			fmt.Fprintf(&src, "type %s %s\ntype %s struct { V %s `json:\"v\"`; P *%s `json:\"p\"` }\n", n, shape.base, root, n, n)
			if strings.HasPrefix(mode, "Appender") {
				fmt.Fprintf(&src, "func(v %s) AppendZJSON(dst []byte)([]byte,error) { b,err:=json.Marshal((%s)(v)); return append(dst,b...),err }\n", n, shape.base)
				fmt.Fprintf(&src, "func(v *%s) ReadZJSON(l *zerojson.Lexer) { b:=l.RawValue(); if l.Err()==nil { l.AddError(json.Unmarshal(b,(*%s)(v))) } }\n", n, shape.base)
			}
			if strings.HasSuffix(mode, "JSON") {
				// AppenderJSON intentionally has no MarshalJSON: null delivery
				// must depend only on UnmarshalJSON, not on a pair of methods.
				if mode == "JSON" {
					fmt.Fprintf(&src, "func(v %s) MarshalJSON()([]byte,error) { return json.Marshal((%s)(v)) }\n", n, shape.base)
				}
				fmt.Fprintf(&src, "func(v *%s) UnmarshalJSON(b []byte)error { if string(b)==\"null\" { calls++; *v=%s(%s); return nil }; return json.Unmarshal(b,(*%s)(v)) }\n", n, n, shape.replacement, shape.base)
			}
			if mode == "Text" {
				fmt.Fprintf(&src, "func(v %s) MarshalText()([]byte,error) { return json.Marshal((%s)(v)) }\n", n, shape.base)
				fmt.Fprintf(&src, "func(v *%s) UnmarshalText(b []byte)error { calls++; return json.Unmarshal(b,(*%s)(v)) }\n", n, shape.base)
			}
			wire := shape.wire
			if mode == "Text" {
				wire = fmt.Sprintf("%q", wire)
			}
			// Exercise null both before and after a value: map clearing must
			// also prevent old entries from surviving a subsequent merge.
			docs := []string{`{"v":null}`, `{"p":null}`, `{"v":null,"p":null}`,
				`{"v":` + wire + `,"v":null}`, `{"p":` + wire + `,"p":null}`,
				`{"v":null,"v":` + wire + `}`, `{"p":null,"p":` + wire + `}`}
			fmt.Fprintf(&tests, "func Test%s(t *testing.T) {\n", n)
			fmt.Fprintf(&tests, "fresh:=func()%s { p:=%s(%s); return %s{V:%s(%s),P:&p} }\n", root, n, shape.initial, root, n, shape.initial)
			fmt.Fprintf(&tests, "var pool %sDecoder\n", root)
			fmt.Fprintf(&tests, "for _, mode:=range []string{\"copy\",\"borrow\",\"fallback\",\"pool\",\"pool-borrow\"} { for i,doc:=range %#v { t.Run(mode+doc,func(t *testing.T){\n", docs)
			fmt.Fprintf(&tests, "zj,std:=fresh(),fresh(); if mode==\"pool\" || mode==\"pool-borrow\" { std=%s{} }; calls=0\n", root)
			tests.WriteString("if err:=json.Unmarshal([]byte(doc),&std);err!=nil { t.Fatal(err) }; wantCalls:=calls; calls=0\n")
			textContainer := mode == "Text" && (shape.name == "Slice" || shape.name == "Map")
			if textContainer {
				// This is a pinned v1-on-v2 drift, not a blanket oracle skip.
				// Only VALUE-field nulls differ. A following map update also
				// exposes that difference by merging the entries v2 retained.
				tests.WriteString(`if i==0 || i==2 || i==3 {
 if oracle.StdlibJSONV2 {
   if i==3 || (mode!="pool" && mode!="pool-borrow") { if std.V==nil { t.Fatal("v2 no longer preserves TextUnmarshaler container on null; revisit drift") } }
 } else if std.V!=nil { t.Fatal("classic stdlib did not clear container") }
 std.V=nil
}
`)
				if shape.name == "Map" {
					tests.WriteString(`if oracle.StdlibJSONV2 && i==5 && mode!="pool" && mode!="pool-borrow" {
 if std.V["old"]!=7 || std.V["new"]!=8 { t.Fatal("v2 no longer merges after TextUnmarshaler null; revisit drift") }
 delete(std.V,"old")
}
`)
				}
			} else {
				tests.WriteString("_ = i\n")
			}
			tests.WriteString("var err error\nswitch mode { case \"copy\": err=zj.UnmarshalZJSON([]byte(doc)); case \"borrow\": err=zj.UnmarshalZJSONBorrow([]byte(doc)); case \"pool\": err=pool.Decode([]byte(doc),&zj); case \"pool-borrow\": err=pool.DecodeBorrow([]byte(doc),&zj); case \"fallback\": l:=zerojson.NewLexer([]byte(doc),false); ")
			fmt.Fprintf(&tests, "zjDecodeFallback%s(&l,&zj,&zjArena%s{}); l.Finish(); err=l.Err() }\n", root, root)
			tests.WriteString("if err!=nil { t.Fatal(err) }; if !reflect.DeepEqual(zj,std) { t.Errorf(\"zj=%#v std=%#v\",zj,std) }; if calls!=wantCalls { t.Errorf(\"callbacks=%d want %d\",calls,wantCalls) }\n}) } } }\n")
		}
	}
	for name, data := range map[string]string{"types.go": src.String(), "null_test.go": tests.String()} {
		formatted, err := format.Source([]byte(data))
		if err != nil {
			t.Fatalf("format %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), formatted, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(gen, "-dir", dir, "-types", strings.Join(roots, ","), "-pool", "-out", "zerojson_gen.go")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate null policy matrix: %v\n%s", err, out)
	}
	run := exec.Command("go", "test", "./"+filepath.Base(dir), "-count=1")
	run.Dir = moduleRoot
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("null policy matrix: %v\n%s", err, out)
	}
}
