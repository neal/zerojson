package p

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Observers retain the actual references, not snapshots of their values. A
// later write through a stale arena cell is visible at the final comparison.
type publicationObservation struct {
	retained  []any
	events    []int
	snapshots [][]publicationValue
}

// Values observed by a callback must not change when retained references do.
// These shapes contain only integer leaves, so copy explicitly rather than
// invoking custom marshaling methods or sharing the campaign's decoder.
type publicationValue struct {
	Nil    bool
	Number int64
	Map    map[string]int64
	Slice  []int64
}

func publicationSnapshot(r reflect.Value) []publicationValue {
	out := make([]publicationValue, 0, 6)
	for _, name := range []string{"P", "Q", "M", "N", "S", "T"} {
		f := r.FieldByName(name)
		v := publicationValue{Nil: f.IsNil()}
		if !v.Nil {
			switch f.Kind() {
			case reflect.Pointer:
				v.Number = f.Elem().Int()
			case reflect.Map:
				v.Map = make(map[string]int64, f.Len())
				it := f.MapRange()
				for it.Next() {
					v.Map[it.Key().String()] = it.Value().Int()
				}
			case reflect.Slice:
				v.Slice = make([]int64, f.Len())
				for i := range v.Slice {
					v.Slice[i] = f.Index(i).Int()
				}
			}
		}
		out = append(out, v)
	}
	return out
}

func publicationFields[T any](v *T, access string) reflect.Value {
	r := reflect.ValueOf(v).Elem()
	if access != "" {
		r = r.FieldByName(access)
	}
	return r
}

func publicationCapture(r reflect.Value, o *publicationObservation) {
	o.snapshots = append(o.snapshots, publicationSnapshot(r))
	for _, name := range []string{"P", "Q", "M", "N", "S", "T"} {
		o.retained = append(o.retained, r.FieldByName(name).Interface())
	}
}

func publicationTopology(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	identity := func(x, y any) bool {
		u, v := reflect.ValueOf(x), reflect.ValueOf(y)
		if !u.IsValid() || !v.IsValid() || u.Type() != v.Type() {
			return false
		}
		switch u.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice:
			return !u.IsNil() && !v.IsNil() && u.Pointer() == v.Pointer()
		}
		return false
	}
	for i := range a {
		for j := range a {
			if identity(a[i], a[j]) != identity(b[i], b[j]) {
				return false
			}
		}
	}
	return true
}

func runPublicationCampaign[T any](t *testing.T, program []byte, access, key string, text bool, decode func([]byte, *T, bool, bool) error) {
	t.Helper()
	if len(program) == 0 {
		return
	}
	if len(program) > 65 {
		program = program[:65]
	}
	pooled, borrow := program[0]&1 != 0, program[0]&2 != 0
	var std, zj T
	var so, zo publicationObservation
	wrap := func(member string) string {
		if access != "" {
			return fmt.Sprintf("%q:{%s}", key, member)
		}
		return member
	}
	leaf := func(n byte) string {
		s := fmt.Sprint(int(n))
		if text {
			s = fmt.Sprintf("%q", s)
		}
		return s
	}
	install := func(v *T, o *publicationObservation) {
		publicationReceiver = func(p any) {
			o.events = append(o.events, 100)
			o.retained = append(o.retained, p)
			o.snapshots = append(o.snapshots, publicationSnapshot(publicationFields(v, access)))
		}
		publicationCallback = func(action int) error {
			o.events = append(o.events, action)
			r := publicationFields(v, access)
			publicationCapture(r, o)
			switch action {
			case 1: // Replace with newly allocated, non-nil storage.
				for _, name := range []string{"P", "Q", "M", "N", "S", "T"} {
					f := r.FieldByName(name)
					switch f.Kind() {
					case reflect.Pointer:
						f.Set(reflect.New(f.Type().Elem()))
					case reflect.Map:
						f.Set(reflect.MakeMap(f.Type()))
					case reflect.Slice:
						f.Set(reflect.MakeSlice(f.Type(), 1, 1))
					}
				}
			case 2: // Create aliases during decoding, not only at initialization.
				for _, pair := range [][2]string{{"Q", "P"}, {"N", "M"}, {"T", "S"}} {
					r.FieldByName(pair[0]).Set(r.FieldByName(pair[1]))
				}
			case 3:
				return fmt.Errorf("publication callback refused")
			}
			return nil
		}
	}
	defer func() { publicationReceiver = nil; publicationCallback = nil }()
	// Eight actions per document permit duplicate/replacement interactions;
	// multiple documents exercise caller reuse and reset-first decoding.
	for start := 1; start < len(program); start += 16 {
		if pooled {
			std = *new(T)
			so = publicationObservation{}
			zo = publicationObservation{}
		}
		var members []string
		fault := false
		end := start + 16
		if end > len(program) {
			end = len(program)
		}
		for i := start; i < end; i += 2 {
			op := program[i] % 16
			n := byte(i)
			if i+1 < end {
				n = program[i+1]
			}
			switch op {
			case 0, 1:
				name := "p"
				if op == 1 {
					name = "q"
				}
				members = append(members, wrap(fmt.Sprintf("%q:%s", name, leaf(n))))
			case 2:
				members = append(members, wrap(`"p":null,"q":null`))
			case 3, 4:
				name := "m"
				if op == 4 {
					name = "n"
				}
				members = append(members, wrap(fmt.Sprintf("%q:{\"x\":%s}", name, leaf(n))))
			case 5:
				members = append(members, wrap(`"m":null,"n":null`))
			case 6, 7:
				name := "s"
				if op == 7 {
					name = "t"
				}
				members = append(members, wrap(fmt.Sprintf("%q:[%s]", name, leaf(n))))
			case 8:
				members = append(members, wrap(`"s":null,"t":null`))
			case 9, 10, 11:
				members = append(members, fmt.Sprintf(`"hook":%d`, op-9))
			case 12:
				members, fault = []string{`"hook":3`}, true
			case 13:
				members, fault = []string{`"hook":[`}, true
			case 14:
				members, fault = []string{wrap(`"p":{}`)}, true
			case 15:
				members = append(members, `"unknown":{"escaped":"}\\\"{"}`)
			}
			if fault {
				break
			}
		}
		data := []byte("{" + strings.Join(members, ",") + "}")
		install(&std, &so)
		stdInput := bytes.Clone(data)
		se := json.Unmarshal(stdInput, &std)
		if !bytes.Equal(stdInput, data) {
			t.Fatal("stdlib mutated campaign input")
		}
		install(&zj, &zo)
		zjInput := bytes.Clone(data)
		ze := decode(zjInput, &zj, pooled, borrow)
		if !bytes.Equal(zjInput, data) {
			t.Fatal("zerojson mutated campaign input")
		}
		if fault {
			if se == nil || ze == nil || !reflect.DeepEqual(so.events, zo.events) {
				t.Fatalf("publication error mismatch: program=%x document=%s errors=%v/%v events=%v/%v", program, data, se, ze, so.events, zo.events)
			}
			// Failed-decode partial values are not promised to match stdlib.
			// Check errors and callbacks, then start a fresh independent pair.
			std, zj = *new(T), *new(T)
			so, zo = publicationObservation{}, publicationObservation{}
			continue
		}
		publicationCapture(publicationFields(&std, access), &so)
		publicationCapture(publicationFields(&zj, access), &zo)
		if !reflect.DeepEqual(so.snapshots, zo.snapshots) {
			t.Fatalf("publication campaign mismatch: callback snapshots: program=%x document=%s stdlib=%+v zerojson=%+v", program, data, so.snapshots, zo.snapshots)
		}
		if se != nil || ze != nil || !reflect.DeepEqual(std, zj) || !reflect.DeepEqual(so, zo) || !publicationTopology(so.retained, zo.retained) {
			t.Fatalf("publication campaign mismatch: program=%x document=%s errors=%v/%v events=%v/%v retained=%v/%v", program, data, se, ze, so.events, zo.events, so.retained, zo.retained)
		}
	}
}

