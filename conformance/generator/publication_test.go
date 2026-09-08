package generator

import "strings"

// A callback can publish an alias during a decode, not just before it. The
// observation is made before the next pooled call invalidates arena storage.
func publicationCases() []behaviorCase {
	var cases []behaviorCase
	for _, mode := range []string{"ordered", "fallback"} {
		flags := []string{"-pool"}
		if mode == "fallback" {
			flags = append(flags, "-no-ordered")
		}
		for _, shape := range []string{"scalar", "custom", "struct", "map", "map_pointer", "slice", "slice_pointer"} {
			typ, first, second := "int", "1", "2"
			switch shape {
			case "custom":
				typ = "Value"
			case "struct":
				typ, first, second = "Item", `{"n":1}`, `{"n":2}`
			case "map", "map_pointer":
				typ, first, second = "map[string]int", `{"a":1}`, `{"b":2}`
			case "slice", "slice_pointer":
				typ, first, second = "[]int", `[1]`, `[2]`
			}
			field := "*" + typ
			if shape == "map" || shape == "slice" {
				field = typ
			}
			cases = append(cases, behaviorCase{
				name: "publication_" + shape + "_" + mode, types: "Root", flags: flags,
				declarations: strings.ReplaceAll(`
type Value int
func(v Value) MarshalJSON()([]byte,error){ return json.Marshal(int(v)) }
func(v *Value) UnmarshalJSON(b []byte)error{ var n int; if err:=json.Unmarshal(b,&n);err!=nil{return err}; *v=Value(n); return nil }
type Item struct { N int ~json:"n"~ }
type Capture int
var observe func()
func(Capture) MarshalJSON()([]byte,error){return []byte("0"),nil}
func(*Capture) UnmarshalJSON([]byte)error{observe();return nil}
type Root struct { P `+field+` ~json:"p"~; C Capture ~json:"c"~ }
`, "~", "`"),
				testBody: `
 for _, pooled:=range []bool{false,true} { for _, borrow:=range []bool{false,true} {
  var dec RootDecoder
  for repeat:=0;repeat<3;repeat++ {
   for _, sequence:=range []string{
    "{\"p\":FIRST,\"c\":0,\"p\":null,\"p\":SECOND}",
    "{\"c\":0,\"p\":FIRST,\"c\":0,\"p\":null,\"p\":SECOND,\"c\":0}",
    "{\"p\":FIRST,\"c\":0,\"p\":null,\"p\":SECOND,\"c\":0,\"p\":null,\"p\":FIRST}",
    "{\"p\":FIRST,\"c\":0,\"p\":null,\"p\":FIRST,\"c\":0}",
    "{\"p\":FIRST,\"c\":0,\"c\":0,\"p\":FIRST}",
   } {
    var got,want Root
    var actual,expected []` + field + `
    observe=func(){expected=append(expected,want.P); if len(expected)==2 { var replacement ` + field + `; if err:=json.Unmarshal([]byte("SECOND"),&replacement);err!=nil{t.Fatal(err)}; want.P=replacement } }
    if err:=json.Unmarshal([]byte(sequence),&want);err!=nil{t.Fatal(err)}
    observe=func(){actual=append(actual,got.P); if len(actual)==2 { var replacement ` + field + `; if err:=json.Unmarshal([]byte("SECOND"),&replacement);err!=nil{t.Fatal(err)}; got.P=replacement } }
    var err error
    if pooled { if borrow {err=dec.DecodeBorrow([]byte(sequence),&got)}else{err=dec.Decode([]byte(sequence),&got)}
    }else if borrow{err=got.UnmarshalZJSONBorrow([]byte(sequence))}else{err=got.UnmarshalZJSON([]byte(sequence))}
    if err!=nil{t.Fatal(err)}
    if !reflect.DeepEqual(got,want)||!reflect.DeepEqual(actual,expected){t.Fatalf("publication pooled=%v borrow=%v input=%s retained=%v want=%v",pooled,borrow,sequence,actual,expected)}
    IDENTITY
   }
  }
 }}
`,
			})
			last := &cases[len(cases)-1]
			identity := ""
			if strings.HasPrefix(field, "*") {
				identity = `for i:=range actual { for j:=range actual { if (actual[i]==actual[j])!=(expected[i]==expected[j]) {t.Fatal("publication changed pointer identity")} } }`
			}
			last.testBody = strings.ReplaceAll(last.testBody, "IDENTITY", identity)
			last.testBody = strings.NewReplacer("FIRST", strings.ReplaceAll(first, `"`, `\"`), "SECOND", strings.ReplaceAll(second, `"`, `\"`)).Replace(last.testBody)
		}
	}
	cases = append(cases, behaviorCase{
		name: "pooled_adopted_slice_lifetime", types: "Root", flags: []string{"-pool"},
		declarations: `
type Hook int
var inject func()
func(Hook) MarshalJSON()([]byte,error){return []byte("0"),nil}
func(*Hook) UnmarshalJSON([]byte)error{inject();return nil}
type Root struct { H Hook; S []int; P *[]int }
`,
		testBody: `
for _,borrow:=range []bool{false,true}{
 var d RootDecoder;var v Root;external:=[]int{7}
 inject=func(){v.S=external;v.P=&external}
 decode:=func(s string)error{if borrow{return d.DecodeBorrow([]byte(s),&v)};return d.Decode([]byte(s),&v)}
 if err:=decode("{\"H\":0,\"S\":[1],\"P\":[2]}");err!=nil{t.Fatal(err)}
 if external[0]!=2 || v.S[0]!=2 {t.Fatal("first-use slice aliases lost")}
 if err:=decode("{\"S\":[3],\"P\":[4]}");err!=nil{t.Fatal(err)}
 if external[0]!=2 {t.Fatal("pooled decode recycled caller-owned slice")}
 if v.S[0]!=3 || (*v.P)[0]!=4 {t.Fatal("pooled replacement decode failed")}
}
`,
	})
	return cases
}
