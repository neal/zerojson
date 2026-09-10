package generator

import "fmt"

// IsZero is user code too: a pointer receiver may mutate its receiver. Map
// values are not addressable, so classic stdlib invokes it on a field copy.
func zeroMethodCases() []behaviorCase {
	var cases []behaviorCase
	for _, pointer := range []bool{false, true} {
		method := "func (n Number) IsZero() bool { calls++; return omit }"
		if pointer {
			method = "func (n *Number) IsZero() bool { calls++; *n = 9; return omit }"
		}
		for _, shape := range []struct {
			name, typ, value, prefix, suffix string
			addressable                      bool
		}{
			{"direct", "Row", "Row{N: initial}", `{"F":`, `}`, true},
			{"pointer", "*Row", "&Row{N: initial}", `{"F":`, `}`, true},
			{"slice", "[]Row", "[]Row{{N: initial}}", `{"F":[`, `]}`, true},
			{"map", "map[string]Row", `map[string]Row{"k": {N: initial}}`, `{"F":{"k":`, `}}`, false},
			{"nested_map", "map[string]Nested", `map[string]Nested{"k": {R: Row{N: initial}}}`, `{"F":{"k":{"R":`, `}}}`, false},
			{"map_pointer", "map[string]Pointed", `map[string]Pointed{"k": {R: &Row{N: initial}}}`, `{"F":{"k":{"R":`, `}}}`, true},
		} {
			for _, empty := range []string{"", ",omitempty"} {
				cases = append(cases, behaviorCase{
					name: fmt.Sprintf("zero_method_%t_%s_empty%t", pointer, shape.name, empty != ""),
					declarations: "type Number int\nvar calls int\nvar omit bool\n" + method +
						"\ntype Row struct { N Number `json:\"n,omitzero" + empty + "\"` }\n" +
						"type Nested struct { R Row }; type Pointed struct { R *Row }\n" +
						"type Root struct { F " + shape.typ + " }\n" +
						"func makeRoot(initial Number) Root { return Root{F: " + shape.value + "} }\n",
					types: "Root",
					testBody: fmt.Sprintf(`
// Pin both standard-library implementations, but retain classic-v1 semantics
// for zerojson. The independent model describes only this tiny callback.
probe, _ := json.Marshal(string([]byte{255}))
v2 := !bytes.Contains(probe, []byte(`+"`\\ufffd`"+`))
for _, initial := range []Number{0,1} {
 for _, shouldOmit := range []bool{false,true} {
  omit = shouldOmit
  model := func(newStdlib bool) ([]byte,int,Root) {
   invoke := newStdlib || !(%t && initial == 0)
   n, state, count := initial, initial, 0
   if invoke {
    count = 1
    if %t {
     if newStdlib || %t { n = 9 }
     if %t { state = 9 }
    }
   }
   row := "{}"
   if !(invoke && shouldOmit) && !(%t && n == 0) {
    raw, _ := json.Marshal(int(n))
    row = "{\"n\":"+string(raw)+"}"
   }
   return []byte(%q+row+%q),count,makeRoot(state)
  }
  v, std := makeRoot(initial), makeRoot(initial)
  calls = 0
  want, err := json.Marshal(&std)
  if err != nil { t.Fatal(err) }
  wantCalls := calls
  stdJSON,stdCalls,stdState := model(v2)
  if !bytes.Equal(want,stdJSON) || wantCalls != stdCalls || !reflect.DeepEqual(std,stdState) {
   t.Fatalf("stdlib observation differs from model: got=%%s want=%%s calls=%%d/%%d",want,stdJSON,wantCalls,stdCalls)
  }
  want,wantCalls,std = model(false)
  calls = 0
  got, err := v.AppendZJSON(nil)
  if err != nil { t.Fatal(err) }
  if !bytes.Equal(got, want) || calls != wantCalls || !reflect.DeepEqual(v,std) {
   t.Fatalf("IsZero receiver: got=%%s want=%%s calls=%%d/%%d state=%%#v/%%#v", got, want, calls,wantCalls,v,std)
  }
 }
}
`, empty != "", pointer, shape.addressable, shape.addressable, empty != "", shape.prefix, shape.suffix),
				})
			}
		}
	}
	return cases
}
