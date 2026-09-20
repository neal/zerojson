package acceptance

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
)

type shape struct{ length, capacity, alias, pointer, maps int }
type world struct {
	value    Plain
	backing  []int
	bytes    []byte
	header   *[]byte
	retained []Plain
}

// Construct each oracle independently from a descriptor, never from a clone
// of zerojson's output or from the generator's type-classification model.
func makeWorld(s shape) *world {
	a := []int{9, 8, 7, 6, 5, 4}
	b := []int{9, 8, 7, 6, 5, 4}
	raw := []byte{9, 8, 7, 6}
	v := Plain{A: a[:s.length:s.capacity], Bytes: raw[:1], Label: "before"}
	switch s.alias {
	case 0:
		v.B = b[:s.length:s.capacity]
	case 1:
		v.B = a[:s.length:s.capacity]
	case 2:
		v.B = a[1 : 1+s.length : 1+s.capacity]
	}
	switch s.pointer {
	case 1:
		v.P = &a[0]
	case 2:
		v.P = &a[2]
	}
	if s.maps != 0 {
		v.MapA = map[string]int{"keep": 9}
		if s.maps == 1 {
			v.MapB = v.MapA
		} else {
			v.MapB = map[string]int{"keep": 9}
		}
	}
	// Include nested aliases only in one partition. Existing slices still
	// require fallback; reset-first modes also exercise fresh destinations.
	if s.pointer != 0 {
		v.Child = Child{S: a[:1], P: v.P}
		h := raw[:1]
		v.BytePtr = &h
	}
	return &world{value: v, backing: a, bytes: raw, header: v.BytePtr}
}

func pointers(w *world) []*int {
	p := []*int{&w.backing[0], &w.backing[1], &w.backing[2]}
	for _, v := range append(append([]Plain{}, w.retained...), w.value) {
		p = append(p, v.P, v.Child.P)
		for _, s := range [][]int{v.A, v.B, v.Child.S} {
			if len(s) > 0 {
				p = append(p, &s[0])
			} else {
				p = append(p, nil)
			}
		}
	}
	return p
}

func equalWorld(a, b *world) bool {
	if !reflect.DeepEqual(a.value, b.value) || !reflect.DeepEqual(a.backing, b.backing) || !reflect.DeepEqual(a.bytes, b.bytes) || !reflect.DeepEqual(a.header, b.header) || !reflect.DeepEqual(a.retained, b.retained) {
		return false
	}
	x, y := pointers(a), pointers(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		for j := 0; j <= i; j++ {
			if (x[i] == x[j]) != (y[i] == y[j]) {
				return false
			}
		}
	}
	// Pointer-to-slice identity is separate from the slice's backing storage.
	ax := []*[]byte{a.header, a.value.BytePtr}
	by := []*[]byte{b.header, b.value.BytePtr}
	for i := range ax {
		for j := range ax {
			if (ax[i] == ax[j]) != (by[i] == by[j]) {
				return false
			}
		}
	}
	return true
}

var documents = []string{
	`{}`,
	`{"A":[1],"B":[2,3], "Label":"tail"}`,
	`{"Bytes":"aGVsbG8=","Bytes":[null,1,null],"BytePtr":[null,7]}`,
	`{"A":null,"A":[null,3],"B":[],"MapA":{"x":1},"MapB":{"y":2}}`,
	`{"P":3,"Child":{"S":[null,5],"P":4},"A":[null,null,null,null]}`,
	`{"A":[null],"A":[2,3],"MapA":null,"MapB":{}}`,
	`{"A":[1],`,
	`{"A":[1],"P":"bad"}`,
}

func runSequence(t *testing.T, s shape, mode int, ops []int) {
	t.Helper()
	a, b := makeWorld(s), makeWorld(s)
	var d PlainDecoder
	for step, op := range ops {
		a.retained = append(a.retained, a.value)
		b.retained = append(b.retained, b.value)
		input := []byte(documents[op])
		original := string(input)
		var e error
		switch mode {
		case 0:
			e = a.value.UnmarshalZJSON(input)
		case 1:
			e = a.value.UnmarshalZJSONBorrow(input)
		case 2:
			b.value = Plain{}
			e = d.Decode(input, &a.value)
		case 3:
			b.value = Plain{}
			e = d.DecodeBorrow(input, &a.value)
		}
		se := json.Unmarshal([]byte(original), &b.value)
		if string(input) != original {
			t.Fatalf("graph mismatch: input changed shape=%+v mode=%d ops=%v step=%d", s, mode, ops, step)
		}
		if (e == nil) != (se == nil) {
			t.Fatalf("graph mismatch: acceptance shape=%+v mode=%d ops=%v step=%d errors=%v/%v", s, mode, ops, step, e, se)
		}
		if e != nil {
			// Partial semantic-error states need not agree. Recover using a
			// fresh destination but the same decoder; do not bless zerojson's
			// partial output as a new stdlib oracle.
			a, b = makeWorld(s), makeWorld(s)
			continue
		}
		if !equalWorld(a, b) {
			t.Fatalf("graph mismatch: shape=%+v mode=%d ops=%v step=%d got=%+v backing=%v want=%+v backing=%v", s, mode, ops, step, a.value, a.backing, b.value, b.backing)
		}
		if mode == 0 || mode == 2 {
			for i := range input {
				input[i] = 0xff
			}
			if !equalWorld(a, b) {
				t.Fatal("graph mismatch: copied result aliases input")
			}
		}
	}
	// Mutate retained and current references in both worlds. DeepEqual alone
	// cannot distinguish equal-valued independent maps from shared ones.
	for _, mutate := range []func(*world){
		func(w *world) {
			if w.value.MapA != nil {
				w.value.MapA["probe"] = 77
			}
		},
		func(w *world) {
			if len(w.value.A) > 0 {
				w.value.A[0] = 88
			}
		},
		func(w *world) {
			if w.value.P != nil {
				*w.value.P = 99
			}
		},
		func(w *world) {
			if len(w.retained) > 0 && w.retained[0].MapB != nil {
				w.retained[0].MapB["retained"] = 66
			}
		},
		func(w *world) { w.bytes[0] = 55; w.backing[0] = 44 },
	} {
		mutate(a)
		mutate(b)
		if !equalWorld(a, b) {
			t.Fatalf("graph mismatch: mutation shape=%+v mode=%d ops=%v", s, mode, ops)
		}
	}
}