func TestPublicationCampaign(t *testing.T) {
	for shape, run := range publicationRunners {
		t.Run(fmt.Sprint(shape), func(t *testing.T) {
			for mode := byte(0); mode < 4; mode++ {
				for a := byte(0); a < 16; a++ {
					for b := byte(0); b < 16; b++ {
						run(t, []byte{mode, 0, 1, 3, 1, 6, 1, 9, 0, a, 2, b, 3, 9, 0})
					}
				}
			}
		})
	}
}

func TestPublicationObservers(t *testing.T) {
	// Calibrate callback-time snapshots separately from live-reference checks.
	type fields struct {
		P, Q *int
		M, N map[string]int
		S, T []int
	}
	n := 7
	f := fields{P: &n, M: map[string]int{"x": 8}, S: []int{9}, T: []int{}}
	var observation publicationObservation
	publicationCapture(reflect.ValueOf(f), &observation)
	if len(observation.snapshots) != 1 {
		t.Fatal("callback snapshots missing")
	}
	want := []publicationValue{{Number: 7}, {Nil: true}, {Map: map[string]int64{"x": 8}}, {Nil: true}, {Slice: []int64{9}}, {Slice: []int64{}}}
	n, f.M["x"], f.S[0] = 10, 11, 12
	if !reflect.DeepEqual(observation.snapshots[0], want) {
		t.Fatal("callback snapshot lost original values or nilness")
	}
	publicationCapture(reflect.ValueOf(f), &observation)
	if reflect.DeepEqual(observation.snapshots[0], observation.snapshots[1]) {
		t.Fatal("callback snapshot missed intermediate writes")
	}
	x, y := 1, 1
	if publicationTopology([]any{&x, &x}, []any{&x, &y}) {
		t.Fatal("topology observer ignored equal-valued distinct pointers")
	}
	m := map[string]int{"x": 1}
	s := []int{1}
	a := publicationObservation{retained: []any{m, s}, events: []int{0}}
	b := publicationObservation{retained: []any{map[string]int{"x": 1}, []int{1}}, events: []int{0}}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("observer baseline differs")
	}
	m["x"] = 2
	s[0] = 2
	if reflect.DeepEqual(a, b) {
		t.Fatal("observer copied retained storage")
	}
	b = a
	b.events = []int{1}
	if reflect.DeepEqual(a, b) {
		t.Fatal("observer ignored callback events")
	}
}

func FuzzPublicationCampaign(f *testing.F) {
	for shape := range publicationRunners {
		for mode := byte(0); mode < 4; mode++ {
			f.Add([]byte{byte(shape), mode, 0, 1, 3, 1, 6, 1, 9, 0, 2, 0, 5, 0, 8, 0, 0, 2, 3, 2, 6, 2, 9, 0})
		}
	}
	f.Fuzz(func(t *testing.T, p []byte) {
		if len(p) < 2 {
			return
		}
		publicationRunners[int(p[0])%len(publicationRunners)](t, p[1:])
	})
}
