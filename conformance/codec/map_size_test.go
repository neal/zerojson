package codec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"
)

// mapSizes straddles the generated map helpers' boundaries: 8 members on the
// stack, a spill buffer of 17 to 40 depending on the entry size, and direct
// insertion past twice that.
var mapSizes = []int{0, 1, 7, 8, 9, 16, 17, 18, 33, 34, 35, 39, 40, 41, 79, 80, 81, 200}

// mapDoc renders an object of n members whose values come from value. A
// non-negative dup repeats that member's key last, with the value of member
// n, so the last occurrence must win wherever the first one was buffered.
func mapDoc(n, dup int, value func(i int) string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%03d":%s`, i, value(i))
	}
	if dup >= 0 && dup < n {
		fmt.Fprintf(&b, `,"k%03d":%s`, dup, value(n))
	}
	b.WriteByte('}')
	return b.String()
}

// anyValue cycles through every ReadInterface shape, with nested objects of
// assorted sizes so their own buffering is covered too.
func anyValue(i int) string {
	switch i % 6 {
	case 0:
		return fmt.Sprintf(`"s%d"`, i)
	case 1:
		return fmt.Sprintf("%d.5", i)
	case 2:
		return "true"
	case 3:
		return "null"
	case 4:
		return mapDoc(i%45, -1, func(j int) string { return fmt.Sprint(j) })
	default:
		return fmt.Sprintf(`[%d,{"x":%d}]`, i, i)
	}
}

// mapSizeCase decodes one document into a fresh zerojson target and a fresh
// encoding/json target, optionally seeded with the same existing contents.
type mapSizeCase struct {
	name  string
	value func(i int) string
	wrap  func(obj string) string
	check func(t *testing.T, data []byte, seed bool)
}

func compareDecode[T any](t *testing.T, data []byte, seed func(*T), decode func(*T, []byte) error) {
	t.Helper()
	var got, want T
	if seed != nil {
		seed(&got)
		seed(&want)
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("encoding/json: %v\n%s", err, data)
	}
	if err := decode(&got, data); err != nil {
		t.Fatalf("zerojson: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch for %s\nzerojson: %#v\nencoding/json: %#v", data, got, want)
	}
}

func TestMapSizesMatchStdlib(t *testing.T) {
	field := func(name string) func(string) string {
		return func(obj string) string { return `{"` + name + `":` + obj + `}` }
	}
	root := func(obj string) string { return obj }
	cases := []mapSizeCase{
		{
			name: "interface_values", value: anyValue, wrap: field("bag"),
			check: func(t *testing.T, data []byte, seed bool) {
				var s func(*Event)
				if seed {
					s = func(v *Event) { v.Bag = map[string]any{"old": "kept", "k000": "replaced"} }
				}
				compareDecode(t, data, s, (*Event).UnmarshalZJSON)
			},
		},
		{
			name: "interned_leaf_values", value: func(i int) string { return fmt.Sprintf(`"%d.25"`, i) }, wrap: field("amounts"),
			check: func(t *testing.T, data []byte, seed bool) {
				var s func(*Event)
				if seed {
					s = func(v *Event) { v.Amounts = map[string]decimal.Decimal{"old": decimal.NewFromInt(1)} }
				}
				compareDecode(t, data, s, (*Event).UnmarshalZJSON)
			},
		},
		{
			name: "pointer_field", value: func(i int) string { return fmt.Sprint(i * 3) }, wrap: field("index"),
			check: func(t *testing.T, data []byte, seed bool) {
				var s func(*PointerCollections)
				if seed {
					s = func(v *PointerCollections) { v.Index = &map[string]int{"old": 1, "k000": 2} }
				}
				compareDecode(t, data, s, (*PointerCollections).UnmarshalZJSON)
			},
		},
		{
			name: "struct_values_root", value: func(i int) string { return fmt.Sprintf(`{"ratio":"%d.5","label":"l%d","mode":"up"}`, i, i) }, wrap: root,
			check: func(t *testing.T, data []byte, seed bool) {
				var s func(*LegIndex)
				if seed {
					s = func(v *LegIndex) { *v = LegIndex{"old": fullEventLeg(), "k000": fullEventLeg()} }
				}
				compareDecode(t, data, s, (*LegIndex).UnmarshalZJSON)
			},
		},
		{
			name: "byte_slice_values", value: func(i int) string { return fmt.Sprintf(`"%s"`, strings.Repeat("QUJD", i%4)) }, wrap: field("index"),
			check: func(t *testing.T, data []byte, seed bool) {
				var s func(*BytesCases)
				if seed {
					s = func(v *BytesCases) { v.Index = map[string][]byte{"old": []byte("x")} }
				}
				compareDecode(t, data, s, (*BytesCases).UnmarshalZJSON)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range mapSizes {
				for _, dup := range []int{-1, 0, 8, 17, 40, n - 1} {
					data := []byte(c.wrap(mapDoc(n, dup, c.value)))
					c.check(t, data, false)
					c.check(t, data, true)
				}
			}
		})
	}
}

var mapSink map[string]int

// TestMapDecodeMakesMapOnce pins that a decoded map is made at its final
// size instead of grown member by member: beyond one key copy per member,
// decoding n members allocates exactly what make(map, n) and n inserts do.
func TestMapDecodeMakesMapOnce(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	var v ReuseSemantics
	for _, n := range []int{1, 8, 9, 17, 33, 40} {
		data := []byte(`{"values":` + mapDoc(n, -1, strconv.Itoa) + `}`)
		keys := make([]string, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("k%03d", i)
		}
		decoded := testing.AllocsPerRun(100, func() {
			v.Values = nil
			if err := v.UnmarshalZJSONBorrow(data); err != nil {
				t.Fatal(err)
			}
		})
		made := testing.AllocsPerRun(100, func() {
			m := make(map[string]int, n)
			for i, k := range keys {
				m[k] = i
			}
			mapSink = m
		})
		if want := made + float64(n); decoded != want {
			t.Errorf("%d members: decode allocations=%v, want %v (a key copy each plus one map made at its final size)", n, decoded, want)
		}
	}
}
