package challenge

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

type recordDecoder func([]byte, *Record) error

// Separate, methodless twins prevent generated UnmarshalJSON wrappers from
// contaminating the stdlib oracle in the compatibility build.
func TestCompatContainerObservations(t *testing.T) {
	type plainNode struct{ Value Observed }
	type plainView struct {
		Values   []Observed
		Pointers []*Observed
		Indirect *[]Observed
		Index    map[string]Observed
		Nodes    []plainNode
		NodeMap  map[string]plainNode
	}
	defer func() { valueCallback = nil }()
	for _, input := range []string{`{"Nodes":[{"Value":1},{"Value":2}]}`, `{"NodeMap":{"x":{"Value":1},"y":{"Value":2}}}`} {
		var want plainView
		var wantSnapshots, gotSnapshots []string
		capture := func(v any, snapshots *[]string) {
			// Keep generated encode map order out of this decode oracle.
			if value, ok := v.(CallbackView); ok {
				type methodlessView CallbackView
				v = methodlessView(value)
			}
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			*snapshots = append(*snapshots, string(b))
		}
		valueCallback = func() { capture(want, &wantSnapshots) }
		if err := json.Unmarshal([]byte(input), &want); err != nil {
			t.Fatal(err)
		}
		capture(want, &wantSnapshots)
		var got CallbackView
		valueCallback = func() { capture(got, &gotSnapshots) }
		if err := got.UnmarshalZJSON([]byte(input)); err != nil {
			t.Fatal(err)
		}
		capture(got, &gotSnapshots)
		if !reflect.DeepEqual(gotSnapshots, wantSnapshots) {
			t.Fatalf("compat callback snapshots: got=%v want=%v", gotSnapshots, wantSnapshots)
		}
	}
}

func TestOrdinaryStdlibContract(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		var got, want Ordinary
		for _, input := range []string{`{"name":1,"Numbers":[7,8,9],"Items":[{"A":1}]}`, `{"NAME":2,"Numbers":[null,1],"Items":[{"B":2}]}`, `{"Name":3,"name":4}`, `{"Numbers":[01]}`, `{"NAME":null}`, `{"A":1,"a":2,"K":3,"\u017f":4,"\u212a":5}`} {
			se := json.Unmarshal([]byte(input), &want)
			var ze error
			if borrow {
				ze = got.UnmarshalZJSONBorrow([]byte(input))
			} else {
				ze = got.UnmarshalZJSON([]byte(input))
			}
			if (se == nil) != (ze == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("ordinary compatibility %s: std=%+v/%v zero=%+v/%v", input, want, se, got, ze)
			}
		}
	}
}

func TestWholeDocumentValidationBeforeCallbacks(t *testing.T) {
	defer func() { callback = nil }()
	for _, input := range []string{`{"H":0,"S":[}`, `{"H":0}x`, `{"H":0,"P":01}`} {
		for _, pooled := range []bool{false, true} {
			for _, borrow := range []bool{false, true} {
				calls := 0
				callback = func(int) error { calls++; return nil }
				var v Record
				var d RecordDecoder
				var err error
				if pooled {
					if borrow {
						err = d.DecodeBorrow([]byte(input), &v)
					} else {
						err = d.Decode([]byte(input), &v)
					}
				} else {
					if borrow {
						err = v.UnmarshalZJSONBorrow([]byte(input))
					} else {
						err = v.UnmarshalZJSON([]byte(input))
					}
				}
				if err == nil || calls != 0 {
					t.Fatalf("syntax preflight %s: error=%v callbacks=%d", input, err, calls)
				}
			}
		}
	}
}

