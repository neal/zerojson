package p

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"testing"
)

const campaignOperations = 16

// External references deliberately outlive field replacement/null. Comparing
// values alone cannot detect a decoder that silently re-points to its arena.
type campaignGraph struct {
	m map[string]int
	v *CampaignState
	n *int
	c *CampaignCounter
	s *string
}

func newCampaignGraph(shared bool) campaignGraph {
	n, c, s := 7, CampaignCounter(11), "initial"
	g := campaignGraph{n: &n, c: &c, s: &s}
	g.m = map[string]int{"keep": 7}
	g.v = &CampaignState{A: &n, B: &n, C: &c, D: &c, Any: &c, S: &s, Nested: CampaignNested{P: &n}}
	g.v.Map, g.v.OtherMap = g.m, g.m
	g.v.MapPointer = &g.v.Map
	if !shared {
		n2, n3, c2, c3 := n, n, c, c
		g.v.B, g.v.Nested.P, g.v.D, g.v.Any = &n2, &n3, &c2, &c3
		g.v.OtherMap = maps.Clone(g.m)
	}
	return g
}

func cloneCampaignPointer[T any](p *T, seen map[*T]*T) *T {
	if p == nil {
		return nil
	}
	if q := seen[p]; q != nil {
		return q
	}
	q := new(T)
	*q = *p
	seen[p] = q
	return q
}

func cloneCampaignGraph(g campaignGraph) campaignGraph {
	ns, cs, ss := map[*int]*int{}, map[*CampaignCounter]*CampaignCounter{}, map[*string]*string{}
	r := g
	v := *g.v
	r.v = &v
	ms := map[uintptr]map[string]int{}
	cloneMap := func(m map[string]int) map[string]int {
		if m == nil {
			return nil
		}
		key := reflect.ValueOf(m).Pointer()
		if copy, ok := ms[key]; ok {
			return copy
		}
		copy := maps.Clone(m)
		ms[key] = copy
		return copy
	}
	r.m, r.v.Map, r.v.OtherMap = cloneMap(g.m), cloneMap(g.v.Map), cloneMap(g.v.OtherMap)
	if g.v.MapPointer == &g.v.Map {
		r.v.MapPointer = &r.v.Map
	} else if g.v.MapPointer != nil {
		m := cloneMap(*g.v.MapPointer)
		r.v.MapPointer = &m
	}
	r.n, r.c, r.s = cloneCampaignPointer(g.n, ns), cloneCampaignPointer(g.c, cs), cloneCampaignPointer(g.s, ss)
	r.v.A, r.v.B, r.v.Nested.P = cloneCampaignPointer(g.v.A, ns), cloneCampaignPointer(g.v.B, ns), cloneCampaignPointer(g.v.Nested.P, ns)
	r.v.C, r.v.D = cloneCampaignPointer(g.v.C, cs), cloneCampaignPointer(g.v.D, cs)
	r.v.S, r.v.R = cloneCampaignPointer(g.v.S, ss), bytes.Clone(g.v.R)
	if p, ok := g.v.Any.(*CampaignCounter); ok {
		r.v.Any = cloneCampaignPointer(p, cs)
	}
	return r
}

func campaignPointers(g campaignGraph) []any {
	return []any{g.v.A, g.v.B, g.v.C, g.v.D, g.v.Any, g.v.S, g.v.Nested.P, g.n, g.c, g.s, g.v.Map, g.v.OtherMap, g.m, g.v.MapPointer, &g.v.Map}
}

func campaignSamePointer(a, b any) bool {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	return x.IsValid() && y.IsValid() && (x.Kind() == reflect.Pointer || x.Kind() == reflect.Map) && x.Kind() == y.Kind() &&
		!x.IsNil() && !y.IsNil() && x.Type() == y.Type() && x.Pointer() == y.Pointer()
}

func campaignSameTopology(a, b []any) bool {
	for i := range a {
		for j := range a {
			if campaignSamePointer(a[i], a[j]) != campaignSamePointer(b[i], b[j]) {
				return false
			}
		}
	}
	return true
}

