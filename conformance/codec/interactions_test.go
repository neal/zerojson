package codec

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestOmitZeroUsesIsZeroForEveryNamedShape(t *testing.T) {
	zeroTime := time.Time{}
	cases := []InteractionOmitZero{
		{When: &zeroTime, Both: &zeroTime, Odd: 42, Items: OddSlice{}},
		{Odd: 0},
	}
	for _, v := range cases {
		want, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
		}
	}
}

func TestMapValuePointerOnlyMarshalerAddressability(t *testing.T) {
	v := MapPointerMethods{
		Values:  map[string]PointerOnlyJSON{"x": {Value: 1}},
		Numbers: map[string]PointerOnlyQuoted{"x": 7},
	}
	want, err := json.Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
	}
	var stdDecoded, zjDecoded MapPointerMethods
	if err := json.Unmarshal(got, &stdDecoded); err != nil {
		t.Fatal(err)
	}
	if err := zjDecoded.UnmarshalZJSON(got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zjDecoded, stdDecoded) {
		t.Fatalf("decode mismatch\nstdlib:   %#v\nzerojson: %#v", stdDecoded, zjDecoded)
	}

	rootCases := []struct {
		name string
		std  any
		zj   interface{ AppendZJSON([]byte) ([]byte, error) }
	}{
		{"struct", PointerMethodIndex{"x": {Value: 1}}, func() *PointerMethodIndex { v := PointerMethodIndex{"x": {Value: 1}}; return &v }()},
		{"quoted", PointerQuotedIndex{"x": 7}, func() *PointerQuotedIndex { v := PointerQuotedIndex{"x": 7}; return &v }()},
	}
	for _, tc := range rootCases {
		t.Run("root/"+tc.name, func(t *testing.T) {
			want, err := json.Marshal(tc.std)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.zj.AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
			}
		})
	}

	{
		in := []byte(`{"x":{"value":1}}`)
		var std PointerMethodIndex
		var zj PointerMethodIndex
		if err := json.Unmarshal(in, &std); err != nil {
			t.Fatal(err)
		}
		if err := zj.UnmarshalZJSON(in); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(zj, std) {
			t.Fatalf("root decode mismatch: std=%#v zerojson=%#v", std, zj)
		}
	}
	{
		in := []byte(`{"x":7}`)
		var std PointerQuotedIndex
		var zj PointerQuotedIndex
		if err := json.Unmarshal(in, &std); err != nil {
			t.Fatal(err)
		}
		if err := zj.UnmarshalZJSON(in); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(zj, std) {
			t.Fatalf("root quoted decode mismatch: std=%#v zerojson=%#v", std, zj)
		}
	}
}

func TestStringOptionRequiresQuotedInput(t *testing.T) {
	in := []byte(`{"count":1,"amount":2}`)
	var std, zj StringOptCases
	stdErr := json.Unmarshal(in, &std)
	zjErr := zj.UnmarshalZJSON(in)
	if stdErr == nil || zjErr == nil {
		t.Fatalf("bare ,string values must fail: std=%v zerojson=%v", stdErr, zjErr)
	}
}

func TestStringOptionAcceptsEscapedDigits(t *testing.T) {
	in := []byte(`{"count":"\u0031","amount":"\u002d2"}`)
	var std, zj StringOptCases
	if err := json.Unmarshal(in, &std); err != nil {
		t.Fatal(err)
	}
	if err := zj.UnmarshalZJSON(in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zj, std) {
		t.Fatalf("escaped ,string mismatch: std=%+v zerojson=%+v", std, zj)
	}
}