func TestRootSyntaxPreflight(t *testing.T) {
	defer func() { valueCallback = nil }()
	for _, input := range []string{`[1,2]x`, `[1,]`} {
		for _, borrow := range []bool{false, true} {
			calls := 0
			valueCallback = func() { calls++ }
			var v ObservedList
			var err error
			if borrow {
				err = v.UnmarshalZJSONBorrow([]byte(input))
			} else {
				err = v.UnmarshalZJSON([]byte(input))
			}
			if err == nil || calls != 0 {
				t.Fatalf("root syntax preflight: input=%s err=%v calls=%d", input, err, calls)
			}
		}
	}
}

func TestCustomContainerAllocationCost(t *testing.T) {
	valueCallback = nil
	data := []byte(`{"Values":[1,2]}`)
	var d CallbackViewDecoder
	var v CallbackView
	pooled := testing.AllocsPerRun(100, func() {
		if err := d.Decode(data, &v); err != nil {
			panic(err)
		}
	})
	stdlib := testing.AllocsPerRun(100, func() {
		v = CallbackView{}
		if err := json.Unmarshal(data, &v); err != nil {
			panic(err)
		}
	})
	t.Logf("custom container allocations: pooled=%g stdlib-fresh=%g", pooled, stdlib)
}

func TestCallbackContainerObservations(t *testing.T) {
	defer func() { valueCallback = nil }()
	for _, input := range []string{`{"Nodes":[{"Value":1},{"Value":2}]}`, `{"NodeMap":{"x":{"Value":1},"y":{"Value":2}}}`, `{"Pointers":[1,2]}`, `{"Indirect":[1,2]}`, `{"Index":{"x":1,"y":2}}`, `{"Values":[1,2],"Values":[3]}`, `{"Values":[1,2]}`} {
		for _, pooled := range []bool{false, true} {
			for _, borrow := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/pool=%v/borrow=%v", input, pooled, borrow), func(t *testing.T) {
					observe := func(decode func([]byte, *CallbackView) error) []string {
						var v CallbackView
						var snapshots []string
						valueCallback = func() {
							b, err := json.Marshal(v)
							if err != nil {
								t.Fatal(err)
							}
							snapshots = append(snapshots, string(b))
							if input == `{"Values":[1,2]}` && len(snapshots) == 1 {
								// The callback may replace the very slice being decoded.
								v.Values = []Observed{7, 8, 9}
							}
						}
						if err := decode([]byte(input), &v); err != nil {
							t.Fatal(err)
						}
						valueCallback()
						return snapshots
					}
					want := observe(func(b []byte, v *CallbackView) error { return json.Unmarshal(b, v) })
					var d CallbackViewDecoder
					got := observe(func(b []byte, v *CallbackView) error {
						if pooled {
							if borrow {
								return d.DecodeBorrow(b, v)
							}
							return d.Decode(b, v)
						}
						if borrow {
							return v.UnmarshalZJSONBorrow(b)
						}
						return v.UnmarshalZJSON(b)
					})
					if !slices.Equal(got, want) {
						t.Fatalf("container callback snapshots: got=%v want=%v", got, want)
					}
				})
			}
		}
	}
}

func TestRootCallbackPublication(t *testing.T) {
	defer func() { valueCallback = nil }()
	for _, borrow := range []bool{false, true} {
		var list ObservedList
		var lengths []int
		valueCallback = func() { lengths = append(lengths, len(list)) }
		var err error
		if borrow {
			err = list.UnmarshalZJSONBorrow([]byte(`[1,2]`))
		} else {
			err = list.UnmarshalZJSON([]byte(`[1,2]`))
		}
		if err != nil || !slices.Equal(lengths, []int{1, 2}) {
			t.Fatalf("root callback lengths=%v err=%v", lengths, err)
		}
		var index ObservedIndex
		lengths = nil
		valueCallback = func() {
			if index == nil {
				t.Error("root map not published")
			}
			lengths = append(lengths, len(index))
		}
		if borrow {
			err = index.UnmarshalZJSONBorrow([]byte(`{"x":1,"y":2}`))
		} else {
			err = index.UnmarshalZJSON([]byte(`{"x":1,"y":2}`))
		}
		if err != nil || !slices.Equal(lengths, []int{0, 1}) {
			t.Fatalf("root map callback lengths=%v err=%v", lengths, err)
		}
	}
}