func checkCampaignGraph(t *testing.T, std, zj campaignGraph) {
	t.Helper()
	if !reflect.DeepEqual(std.v, zj.v) || !maps.Equal(std.m, zj.m) || *std.n != *zj.n || *std.c != *zj.c || *std.s != *zj.s {
		t.Fatalf("state/retained-pointee mismatch: std=%+v zerojson=%+v external=(%d,%d,%q)/(%d,%d,%q)", std.v, zj.v, *std.n, *std.c, *std.s, *zj.n, *zj.c, *zj.s)
	}
	if !campaignSameTopology(campaignPointers(std), campaignPointers(zj)) {
		t.Fatal("alias topology mismatch")
	}
}

// Controlled malformed/semantic-error inputs fail at the first value, so
// callback counts are comparable even though the partial target is not.
func campaignDocument(op, value byte) []byte {
	n := int(value) + 1
	switch op % campaignOperations {
	case 1:
		return []byte(fmt.Sprintf(`{"a":%d,"b":%d,"nested":{"p":%d}}`, n, n+1, n+2))
	case 2:
		return []byte(fmt.Sprintf(`{"b":%d,"unknown":{},"a":%d,"a":%d,"map_pointer":{"a":1},"map":null,"map_pointer":{"b":2},"map":{"c":3}}`, n, n+1, n+2))
	case 3:
		return []byte(`{"a":null,"c":null,"any":null,"s":null}`)
	case 4:
		return []byte(fmt.Sprintf(`{"a":%d,"b":%d,"s":"restored"}`, n, n+1))
	case 5:
		return []byte(fmt.Sprintf(`{"c":%d,"d":2,"any":3}`, n))
	case 6:
		return []byte(`{"c":-1}`)
	case 7:
		return []byte(`{"c":[}`)
	case 8:
		b, _ := json.Marshal(string([]byte{value, '\\', '"', '\n', 0xff}))
		return append(append([]byte(`{"s":`), b...), '}')
	case 9:
		return []byte(fmt.Sprintf(`{"r":{"long_raw_field":%d,"text":"abcdefghijk"}}`, n))
	case 10:
		return []byte(`{"r":{"x":2}}`)
	case 12:
		return []byte(`{"any":null,"nested":{"p":null},"r":null}`)
	case 13:
		return []byte(`null`)
	case 14:
		return []byte(`{"c":1,"unknown":{"s":"}\\\"{"},"d":2,"c":3}`)
	case 15:
		return []byte(`{"a":"wrong"}`)
	default:
		return []byte(`{}`)
	}
}

