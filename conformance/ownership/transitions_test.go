package ownership

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Enumerate sizes and null/value choices, not a list of historical inputs.
// Pooled oracles reset per document; ordinary oracles retain caller storage.
func TestTransitions(t *testing.T) {
	type oracle Sample
	array := func(n int, null bool) string {
		parts := make([]string, n)
		for i := range parts {
			if null {
				parts[i] = "null"
			} else {
				parts[i] = fmt.Sprint(29 + i)
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	checks, mismatches := 0, 0
	for _, pooled := range []bool{false, true} {
		for _, borrow := range []bool{false, true} {
			for capacity := 0; capacity <= 8; capacity++ {
				for initial := 0; initial <= capacity; initial++ {
					for warm := 0; warm <= 5; warm++ {
						for short := 0; short <= 5; short++ {
							for grow := 0; grow <= 5; grow++ {
								for _, null := range []bool{false, true} {
									a, b := make([]int, capacity), make([]int, capacity)
									for i := range a {
										a[i] = 101 + i
										b[i] = 101 + i
									}
									got, want := Sample{a[:initial]}, oracle{b[:initial]}
									var d SampleDecoder
									for _, doc := range []string{
										`{"Values":` + array(warm, false) + `}`,
										`{"Values":` + array(short, null) + `,"Values":` + array(grow, null) + `}`,
									} {
										checks++
										if pooled {
											want = oracle{}
										}
										input := []byte(doc)
										var err error
										if pooled {
											if borrow {
												err = d.DecodeBorrow(input, &got)
											} else {
												err = d.Decode(input, &got)
											}
										} else if borrow {
											err = got.UnmarshalZJSONBorrow(input)
										} else {
											err = got.UnmarshalZJSON(input)
										}
										se := json.Unmarshal([]byte(doc), &want)
										bad := err != nil || se != nil || !reflect.DeepEqual(oracle(got), want) || !reflect.DeepEqual(a, b) || string(input) != doc
										if grow == 0 && strings.Contains(doc, `,"Values"`) {
											bad = bad || cap(got.Values) != 0
										}
										if bad {
											mismatches++
											if mismatches <= 3 {
												t.Logf("pool=%v borrow=%v cap=%d initial=%d warm=%d short=%d grow=%d null=%v got=%v want=%v", pooled, borrow, capacity, initial, warm, short, grow, null, got, want)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("checks=%d mismatches=%d", checks, mismatches)
	if mismatches != 0 {
		t.Fail()
	}
}

var payloadSink Payload

func TestCallbackTransitions(t *testing.T) {
	defer func() { observe = nil }()
	checks, mismatches := 0, 0
	for _, pooled := range []bool{false, true} {
		for _, borrow := range []bool{false, true} {
			for capacity := 0; capacity <= 6; capacity++ {
				for initial := 0; initial <= capacity; initial++ {
					for size := 0; size <= 8; size++ {
						got, want := ObservedPayload{make([]Observed, initial, capacity)}, ObservedPayload{make([]Observed, initial, capacity)}
						if pooled {
							want = ObservedPayload{}
						}
						values := make([]int, size)
						for i := range values {
							values[i] = i + 1
						}
						input, _ := json.Marshal(struct{ Values []int }{values})
						original := string(input)
						var gs, ws []string
						observe = func() { gs = append(gs, fmt.Sprint(got.Values)) }
						var d ObservedPayloadDecoder
						var err error
						if pooled {
							if borrow {
								err = d.DecodeBorrow(input, &got)
							} else {
								err = d.Decode(input, &got)
							}
						} else if borrow {
							err = got.UnmarshalZJSONBorrow(input)
						} else {
							err = got.UnmarshalZJSON(input)
						}
						observe = func() { ws = append(ws, fmt.Sprint(want.Values)) }
						se := json.Unmarshal([]byte(original), &want)
						checks++
						if err != nil || se != nil || string(input) != original || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gs, ws) {
							mismatches++
							if mismatches <= 3 {
								t.Logf("pool=%v borrow=%v initial=%d cap=%d size=%d snapshots=%v/%v", pooled, borrow, initial, capacity, size, gs, ws)
							}
						}
					}
				}
			}
		}
	}
	t.Logf("checks=%d mismatches=%d", checks, mismatches)
	if mismatches != 0 {
		t.Fail()
	}
}

func TestPayloadValues(t *testing.T) {
	data := []byte(`{"ID":"sample","Count":7,"Ratio":1.25,"Stamp":"2020-01-02T03:04:05Z","Names":["a","b"],"Items":[{"ID":"item","Quantity":3,"Price":2.5}],"Index":{"a":1,"b":2},"Optional":9}`)
	var want Payload
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var d PayloadDecoder
	for _, mode := range []string{"copy", "borrow", "pool-copy", "pool-borrow"} {
		var got Payload
		var err error
		switch mode {
		case "copy":
			err = got.UnmarshalZJSON(data)
		case "borrow":
			err = got.UnmarshalZJSONBorrow(data)
		case "pool-copy":
			err = d.Decode(data, &got)
		case "pool-borrow":
			err = d.DecodeBorrow(data, &got)
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got=%+v want=%+v err=%v", mode, got, want, err)
		}
	}
}

func BenchmarkPayload(b *testing.B) {
	for _, size := range []int{0, 4, 64} {
		v := Payload{ID: "synthetic-identifier", Count: 17, Ratio: 1.25}
		for i := 0; i < size; i++ {
			v.Names = append(v.Names, fmt.Sprint("name-", i))
			v.Items = append(v.Items, Item{fmt.Sprint(i), i, 1.25})
		}
		v.Index = map[string]int{"a": 1, "b": 2}
		n := 17
		v.Optional = &n
		data, err := json.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		for _, mode := range []string{"reuse-copy", "reuse-borrow", "pool-copy", "pool-borrow", "stdlib-reuse", "stdlib-reset"} {
			b.Run(fmt.Sprintf("n%d/%s", size, mode), func(b *testing.B) {
				var got Payload
				var d PayloadDecoder
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()
				for range b.N {
					switch mode {
					case "reuse-copy":
						err = got.UnmarshalZJSON(data)
					case "reuse-borrow":
						err = got.UnmarshalZJSONBorrow(data)
					case "pool-copy":
						err = d.Decode(data, &got)
					case "pool-borrow":
						err = d.DecodeBorrow(data, &got)
					case "stdlib-reuse":
						err = json.Unmarshal(data, &got)
					case "stdlib-reset":
						got = Payload{}
						err = json.Unmarshal(data, &got)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				payloadSink = got
			})
		}
	}
}