func TestCallbackSeesSliceProgress(t *testing.T) {
	defer func() { valueCallback = nil }()
	observe := func(decode func([]byte, *CallbackView) error) ([]int, []Observed) {
		var v CallbackView
		var lengths []int
		valueCallback = func() { lengths = append(lengths, len(v.Values)) }
		if err := decode([]byte(`{"Values":[1,2]}`), &v); err != nil {
			t.Fatal(err)
		}
		return lengths, v.Values
	}
	want, wantValues := observe(func(b []byte, v *CallbackView) error { return json.Unmarshal(b, v) })
	if !slices.Equal(want, []int{1, 2}) {
		t.Fatalf("unexpected oracle callback lengths: %v", want)
	}
	for _, pooled := range []bool{false, true} {
		for _, borrow := range []bool{false, true} {
			t.Run(fmt.Sprintf("pooled=%v/borrow=%v", pooled, borrow), func(t *testing.T) {
				var d CallbackViewDecoder
				got, values := observe(func(b []byte, v *CallbackView) error {
					if pooled {
						if borrow {
							return d.DecodeBorrow(b, v)
						}
						return d.Decode(b, v)
					}
					if borrow {
						return v.UnmarshalZJSONBorrow(b)
					}
					return v.UnmarshalZJSON(b)
				})
				if !slices.Equal(values, wantValues) {
					t.Fatalf("final values differ: %v/%v", values, wantValues)
				}
				if !slices.Equal(got, want) {
					t.Errorf("callback-visible slice lengths: got=%v want=%v (final values agree)", got, want)
				}
			})
		}
	}
}

func TestOverlappingSlices(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		for _, borrow := range []bool{false, true} {
			for start := 0; start < 3; start++ {
				for capacity := 1; capacity <= 3-start; capacity++ {
					t.Run(fmt.Sprintf("pool=%v/borrow=%v/start=%d/cap=%d", pooled, borrow, start, capacity), func(t *testing.T) {
						observe := func(decode recordDecoder) ([]int, []int, []int, bool) {
							backing := []int{1, 2, 3, 4}
							var v Record
							callback = func(int) error { v.S = backing[start : start+1 : start+capacity]; v.T = backing[1:3:4]; return nil }
							if err := decode([]byte(`{"H":0,"S":[7,8],"T":[9,10]}`), &v); err != nil {
								t.Fatal(err)
							}
							alias := &v.T[0] == &backing[1]
							return slices.Clone(backing), slices.Clone(v.S), slices.Clone(v.T), alias
						}
						w, ws, wt, wa := observe(func(b []byte, v *Record) error { return json.Unmarshal(b, v) })
						var d RecordDecoder
						g, gs, gt, ga := observe(func(b []byte, v *Record) error {
							if pooled {
								if borrow {
									return d.DecodeBorrow(b, v)
								}
								return d.Decode(b, v)
							}
							if borrow {
								return v.UnmarshalZJSONBorrow(b)
							}
							return v.UnmarshalZJSON(b)
						})
						if !slices.Equal(g, w) || !slices.Equal(gs, ws) || !slices.Equal(gt, wt) || ga != wa {
							t.Fatalf("overlap: backing=%v/%v S=%v/%v T=%v/%v alias=%v/%v", g, w, gs, ws, gt, wt, ga, wa)
						}
					})
				}
			}
		}
	}
}

