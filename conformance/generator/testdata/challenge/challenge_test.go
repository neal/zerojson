package challenge

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
)

type recordDecoder func([]byte, *Record) error

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
