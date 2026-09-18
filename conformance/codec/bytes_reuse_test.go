package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestByteSliceReuseState(t *testing.T) {
	for _, doc := range []string{
		`{"data":"aGVsbG8=","data":[null,null,null,null,null]}`,
		`{"data":[null,1,null]}`, `{"data":[]}`, `{"data":null}`,
		`{"ptr":[null,1,null]}`, `{"lists":[[null,1,null]]}`,
		`{"index":{"a":[null,1,null]}}`,
	} {
		for _, borrow := range []bool{false, true} {
			makeTarget := func() (BytesCases, []byte) {
				backing := []byte("hello")
				p := []byte("world")
				return BytesCases{Data: backing[:1], Ptr: &p, Lists: [][]byte{[]byte("abc")}, Index: map[string][]byte{"a": []byte("xyz")}}, backing
			}
			got, gotBacking := makeTarget()
			want, wantBacking := makeTarget()
			var err error
			if borrow {
				err = got.UnmarshalZJSONBorrow([]byte(doc))
			} else {
				err = got.UnmarshalZJSON([]byte(doc))
			}
			stdErr := json.Unmarshal([]byte(doc), &want)
			if err != nil || stdErr != nil {
				t.Fatalf("%s: %v / %v", doc, err, stdErr)
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotBacking, wantBacking) {
				t.Errorf("%s borrow=%v: got %+v backing=%v; want %+v backing=%v", doc, borrow, got, gotBacking, want, wantBacking)
			}
		}
	}
}
