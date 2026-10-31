package xpkg

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neal/zerojson/conformance/generator/xpkg/inner"
)

// TestForeignSliceElementsReuseAsStdlib pins that slices of a type generated
// in another package reuse their elements as encoding/json does: a repeated
// member or a prepopulated slice decodes into the elements and pointers it
// already holds, and fields the input omits keep their values.
func TestForeignSliceElementsReuseAsStdlib(t *testing.T) {
	seeded := func(o *Outer) {
		o.S = []inner.Ext{{Name: "a", N: 1}}
		o.SP = []*inner.Ext{{Name: "b", N: 2}}
	}
	decoders := map[string]func(*Outer, []byte) error{
		"copy":    (*Outer).UnmarshalZJSON,
		"borrow":  (*Outer).UnmarshalZJSONBorrow,
		"trusted": (*Outer).UnmarshalZJSONTrusted,
	}
	for _, c := range []struct {
		doc  string
		seed func(*Outer)
	}{
		{`{"s":[{"name":"old","n":42}],"s":[{"name":"new"}],"sp":[{"name":"old","n":7}],"sp":[{"name":"new"}]}`, func(*Outer) {}},
		{`{"s":[{"name":"x"}],"sp":[{"name":"y"}]}`, seeded},
		{`{"s":[null],"sp":[null]}`, seeded},
		{`{"s":[{"name":"x"},{"name":"y"}],"sp":[{"name":"y"},{"name":"z"}]}`, seeded},
	} {
		for mode, decode := range decoders {
			var want, got Outer
			c.seed(&want)
			c.seed(&got)
			wantPtrs, gotPtrs := append([]*inner.Ext(nil), want.SP...), append([]*inner.Ext(nil), got.SP...)
			if err := json.Unmarshal([]byte(c.doc), &want); err != nil {
				t.Fatal(err)
			}
			if err := decode(&got, []byte(c.doc)); err != nil {
				t.Fatalf("%s %s: %v", mode, c.doc, err)
			}
			if !reflect.DeepEqual(&want, &got) {
				t.Fatalf("%s %s\nencoding/json: %+v\nzerojson:      %+v", mode, c.doc, want, got)
			}
			for i := range wantPtrs {
				if i < len(want.SP) && (want.SP[i] == wantPtrs[i]) != (got.SP[i] == gotPtrs[i]) {
					t.Fatalf("%s %s: element %d pointer reused by encoding/json %v, by zerojson %v", mode, c.doc, i, want.SP[i] == wantPtrs[i], got.SP[i] == gotPtrs[i])
				}
			}
		}
	}
}
