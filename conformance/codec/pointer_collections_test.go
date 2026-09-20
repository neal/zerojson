package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPointerCollectionsMatchStdlib pins *[]T / *map[string]T fields
// against encoding/json across every pointer/pointee state, including a
// non-nil pointer to a nil map.
func TestPointerCollectionsMatchStdlib(t *testing.T) {
	names := []string{"a", "b"}
	emptyNames := []string{}
	index := map[string]int{"k": 1}
	var nilIndex map[string]int
	values := []PointerCollections{
		{},
		{Names: &names, Index: &index},
		{Names: &emptyNames, Index: &map[string]int{}},
		{Index: &nilIndex}, // pointer to a nil map encodes as null
	}
	for _, v := range values {
		zj, err := v.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		std, _ := json.Marshal(v)
		if string(zj) != string(std) {
			t.Fatalf("encode mismatch\n zj: %s\nstd: %s", zj, std)
		}
	}

	docs := []string{
		`{"names":null,"index":null}`,
		`{"names":[],"index":{}}`,
		`{"names":["x"],"index":{"k":2}}`,
		`{"names":["x"],"index":{"k":2},"legs":[{"ratio":"1.5","label":"L"}],"tags":{"t":"1.00"}}`,
		`{"index":{"a":1},"index":{"b":2}}`,
	}
	// Targets: zero; pointer to nil map (the panic case); pointer to a
	// populated map (stdlib keeps existing entries); pointer to nil slice.
	targets := []func() PointerCollections{
		func() PointerCollections { return PointerCollections{} },
		func() PointerCollections { var m map[string]int; return PointerCollections{Index: &m} },
		func() PointerCollections { m := map[string]int{"keep": 9}; return PointerCollections{Index: &m} },
		func() PointerCollections { var s []string; return PointerCollections{Names: &s} },
	}
	for _, doc := range docs {
		for ti, mk := range targets {
			zjT, stdT := mk(), mk()
			zjErr := zjT.UnmarshalZJSON([]byte(doc))
			stdErr := json.Unmarshal([]byte(doc), &stdT)
			if (zjErr == nil) != (stdErr == nil) {
				t.Fatalf("%s target#%d: zerojson err=%v stdlib err=%v", doc, ti, zjErr, stdErr)
			}
			if !reflect.DeepEqual(zjT, stdT) {
				t.Fatalf("%s target#%d decode mismatch\n zj: %+v\nstd: %+v", doc, ti, dump(zjT), dump(stdT))
			}
		}
		// Pooled decoder, twice, must equal a fresh decode each time.
		var d PointerCollectionsDecoder
		for i := 0; i < 2; i++ {
			var pooled, fresh PointerCollections
			if err := d.Decode([]byte(doc), &pooled); err != nil {
				t.Fatalf("pooled decode %s: %v", doc, err)
			}
			if err := fresh.UnmarshalZJSON([]byte(doc)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pooled, fresh) {
				t.Fatalf("pooled decode #%d of %s differs from fresh\npooled: %+v\nfresh:  %+v", i, doc, dump(pooled), dump(fresh))
			}
		}
	}
}

// dump renders a PointerCollections with pointees visible, for failure output.
func dump(v PointerCollections) string {
	b, _ := json.Marshal(v)
	return string(b)
}
