package generator

import "fmt"

// Exhaust the four JSON/Text method-presence bits. In particular, a byte-slice
// optimization must not bypass the same supported-method policy as a field.
func byteMethodSetCases() []behaviorCase {
	var cases []behaviorCase
	for mask := 0; mask < 16; mask++ {
		methods := "type Byte uint8\n"
		if mask&1 != 0 {
			methods += "func(Byte) MarshalJSON()([]byte,error){return []byte(`3`),nil}\n"
		}
		if mask&2 != 0 {
			methods += "func(v *Byte) UnmarshalJSON([]byte)error{*v=9;return nil}\n"
		}
		if mask&4 != 0 {
			methods += "func(Byte) MarshalText()([]byte,error){return []byte(`3`),nil}\n"
		}
		if mask&8 != 0 {
			methods += "func(v *Byte) UnmarshalText([]byte)error{*v=9;return nil}\n"
		}
		refusal := ""
		if (mask&1 != 0) != (mask&2 != 0) {
			refusal = "has only one of MarshalJSON/UnmarshalJSON"
		} else if mask&3 == 0 && (mask&4 != 0) != (mask&8 != 0) {
			refusal = "has only one of MarshalText/UnmarshalText"
		}
		for _, shape := range []string{"direct", "slice", "named_slice"} {
			typ, value, inputs := "Byte", "Byte(1)", []string{`{"B":1}`, `{"B":"3"}`}
			if shape != "direct" {
				typ, value, inputs = "[]Byte", "[]Byte{1}", []string{`{"B":[1]}`, `{"B":["3"]}`, `{"B":"AQ=="}`}
			}
			decl := methods
			if shape == "named_slice" {
				decl += "type Bytes []Byte\n"
				typ = "Bytes"
				value = "Bytes{1}"
			}
			body := `type oracle Root
v:=Root{B:` + value + `}; want,err:=json.Marshal(&v);if err!=nil{t.Fatal(err)};got,err:=v.AppendZJSON(nil);if err!=nil{t.Fatal(err)};if !bytes.Equal(got,want){t.Fatalf("encode got=%s want=%s",got,want)}
`
			for _, input := range inputs {
				body += fmt.Sprintf(`{var got Root;var want oracle; data:=[]byte(%q);ge:=got.UnmarshalZJSON(data);we:=json.Unmarshal(data,&want);if (ge==nil)!=(we==nil){t.Fatalf("acceptance %%s: got=%%v want=%%v",data,ge,we)};if ge==nil&&!reflect.DeepEqual(oracle(got),want){t.Fatalf("value %%s: got=%%v want=%%v",data,got,want)}}
`, input)
			}
			cases = append(cases, behaviorCase{name: fmt.Sprintf("byte_methods_%02d_%s", mask, shape), types: "Root", declarations: decl + "type Root struct { B " + typ + " }", wantGenError: refusal, testBody: body})
		}
	}
	return cases
}