func TestInvalidUTF8DecodeMatchesStdlib(t *testing.T) {
	stringInput := append([]byte(`{"origin":"`), 0xff)
	stringInput = append(stringInput, []byte(`"}`)...)
	var stdEvent, zjEvent Event
	if err := json.Unmarshal(stringInput, &stdEvent); err != nil {
		t.Fatal(err)
	}
	if err := zjEvent.UnmarshalZJSON(stringInput); err != nil {
		t.Fatal(err)
	}
	if zjEvent.Origin != stdEvent.Origin {
		t.Fatalf("string mismatch: std=%q (% x) zerojson=%q (% x)", stdEvent.Origin, []byte(stdEvent.Origin), zjEvent.Origin, []byte(zjEvent.Origin))
	}

	mapInput := append([]byte(`{"bag":{"`), 0xff)
	mapInput = append(mapInput, []byte(`":1}}`)...)
	stdEvent, zjEvent = Event{}, Event{}
	if err := json.Unmarshal(mapInput, &stdEvent); err != nil {
		t.Fatal(err)
	}
	if err := zjEvent.UnmarshalZJSON(mapInput); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zjEvent.Bag, stdEvent.Bag) {
		t.Fatalf("map-key mismatch: std=%#v zerojson=%#v", stdEvent.Bag, zjEvent.Bag)
	}
}

func TestReusedTargetAndDuplicateCompositeSemantics(t *testing.T) {
	reuseInput := []byte(`{"values":{"new":2},"inner":{"a":3},"accum":4}`)
	std := ReuseSemantics{Values: map[string]int{"old": 1}, Inner: &ReuseInner{A: 1, B: 2}, Accum: Accumulator{9}}
	zj := ReuseSemantics{Values: map[string]int{"old": 1}, Inner: &ReuseInner{A: 1, B: 2}, Accum: Accumulator{9}}
	if err := json.Unmarshal(reuseInput, &std); err != nil {
		t.Fatal(err)
	}
	if err := zj.UnmarshalZJSON(reuseInput); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zj, std) {
		t.Fatalf("reused-target mismatch\nstdlib:   %#v\nzerojson: %#v", std, zj)
	}

	dupInput := []byte(`{"values":{"a":1},"values":{"b":2},"accum":1,"accum":2}`)
	std, zj = ReuseSemantics{}, ReuseSemantics{}
	if err := json.Unmarshal(dupInput, &std); err != nil {
		t.Fatal(err)
	}
	if err := zj.UnmarshalZJSON(dupInput); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zj, std) {
		t.Fatalf("duplicate-key mismatch\nstdlib:   %#v\nzerojson: %#v", std, zj)
	}
}

func TestOrderedFallbackDoesNotReplayCallbacks(t *testing.T) {
	for _, doc := range []string{
		`{"first":1,"last" :2}`,            // a key deviation: the keyed loop carries on
		`{"first":1,"inner":{ },"last":2}`, // a nested object the ordered path cannot match restarts it
	} {
		in := []byte(doc)
		sideEffectCalls = 0
		var std OrderedSideEffects
		if err := json.Unmarshal(in, &std); err != nil {
			t.Fatal(err)
		}
		wantCalls := sideEffectCalls
		sideEffectCalls = 0
		var zj OrderedSideEffects
		if err := zj.UnmarshalZJSON(in); err != nil {
			t.Fatal(err)
		}
		if sideEffectCalls != wantCalls {
			t.Fatalf("%s: callback count: stdlib=%d zerojson=%d", doc, wantCalls, sideEffectCalls)
		}
	}
}

func TestPooledAppenderPointerDoesNotLeakState(t *testing.T) {
	var d PoolAppenderHolderDecoder
	var first, second PoolAppenderHolder
	if err := d.Decode([]byte(`{"value":1}`), &first); err != nil {
		t.Fatal(err)
	}
	if got := append(PoolAccumulator(nil), (*first.Value)...); !reflect.DeepEqual(got, PoolAccumulator{1}) {
		t.Fatalf("first decode: %v", got)
	}
	if err := d.Decode([]byte(`{"value":2}`), &second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*second.Value, PoolAccumulator{2}) {
		t.Fatalf("second decode leaked state: %v", *second.Value)
	}
}
