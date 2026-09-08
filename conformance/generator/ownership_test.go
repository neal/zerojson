package generator

import "strings"

// These cases observe aliases and transitive addressability, not just the
// freshly decoded root value.
func ownershipCases() []behaviorCase {
	var cases []behaviorCase
	for _, mode := range []string{"ordered", "fallback"} {
		flags := []string{"-pool"}
		if mode == "fallback" {
			flags = append(flags, "-no-ordered")
		}
		cases = append(cases, behaviorCase{
			name: "map_header_alias_" + mode, types: "Root", flags: flags,
			declarations: "type Root struct { M map[string]int `json:\"m\"`; P *map[string]int `json:\"p\"` }",
			testBody: `
 for _, populated := range []bool{false,true} {
  for _, borrow := range []bool{false,true} {
   for _, data := range []string{
    "{\"p\":{\"a\":1},\"m\":null,\"p\":{\"b\":2}}",
    "{\"m\":{\"a\":1},\"p\":{},\"m\":null,\"p\":{\"b\":2},\"m\":{\"c\":3}}",
   } {
    var got, want Root
    init := func(v *Root) { if populated { v.M=map[string]int{"keep":7} }; v.P=&v.M }
    init(&got); init(&want)
    var err error
    if borrow { err=got.UnmarshalZJSONBorrow([]byte(data)) } else { err=got.UnmarshalZJSON([]byte(data)) }
    if err!=nil { t.Fatal(err) }; if err=json.Unmarshal([]byte(data),&want); err!=nil { t.Fatal(err) }
    if got.P!=&got.M || !reflect.DeepEqual(got,want) { t.Fatalf("map header alias: got=%+v want=%+v",got,want) }
   }
  }
 }
`,
		})
		cases = append(cases, behaviorCase{
			name: "named_interface_" + mode, types: "Root,List,Index", flags: flags,
			declarations: "type Value interface{}; type Alias = any; type Root struct { P *Value; S []Value; V Value; A *Alias; AS []Alias }; type List []Value; type Index map[string]Value",
			testBody: `
 data:=[]byte("{\"P\":1,\"S\":[1,null,{\"x\":\"y\"}],\"V\":true,\"A\":\"x\",\"AS\":[2]}")
 var got,want Root
 if err:=got.UnmarshalZJSON(data); err!=nil {t.Fatal(err)}
 if err:=json.Unmarshal(data,&want); err!=nil {t.Fatal(err)}
 if !reflect.DeepEqual(got,want) {t.Fatalf("named interface: got=%+v want=%+v",got,want)}
 var decoder RootDecoder
 for i:=0;i<3;i++ {if err:=decoder.Decode(data,&got); err!=nil {t.Fatal(err)}; if !reflect.DeepEqual(got,want) {t.Fatal("pooled named interface mismatch")}}
 var list List; var index Index
 if err:=list.UnmarshalZJSON([]byte("[1,null,{\"a\":2}]")); err!=nil {t.Fatal(err)}
 if err:=index.UnmarshalZJSON([]byte("{\"a\":1}")); err!=nil {t.Fatal(err)}
`,
		})
		cases = append(cases, behaviorCase{
			name: "map_null_detaches_" + mode, types: "Root", flags: flags,
			declarations: "type Root struct { M map[string]int `json:\"m\"`; P *map[string]int `json:\"p\"`; Other map[string]int `json:\"other\"` }",
			testBody: `
 for _, key := range []string{"m", "p"} {
  for _, borrow := range []bool{false,true} {
   fresh := func() (Root,map[string]int) { m:=map[string]int{"keep":7}; return Root{M:m,P:&m,Other:m},m }
   got, retained := fresh(); want, expected := fresh()
   data := []byte("{\""+key+"\":{\"a\":1},\""+key+"\":null,\""+key+"\":{\"b\":2}}")
   var err error
   if borrow { err=got.UnmarshalZJSONBorrow(data) } else { err=got.UnmarshalZJSON(data) }
   if err!=nil { t.Fatal(err) }; if err=json.Unmarshal(data,&want); err!=nil { t.Fatal(err) }
   if !reflect.DeepEqual(got,want) || !reflect.DeepEqual(retained,expected) { t.Fatalf("map ownership key=%s borrow=%v: got=%+v retained=%v want=%+v retained=%v",key,borrow,got,retained,want,expected) }
  }
 }
`,
		})
	}
	for _, family := range []string{"JSON", "Text"} {
		for _, receiver := range []string{"", "*"} {
			methods := "func (" + receiver + "Leaf) MarshalJSON() ([]byte,error) { return []byte(`\"custom\"`),nil }; func (*Leaf) UnmarshalJSON([]byte) error { return nil }"
			if family == "Text" {
				methods = "func (" + receiver + "Leaf) MarshalText() ([]byte,error) { return []byte(`custom`),nil }; func (*Leaf) UnmarshalText([]byte) error { return nil }"
			}
			cases = append(cases, behaviorCase{
				name: "nested_map_" + family + map[string]string{"": "value", "*": "pointer"}[receiver], types: "Root,Index",
				declarations: strings.ReplaceAll(`type Leaf int
`+methods+`
type Item struct { Value Leaf ~json:"value"~; Pointer *Leaf ~json:"pointer"~ }
type Nested struct { Item Item ~json:"item"~; Pointer *Item ~json:"pointer"~; Slice []Item ~json:"slice"~ }
type Root struct { Items map[string]Nested ~json:"items"~ }
type Index map[string]Nested
`, "~", "`"),
				testBody: `
 leaf:=Leaf(5); item:=Item{Value:5,Pointer:&leaf}; nested:=Nested{Item:item,Pointer:&item,Slice:[]Item{item}}
 v:=Root{Items:map[string]Nested{"x":nested}}; idx:=Index(v.Items)
 got,err:=v.AppendZJSON(nil); if err!=nil { t.Fatal(err) }; want,err:=json.Marshal(&v); if err!=nil { t.Fatal(err) }
 if !bytes.Equal(got,want) { t.Fatalf("nested map encode: got=%s want=%s",got,want) }
 got,err=idx.AppendZJSON(nil); if err!=nil { t.Fatal(err) }; want,err=json.Marshal(&idx); if err!=nil { t.Fatal(err) }
 if !bytes.Equal(got,want) { t.Fatalf("root map encode: got=%s want=%s",got,want) }
`,
			})
		}
	}
	return cases
}