func TestGraphMatrix(t *testing.T) {
	count := 0
	for capacity := 1; capacity <= 3; capacity++ {
		for length := 0; length <= capacity; length++ {
			for alias := 0; alias < 4; alias++ {
				for pointer := 0; pointer < 3; pointer++ {
					for maps := 0; maps < 3; maps++ {
						s := shape{length, capacity, alias, pointer, maps}
						for first := range documents {
							for second := range documents {
								for mode := 0; mode < 4; mode++ {
									runSequence(t, s, mode, []int{first, second, 0})
									count++
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("graph matrix: %d three-operation sequences", count)
}

func TestGraphRandomSequences(t *testing.T) {
	seed := int64(0x32299c5)
	if raw := os.Getenv("ZEROJSON_GRAPH_SEED"); raw != "" {
		v, e := strconv.ParseInt(raw, 0, 64)
		if e != nil {
			t.Fatal(e)
		}
		seed = v
	}
	count := 500
	if raw := os.Getenv("ZEROJSON_GRAPH_RANDOM"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 100000 {
			t.Fatal("invalid random count")
		}
		count = v
	}
	r := rand.New(rand.NewSource(seed))
	t.Logf("random seed=%d sequences=%d", seed, count)
	for i := 0; i < count; i++ {
		capacity := 1 + r.Intn(3)
		s := shape{r.Intn(capacity + 1), capacity, r.Intn(4), r.Intn(3), r.Intn(3)}
		ops := make([]int, 8)
		for j := range ops {
			ops[j] = r.Intn(len(documents))
		}
		runSequence(t, s, r.Intn(4), ops)
	}
}

func TestObserverCalibration(t *testing.T) {
	s := shape{1, 1, 1, 1, 1}
	a, b := makeWorld(s), makeWorld(s)
	if !equalWorld(a, b) {
		t.Fatal("observer rejects equal graphs")
	}
	b.value.B = append([]int(nil), b.value.B...)
	if equalWorld(a, b) {
		t.Fatal("observer missed slice topology")
	}
	a, b = makeWorld(s), makeWorld(s)
	b.value.P = new(int)
	*b.value.P = *a.value.P
	if equalWorld(a, b) {
		t.Fatal("observer missed pointer identity")
	}
	a, b = makeWorld(s), makeWorld(s)
	b.value.MapB = map[string]int{"keep": 9}
	a.value.MapA["probe"] = 1
	b.value.MapA["probe"] = 1
	if equalWorld(a, b) {
		t.Fatal("observer missed map sharing")
	}
}

func TestCallbackTraces(t *testing.T) {
	for _, s := range []shape{{1, 1, 1, 1, 1}, {0, 3, 2, 2, 2}, {0, 1, 3, 0, 0}} {
		for mode := 0; mode < 4; mode++ {
			for _, doc := range documents {
				a, b := makeWorld(s), makeWorld(s)
				got, want := Observed{State: a.value}, Observed{State: b.value}
				var d ObservedDecoder
				input := []byte(`{"Before":1,"State":` + doc + `, "After":2,"Before":3}`)
				trace := func(v *Observed) func() {
					return func() {
						raw, e := json.Marshal(v)
						if e != nil {
							t.Fatal(e)
						}
						events = append(events, string(raw))
					}
				}
				events = nil
				observe = trace(&got)
				var e error
				switch mode {
				case 0:
					e = got.UnmarshalZJSON(input)
				case 1:
					e = got.UnmarshalZJSONBorrow(input)
				case 2:
					want = Observed{}
					e = d.Decode(input, &got)
				case 3:
					want = Observed{}
					e = d.DecodeBorrow(input, &got)
				}
				actual := append([]string(nil), events...)
				events = nil
				observe = trace(&want)
				se := json.Unmarshal(append([]byte(nil), input...), &want)
				observe = nil
				// Successful callbacks must match exactly. Syntax failures must
				// invoke none. Later callbacks after semantic errors are not part
				// of the failed-output compatibility contract.
				compareTrace := e == nil || !json.Valid(input)
				if !json.Valid(input) && (len(actual) != 0 || len(events) != 0) {
					t.Fatal("callback mismatch: malformed document invoked user code")
				}
				if (e == nil) != (se == nil) || (compareTrace && !reflect.DeepEqual(actual, events)) || (e == nil && !reflect.DeepEqual(got, want)) {
					t.Fatalf("callback mismatch: shape=%+v mode=%d doc=%s trace=%v/%v errors=%v/%v", s, mode, doc, actual, events, e, se)
				}
			}
		}
	}
}

var events []string
