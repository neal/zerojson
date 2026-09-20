package p

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCallbackBoundary(t *testing.T) {
	for _, doc := range []string{`{"A":1,"S":[2],"M":{"k":3},"Z":0}`, `{"A":1,"S":[2],"M":{"k":3}, "Z":0}`, `{"A":1,"S":[2],"M":{"k":3},"A":4}`} {
		var got, want Callbacks
		Calls = 0
		e := got.UnmarshalZJSON([]byte(doc))
		n := Calls
		Calls = 0
		se := json.Unmarshal([]byte(doc), &want)
		if e != nil || se != nil || n != Calls || !reflect.DeepEqual(got, want) {
			t.Fatalf("callback %s calls=%d/%d errors=%v/%v", doc, n, Calls, e, se)
		}
	}
	// Only the observed count has a side effect; the registered reader's value
	// transformation is idempotent, as its extension contract requires.
	for _, doc := range []string{`{"A":7,"Z":0}`, `{"A":7, "Z":0}`, `{"A":7,"A":8}`} {
		var got Leaves
		LeafCalls = 0
		if e := got.UnmarshalZJSON([]byte(doc)); e != nil {
			t.Fatal(e)
		}
		var want struct {
			A int64
			Z int
		}
		if e := json.Unmarshal([]byte(doc), &want); e != nil {
			t.Fatal(e)
		}
		if int64(got.A) != want.A {
			t.Fatalf("leaf %s %v/%v", doc, got, want)
		}
		t.Logf("registered leaf %s calls=%d", doc, LeafCalls)
	}
}
