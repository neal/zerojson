package generator

import (
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
	cmd := exec.Command(gen, "-dir", dir, "-types", "Ints,Bytes,Rows,OuterInts,OuterBytes,OuterRows", "-pool")
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

const sharedStorageFixture = `
func Test$NAME(t *testing.T) {
 values := []string{$VALUES}
 for _, x := range values { for _, y := range values {
  for _, tail := range []string{",\"C\":0}",", \"C\":0}",",\"A\":[null]}",",\"\\u0043\":0}"} {
   for state:=0;state<4;state++ { for _, mode:=range []string{"copy","borrow","poolcopy","poolborrow"} {
    makeValue:=func()($NAME,[]$ELEM){a:=[]$ELEM{$SEED};switch state {
     case 0:return $NAME{},a
     case 1:return $NAME{A:a[:1:1],B:a[:1:1]},a
     case 2:return $NAME{A:a[:1],B:a[1:2]},a
     default:return $NAME{A:a[:1],B:[]$ELEM{$SEED}},a
    }}
    got,a:=makeValue();want,b:=makeValue()
    doc:=[]byte("{\"A\":"+x+",\"B\":"+y+tail)
    var err error
    var d $NAMEDecoder
    switch mode {
     case "copy":err=got.UnmarshalZJSON(doc)
     case "borrow":err=got.UnmarshalZJSONBorrow(doc)
     case "poolcopy":want=$NAME{};err=d.Decode(doc,&got)
     case "poolborrow":want=$NAME{};err=d.DecodeBorrow(doc,&got)
    }
    stdErr:=json.Unmarshal(doc,&want)
    if err!=nil||stdErr!=nil||!reflect.DeepEqual(got,want)||!reflect.DeepEqual(a,b) {
     t.Fatalf("shared storage state=%d mode=%s %s got=%+v/%v want=%+v/%v errors=%v/%v",state,mode,doc,got,a,want,b,err,stdErr)
    }
    // The same guards must recurse through a value-typed child struct.
    gv,ga:=makeValue();wv,wa:=makeValue()
    g,w:=Outer$NAME{Child:gv},Outer$NAME{Child:wv}
    nested:=append(append([]byte("{\"Child\":"),doc...),'}')
    if err:=g.UnmarshalZJSON(nested);err!=nil {t.Fatal(err)}
    if err:=json.Unmarshal(nested,&w);err!=nil {t.Fatal(err)}
    if !reflect.DeepEqual(g,w)||!reflect.DeepEqual(ga,wa) {t.Fatalf("shared storage nested %s",nested)}
   }}
  }
 }}
 // A conservative reuse guard must not disable the fresh-target fast path.
 var fresh $NAME
 l:=zj.NewLexer([]byte("{\"A\":[],\"B\":[],\"C\":0}"),false)
 if !zjDecodeOrdered_$NAME(&l,&fresh,&zjArena$NAME{})||l.Err()!=nil {t.Fatal("fresh ordered path disabled",l.Err())}
}
`
