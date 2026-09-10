package ownership

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// Retain complete earlier results while the same Decoder handles changing
// collection sizes, nulls, duplicate members, errors, and recovery. The
// independent oracle resets before each call, but never rewrites old results.
func TestResultLifetime(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		t.Run(fmt.Sprint("borrow=", borrow), func(t *testing.T) {
			var d PayloadDecoder
			var got Payload
			var retained, expected []Payload
			for _, size := range []int{8, 0, 1, 16, 2} {
				value := Payload{ID: fmt.Sprint(size), Index: map[string]int{"key": size}}
				n := size
				value.Optional = &n
				for i := 0; i < size; i++ {
					value.Names = append(value.Names, fmt.Sprint(i))
					value.Items = append(value.Items, Item{ID: fmt.Sprint(i), Quantity: i})
				}
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				for _, input := range [][]byte{data, []byte(`{"Names":["first"],"Names":null,"Names":["last"],"Index":null,"Optional":null}`), []byte(`{"Names":`), []byte(`{}`)} {
					var want Payload
					se := json.Unmarshal(append([]byte(nil), input...), &want)
					if borrow {
						err = d.DecodeBorrow(input, &got)
					} else {
						err = d.Decode(input, &got)
					}
					if (err == nil) != (se == nil) {
						t.Fatalf("error parity: %v / %v", err, se)
					}
					if err == nil {
						if !reflect.DeepEqual(got, want) {
							t.Fatal("current result mismatch")
						}
						retained = append(retained, got)
						expected = append(expected, want)
					}
					if !reflect.DeepEqual(retained, expected) {
						t.Fatal("earlier result changed after a later decode")
					}
				}
			}
		})
	}
}