func TestRootCollectionAliases(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		wantBacking, gotBacking := []int{1, 2, 3, 4}, []int{1, 2, 3, 4}
		want, got := RootSlice(wantBacking[1:3]), RootSlice(gotBacking[1:3])
		if err := json.Unmarshal([]byte(`[7,8]`), &want); err != nil {
			t.Fatal(err)
		}
		var err error
		if borrow {
			err = got.UnmarshalZJSONBorrow([]byte(`[7,8]`))
		} else {
			err = got.UnmarshalZJSON([]byte(`[7,8]`))
		}
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) || !slices.Equal(gotBacking, wantBacking) {
			t.Errorf("root slice alias: value=%v/%v backing=%v/%v", got, want, gotBacking, wantBacking)
		}
		wm, gm := map[string]int{"keep": 1}, map[string]int{"keep": 1}
		wr, gr := RootMap(wm), RootMap(gm)
		if err := json.Unmarshal([]byte(`{"add":2}`), &wr); err != nil {
			t.Fatal(err)
		}
		if borrow {
			err = gr.UnmarshalZJSONBorrow([]byte(`{"add":2}`))
		} else {
			err = gr.UnmarshalZJSON([]byte(`{"add":2}`))
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(gm) != len(wm) || gm["keep"] != wm["keep"] || gm["add"] != wm["add"] {
			t.Fatal("root map lost caller alias")
		}
	}
}

func TestRootSlicePoolDoesNotAdoptCallerStorage(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		var d RootSliceDecoder
		backing := []int{1, 2, 3, 4}
		v := RootSlice(backing[1:3])
		decode := func(input string) error {
			if borrow {
				return d.DecodeBorrow([]byte(input), &v)
			}
			return d.Decode([]byte(input), &v)
		}
		for _, input := range []string{`[7,8]`, `[9]`, `null`, `[]`, `[10,11,12]`} {
			if err := decode(input); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(backing, []int{1, 2, 3, 4}) {
				t.Fatal("root pool overwrote caller storage")
			}
		}
	}
}

func TestCallbackReplacesNestedPointer(t *testing.T) {
	observe := func(decode recordDecoder) []int {
		var v Record
		var detached *Nested
		callback = func(int) error { detached = v.Inner; n := 9; v.Inner = &Nested{P: &n}; return nil }
		if err := decode([]byte(`{"Inner":{"P":1,"H":0,"P":2},"P":3}`), &v); err != nil {
			t.Fatal(err)
		}
		return []int{*detached.P, *v.Inner.P, *v.P}
	}
	w := observe(func(b []byte, v *Record) error { return json.Unmarshal(b, v) })
	for _, borrow := range []bool{false, true} {
		g := observe(func(b []byte, v *Record) error {
			if borrow {
				return v.UnmarshalZJSONBorrow(b)
			}
			return v.UnmarshalZJSON(b)
		})
		if !slices.Equal(g, w) {
			t.Fatalf("nested callback replacement: got=%v want=%v", g, w)
		}
	}
}

func TestPooledRecovery(t *testing.T) {
	for _, borrow := range []bool{false, true} {
		var d RecordDecoder
		var v Record
		decode := func(b []byte) error {
			if borrow {
				return d.DecodeBorrow(b, &v)
			}
			return d.Decode(b, &v)
		}
		sentinel := errors.New("challenge failure")
		for _, panics := range []bool{false, true} {
			callback = func(int) error {
				if panics {
					panic(sentinel)
				}
				return sentinel
			}
			func() {
				defer func() {
					p := recover()
					if panics && p != sentinel {
						t.Errorf("panic=%v", p)
					}
					if !panics && p != nil {
						t.Errorf("unexpected panic=%v", p)
					}
				}()
				err := decode([]byte(`{"H":0}`))
				if panics {
					t.Error("callback did not panic")
				} else if !errors.Is(err, sentinel) {
					t.Errorf("callback error=%v", err)
				}
			}()
			callback = nil
			if err := decode([]byte(`{"P":7,"S":[8,9]}`)); err != nil {
				t.Fatal(err)
			}
			if v.P == nil || *v.P != 7 || !slices.Equal(v.S, []int{8, 9}) {
				t.Fatal("decoder failed recovery")
			}
		}
	}
}
