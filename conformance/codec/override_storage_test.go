package codec

import "testing"

// Registered leaves use generated collection traversal even on reused targets.
// Exercise its length/capacity rules separately from the ordinary scalar path.
func TestOverrideSliceStorage(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		for _, length := range []int{0, 1, 4} {
			v := Event{Items: make([]EventLeg, length, 8)}
			decode := v.UnmarshalZJSON
			if borrow {
				decode = v.UnmarshalZJSONBorrow
			}
			if err := decode([]byte(`{"items":[]}`)); err != nil {
				t.Fatal(err)
			}
			if v.Items == nil || len(v.Items) != 0 || cap(v.Items) != 0 {
				t.Fatalf("empty override slice retained capacity: borrow=%v len=%d cap=%d", borrow, len(v.Items), cap(v.Items))
			}
			root := make(DecimalList, 3)
			rootDecode := root.UnmarshalZJSON
			if borrow {
				rootDecode = root.UnmarshalZJSONBorrow
			}
			if err := rootDecode([]byte(`["1"]`)); err != nil {
				t.Fatal(err)
			}
			if len(root) != 1 {
				t.Fatalf("reused override root length: %d", len(root))
			}
		}
	}
}
