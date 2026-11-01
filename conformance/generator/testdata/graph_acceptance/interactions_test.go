package acceptance

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type interactionWorld struct {
	v        Interaction
	retained []Interaction
	seed     *int
}

func newInteractionWorld(state int) *interactionWorld {
	w := &interactionWorld{seed: new(int)}
	*w.seed = 9
	if state == 0 {
		return w
	}
	w.v.Cells = Cells{A: w.seed, B: w.seed, Nested: &Child{P: w.seed, S: []int{9, 8, 7}}}
	w.v.Maps = MapCases{Small: map[string]int{"keep": 9}, Medium: map[string]Child{"keep": {P: w.seed}}, Large: map[string]Large{"keep": {A: "old"}}, Dynamic: map[string]any{"keep": float64(9)}}
	if state == 2 {
		other := new(int)
		*other = 9
		w.v.Cells.B = other
	}
	return w
}

func interactionPointers(w *interactionWorld) []*int {
	p := []*int{nil, w.seed}
	for _, v := range append(append([]Interaction{}, w.retained...), w.v) {
		p = append(p, v.Cells.A, v.Cells.B)
		if v.Cells.Nested != nil {
			p = append(p, v.Cells.Nested.P)
		} else {
			p = append(p, nil)
		}
	}
	return p
}

func compareInteractions(t *testing.T, got, want *interactionWorld) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interaction values differ: got=%+v want=%+v", got.v, want.v)
	}
	a, b := interactionPointers(got), interactionPointers(want)
	for i := range a {
		for j := range a {
			if (a[i] == a[j]) != (b[i] == b[j]) {
				t.Fatalf("interaction pointer identity differs at %d/%d", i, j)
			}
		}
	}
}

func mapDocument(n int, duplicates bool, value func(int) string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		key := i
		if duplicates {
			key = i % 3
		}
		fmt.Fprintf(&b, "%q:%s", fmt.Sprintf("k%d", key), value(i))
	}
	b.WriteByte('}')
	return b.String()
}

// Fixed, finite interaction domain. Sizes surround the 8-entry first buffer,
// 40-entry scalar spill and 26-entry Child spill (48-byte entries). Large
// entries bypass buffering. Duplicate-key cases retain the same wire counts.
func TestDecodeInteractions(t *testing.T) {
	count := 0
	for _, n := range []int{0, 1, 7, 8, 9, 25, 26, 27, 39, 40, 41} {
		for _, duplicates := range []bool{false, true} {
			small := mapDocument(n, duplicates, func(i int) string { return fmt.Sprint(i) })
			medium := mapDocument(n, duplicates, func(i int) string { return fmt.Sprintf(`{"S":[%d],"P":%d}`, i, i) })
			large := mapDocument(n, duplicates, func(i int) string { return fmt.Sprintf(`{"A":"value-%d"}`, i) })
			maps := fmt.Sprintf(`{"Small":%s,"Medium":%s,"Large":%s,"Dynamic":%s}`, small, medium, large, small)
			for handoff, cells := range []string{
				`{"A":1,"B":2,"Nested":{"S":[1],"P":3}}`,
				`{"A":1, "B":2,"Nested":{"P":3,"S":[1]}}`,
				`{"B":2,"A":1,"Nested":{"P":3,"S":[1]}}`,
				`{"A":1,"A":null,"A":4,"Nested":{"P":3,"P":null,"P":5}}`,
				`{"\u0041":1,"b":2,"Nested":{"P":3,"S":[1]}}`,
			} {
				for state := 0; state < 3; state++ {
					for mode := 0; mode < 3; mode++ {
						name := fmt.Sprintf("size%d/duplicates%t/handoff%d/state%d/mode%d", n, duplicates, handoff, state, mode)
						t.Run(name, func(t *testing.T) {
							got, want := newInteractionWorld(state), newInteractionWorld(state)
							sequence := []string{
								fmt.Sprintf(`{"Lead":1,"Cells":%s,"Maps":%s,"Tail":2}`, cells, maps),
								`{"Cells":{"A":null,"Nested":null},"Maps":{"Small":null,"Medium":null,"Large":null,"Dynamic":null}}`,
								fmt.Sprintf(`{"Tail":3,"Maps":%s,"Cells":%s,"Lead":4}`, maps, cells),
							}
							for step, doc := range sequence {
								got.retained = append(got.retained, got.v)
								want.retained = append(want.retained, want.v)
								input := []byte(doc)
								if !json.Valid(input) {
									t.Fatal("invalid test document")
								}
								var err error
								switch mode {
								case 0:
									err = got.v.UnmarshalZJSON(input)
								case 1:
									err = got.v.UnmarshalZJSONBorrow(input)
								case 2:
									err = got.v.UnmarshalZJSONTrusted(input)
								}
								if err != nil {
									t.Fatalf("step %d: %v", step, err)
								}
								if err = json.Unmarshal([]byte(doc), &want.v); err != nil {
									t.Fatal(err)
								}
								if string(input) != doc {
									t.Fatal("input modified")
								}
								compareInteractions(t, got, want)
								if mode != 1 {
									for i := range input {
										input[i] = 0xff
									}
									compareInteractions(t, got, want)
								}
							}
							// Mutation probes distinguish retained shared maps and nested
							// pointers from equal-valued copies.
							for _, w := range []*interactionWorld{got, want} {
								*w.seed = 31
								if w.v.Cells.Nested != nil {
									w.v.Cells.Nested.S = append(w.v.Cells.Nested.S, 77)
								}
								w.v.Maps.Small["probe"] = 55
								for _, v := range w.retained {
									if v.Maps.Small != nil {
										v.Maps.Small["retained"] = 66
									}
								}
							}
							compareInteractions(t, got, want)
						})
						count++
					}
				}
			}
		}
	}
	t.Logf("interaction sequences: %d, three successful decodes each", count)
}
