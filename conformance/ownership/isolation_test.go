package ownership

import (
	"encoding/json"
	"fmt"
	"testing"
)

func payloadSnapshot(t *testing.T, v Payload) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Equal values can conceal shared storage from DeepEqual. Mutate in both
// directions after decoding, including storage published by a failed call.
// Failed partial values are snapshotted, not compared with stdlib's partial
// values: the public contract does not promise identical error-side effects.
func TestResultStorageIsolation(t *testing.T) {
	const fields = `"Names":["same"],"Items":[{"ID":"same","Quantity":1}],"Index":{"same":1},"Optional":1`
	for _, firstBorrow := range []bool{false, true} {
		for _, nextBorrow := range []bool{false, true} {
			for _, firstFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("firstBorrow=%v/nextBorrow=%v/firstFails=%v", firstBorrow, nextBorrow, firstFails), func(t *testing.T) {
					var d PayloadDecoder
					decode := func(data []byte, v *Payload, borrow bool) error {
						if borrow {
							return d.DecodeBorrow(data, v)
						}
						return d.Decode(data, v)
					}
					firstDoc := "{" + fields + "}"
					if firstFails {
						firstDoc = "{" + fields + `,"Count":{}}`
					}
					input := []byte(firstDoc)
					var current Payload
					err := decode(input, &current, firstBorrow)
					if (err != nil) != firstFails {
						t.Fatalf("first decode: %v", err)
					}
					if len(current.Names) != 1 || len(current.Items) != 1 || len(current.Index) != 1 || current.Optional == nil {
						t.Fatal("first call did not publish the storage being tested")
					}
					first := current
					before := payloadSnapshot(t, first)
					nextInput := []byte("{" + fields + "}")
					if err := decode(nextInput, &current, nextBorrow); err != nil {
						t.Fatal(err)
					}
					current.Names[0] = "second"
					current.Items[0].Quantity = 2
					current.Index["same"] = 2
					*current.Optional = 2
					if payloadSnapshot(t, first) != before {
						t.Fatal("later result shares earlier result storage")
					}
					nextBefore := payloadSnapshot(t, current)
					first.Names[0] = "first"
					first.Items[0].Quantity = 3
					first.Index["same"] = 3
					*first.Optional = 3
					if payloadSnapshot(t, current) != nextBefore {
						t.Fatal("earlier result shares later result storage")
					}
					if string(input) != firstDoc || string(nextInput) != "{"+fields+"}" {
						t.Fatal("decode changed input")
					}
				})
			}
		}
	}
}

func TestResultStorageSurvivesCallbackPanic(t *testing.T) {
	defer func() { observe = nil }()
	for _, borrow := range []bool{false, true} {
		var d ObservedPayloadDecoder
		var current ObservedPayload
		decode := d.Decode
		if borrow {
			decode = d.DecodeBorrow
		}
		calls := 0
		marker := new(int)
		observe = func() {
			calls++
			if calls == 2 {
				panic(marker)
			}
		}
		func() {
			defer func() {
				if got := recover(); got != marker {
					t.Fatalf("panic = %v, want marker", got)
				}
			}()
			_ = decode([]byte(`{"Values":[1,2]}`), &current)
		}()
		observe = nil
		if calls != 2 || len(current.Values) != 2 || current.Values[0] != 1 {
			t.Fatal("panic did not leave observable partial storage")
		}
		retained := current.Values
		before := append([]Observed(nil), retained...)
		if err := decode([]byte(`{"Values":[1,2]}`), &current); err != nil {
			t.Fatal(err)
		}
		current.Values[0] = 9
		for i := range before {
			if retained[i] != before[i] {
				t.Fatal("panic result changed after recovery decode")
			}
		}
	}
}
