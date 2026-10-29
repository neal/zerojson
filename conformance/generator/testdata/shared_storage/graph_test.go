package p

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGraphReplay(t *testing.T) {
	values := []string{`null`, `{}`, `{"x":null}`, `{"x":{"N":1,"P":2,"S":[null,3]}}`, `{"x":{"S":[]}}`, `{"x":{"S":[1,2,3,4]}}`}
	for _, a := range values {
		for _, b := range values {
			// A whitespace-padded empty Child makes the ordered path bail and
			// restart the object after A, B, Items, and P.
			for _, tail := range []string{`,"C":0}`, `, "C":0}`, `,"A":{"x":{"N":7}}}`, `,"\u0043":0}`, `{ },"C":0}`} {
				for state := 0; state < 3; state++ {
					for mode := 0; mode < 4; mode++ {
						makeValue := func() (Graph, []int, *Node) {
							buf := []int{9, 8, 7}
							n := &Node{N: 5, P: &buf[0], S: buf[:1]}
							m := map[string]Node{"x": *n}
							switch state {
							case 0:
								return Graph{}, buf, n
							case 1:
								return Graph{A: m, B: m, Items: []Node{*n}, P: &buf[0], Child: *n}, buf, n
							default:
								return Graph{A: m, B: map[string]Node{"x": *n}, Items: []Node{*n}, Child: *n}, buf, n
							}
						}
						got, ga, gn := makeValue()
						want, wa, wn := makeValue()
						oldp := got.P
						data := []byte(`{"A":` + a + `,"B":` + b + `,"Items":[{"P":4,"S":[null,6]}],"P":3,"Child":`)
						if tail[0] == '{' {
							data = append(data, tail...)
						} else {
							data = append(append(data, `{"S":[null,4]}`...), tail...)
						}
						original := string(data)
						var e error
						var d GraphDecoder
						switch mode {
						case 0:
							e = got.UnmarshalZJSON(data)
						case 1:
							e = got.UnmarshalZJSONBorrow(data)
						case 2:
							want = Graph{}
							e = d.Decode(data, &got)
						case 3:
							want = Graph{}
							e = d.DecodeBorrow(data, &got)
						}
						if string(data) != original {
							t.Fatal("decoder mutated input")
						}
						se := json.Unmarshal([]byte(original), &want)
						if e != nil || se != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(ga, wa) || !reflect.DeepEqual(gn, wn) {
							t.Fatalf("state=%d mode=%d %s got=%+v/%v/%+v want=%+v/%v/%+v err=%v/%v", state, mode, data, got, ga, gn, want, wa, wn, e, se)
						}
						if mode < 2 && oldp != nil && oldp != got.P {
							t.Fatal("pointer replaced")
						}
					}
				}
			}
		}
	}
}