func runCampaignState(t *testing.T, program []byte) {
	t.Helper()
	if len(program) > 128 {
		program = program[:128]
	}
	std, zj := newCampaignGraph(true), newCampaignGraph(true)
	var decoder CampaignStateDecoder
	var pooled CampaignState
	var borrowed, originals [][]byte
	for step := 0; step < len(program); step += 2 {
		op := program[step]
		value := byte(step)
		if step+1 < len(program) {
			value = program[step+1]
		}
		if op%campaignOperations == 0 {
			std, zj = newCampaignGraph(value%2 == 0), newCampaignGraph(value%2 == 0)
		} else if op%campaignOperations == 11 {
			// Reset-first decoding must recover after invalid input without
			// invoking callbacks for that malformed document.
			campaignCalls = 0
			if err := decoder.Decode([]byte(`{"c":[}`), &pooled); err == nil || campaignCalls != 0 {
				t.Fatal("pool accepted malformed value")
			}
			doc := []byte(fmt.Sprintf(`{"a":%d,"b":%d,"c":1,"d":2,"any":3,"s":"pool","r":{"x":7}}`, int(value), int(value)+1))
			before := bytes.Clone(doc)
			var want CampaignState
			campaignCalls = 0
			se := json.Unmarshal(doc, &want)
			wantCalls := campaignCalls
			campaignCalls = 0
			var ze error
			if op&16 != 0 {
				ze = decoder.DecodeBorrow(doc, &pooled)
			} else {
				ze = decoder.Decode(doc, &pooled)
			}
			if se != nil || ze != nil || campaignCalls != wantCalls || !reflect.DeepEqual(want, pooled) {
				t.Fatalf("pool reuse mismatch: std=%+v (%v) zerojson=%+v (%v) calls=%d/%d", want, se, pooled, ze, wantCalls, campaignCalls)
			}
			if !campaignSameTopology(campaignPointers(campaignGraph{v: &want}), campaignPointers(campaignGraph{v: &pooled})) {
				t.Fatal("pool alias topology mismatch")
			}
			if !bytes.Equal(doc, before) {
				t.Fatal("pool changed its input")
			}
			if op&16 != 0 {
				borrowed, originals = append(borrowed, doc), append(originals, before)
			} else {
				for i := range doc {
					doc[i] = '#'
				}
				if !reflect.DeepEqual(want, pooled) {
					t.Fatal("pool copy retained its input")
				}
			}
		} else {
			doc := campaignDocument(op, value)
			before := bytes.Clone(doc)
			campaignCalls = 0
			se := json.Unmarshal(doc, std.v)
			wantCalls := campaignCalls
			campaignCalls = 0
			var ze error
			if op%campaignOperations == 10 {
				// stdlib after a borrow decode must detach Raw's storage.
				ze = json.Unmarshal(doc, zj.v)
			} else if op&16 != 0 {
				ze = zj.v.UnmarshalZJSONBorrow(doc)
				borrowed, originals = append(borrowed, doc), append(originals, before)
			} else {
				ze = zj.v.UnmarshalZJSON(doc)
			}
			if (se == nil) != (ze == nil) || wantCalls != campaignCalls {
				t.Fatalf("step=%d op=%d doc=%q errors=%v/%v callbacks=%d/%d program=%x", step/2, op, doc, se, ze, wantCalls, campaignCalls, program)
			}
			if ze != nil {
				// The error contract does NOT promise transactional writes or
				// stdlib-identical partial values. Start the NEXT operation's
				// independent oracle from the same graph, preserving aliases.
				// Never normalize a successful decode this way.
				std = cloneCampaignGraph(zj)
			}
			if !bytes.Equal(doc, before) {
				t.Fatal("decode changed its input")
			}
			if op&16 == 0 || op%campaignOperations == 10 {
				for i := range doc {
					doc[i] = '#'
				}
			}
		}
		checkCampaignGraph(t, std, zj)
		for i := range borrowed {
			if !bytes.Equal(borrowed[i], originals[i]) {
				t.Fatalf("step=%d overwrote borrowed buffer %d program=%x", step/2, i, program)
			}
		}
	}
}

func TestCampaignGraphClone(t *testing.T) {
	for _, shared := range []bool{false, true} {
		g := newCampaignGraph(shared)
		g.v.R = []byte(`{"x":1}`)
		clone := cloneCampaignGraph(g)
		checkCampaignGraph(t, g, clone)
		for _, a := range campaignPointers(g) {
			for _, b := range campaignPointers(clone) {
				if campaignSamePointer(a, b) {
					t.Fatal("oracle checkpoint aliases decoder state")
				}
			}
		}
		g.v.R[0] = '!'
		if clone.v.R[0] != '{' {
			t.Fatal("oracle checkpoint aliases Raw")
		}
		// The observer must catch topology changes even when DeepEqual
		// sees exactly the same pointee values.
		broken := cloneCampaignGraph(clone)
		n := *broken.v.A
		broken.v.A = &n
		if !reflect.DeepEqual(clone.v, broken.v) || campaignSameTopology(campaignPointers(clone), campaignPointers(broken)) {
			t.Fatal("topology observer is vacuous")
		}
	}
}

func TestCampaignStateTransitions(t *testing.T) {
	// Cover every ordered operation pair, in both copy/borrow modes,
	// with recovery and pool reuse after each pair. No random coverage hole.
	for mode := byte(0); mode <= 16; mode += 16 {
		for a := byte(0); a < campaignOperations; a++ {
			for b := byte(0); b < campaignOperations; b++ {
				t.Run(fmt.Sprintf("%d/%d/%d", mode, a, b), func(t *testing.T) {
					runCampaignState(t, []byte{a | mode, 2, b | mode, 3, 4 | mode, 4, 11 | mode, 5})
				})
			}
		}
	}
}

func FuzzCampaignState(f *testing.F) {
	for a := byte(0); a < campaignOperations; a++ {
		f.Add([]byte{a | 16, 2, 10, 3, 6, 4, 11, 5, 0, 1, 5, 6})
	}
	f.Fuzz(runCampaignState)
}
