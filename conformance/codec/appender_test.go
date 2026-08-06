package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// TestCentsAppendZJSONMatchesMarshalJSON checks Cents's hand-written
// AppendZJSON (the Appender fast path — see zerojson's appender.go)
// against its own MarshalJSON, byte for byte, across boundary values.
func TestCentsAppendZJSONMatchesMarshalJSON(t *testing.T) {
	cases := []Cents{0, 1, 5, 99, 100, 101, 12345, -1, -5, -100, -12345, 1<<31 - 1}
	for _, c := range cases {
		want, err := c.MarshalJSON()
		if err != nil {
			t.Fatalf("%d: MarshalJSON error: %v", c, err)
		}
		got, err := c.AppendZJSON(nil)
		if err != nil {
			t.Fatalf("%d: AppendZJSON error: %v", c, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%d: MarshalJSON=%s AppendZJSON=%s", c, want, got)
		}
		// AppendZJSON must append, not reset: a non-empty prefix survives.
		got2, _ := c.AppendZJSON([]byte("PREFIX"))
		if !bytes.Equal(append([]byte("PREFIX"), want...), got2) {
			t.Errorf("%d: AppendZJSON did not append to an existing prefix: %s", c, got2)
		}
	}
}

// TestCentsRoundTrip checks ReadZJSON against MarshalJSON's own output
// (ReadZJSON's contract only needs to parse what AppendZJSON/MarshalJSON
// produce, since that's all the -tests fuzzer's canon step ever feeds it —
// see the generated FuzzAppenderCasesZJSON for the full differential
// argument) and separately round-trips through UnmarshalJSON.
func TestCentsRoundTrip(t *testing.T) {
	cases := []Cents{0, 1, 5, 99, 100, 101, 12345, -1, -5, -100, -12345}
	for _, c := range cases {
		enc, err := c.MarshalJSON()
		if err != nil {
			t.Fatalf("%d: MarshalJSON error: %v", c, err)
		}

		var viaJSON Cents
		if err := viaJSON.UnmarshalJSON(enc); err != nil {
			t.Fatalf("%d: UnmarshalJSON(%s) error: %v", c, enc, err)
		}
		if viaJSON != c {
			t.Errorf("%d: UnmarshalJSON round-trip got %d", c, viaJSON)
		}

		var ac AppenderCases
		payload := []byte(`{"amount":` + string(enc) + `}`)
		if err := ac.UnmarshalZJSON(payload); err != nil {
			t.Fatalf("%d: UnmarshalZJSON(%s) error: %v", c, payload, err)
		}
		if ac.Amount != c {
			t.Errorf("%d: ReadZJSON round-trip (via AppenderCases.Amount) got %d", c, ac.Amount)
		}
	}
}

// TestAppenderCasesEncodeDecode is a whole-struct byte-identity and
// round-trip check: the Appender value field (Amount), the Appender
// pointer field (Cost, both set and nil — null clears it like every other
// pointer kind), encoded with zerojson and decoded back with both
// zerojson and encoding/json (Cents's MarshalJSON/UnmarshalJSON oracle).
func TestAppenderCasesEncodeDecode(t *testing.T) {
	cost := Cents(250)
	cases := []AppenderCases{
		{Amount: 12345, Cost: &cost},
		{Amount: 0, Cost: nil},
		{Amount: -500, Cost: &cost},
	}
	for _, c := range cases {
		want, err := json.Marshal(&c)
		if err != nil {
			t.Fatalf("%+v: json.Marshal error: %v", c, err)
		}
		got, err := c.AppendZJSON(nil)
		if err != nil {
			t.Fatalf("%+v: AppendZJSON error: %v", c, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%+v: json.Marshal=%s AppendZJSON=%s", c, want, got)
		}

		var viaZJ AppenderCases
		if err := viaZJ.UnmarshalZJSON(got); err != nil {
			t.Fatalf("%+v: UnmarshalZJSON(%s) error: %v", c, got, err)
		}
		if !reflect.DeepEqual(c, viaZJ) {
			t.Errorf("%+v: round-trip mismatch: got %+v", c, viaZJ)
		}

		var viaStd AppenderCases
		if err := json.Unmarshal(got, &viaStd); err != nil {
			t.Fatalf("%+v: stdlib rejected zerojson output %s: %v", c, got, err)
		}
		if !reflect.DeepEqual(c, viaStd) {
			t.Errorf("%+v: stdlib decode of zerojson output mismatch: got %+v", c, viaStd)
		}
	}
}

// TestAppenderCasesEncodeAllocs shows the Appender fast path doesn't
// allocate on encode: AppendZJSON on a value whose only non-scalar field
// is the Cost pointer (already allocated outside the timed region).
func TestAppenderCasesEncodeAllocs(t *testing.T) {
	cost := Cents(250)
	v := AppenderCases{Amount: 12345, Cost: &cost}
	buf := make([]byte, 0, 64)
	allocs := testing.AllocsPerRun(1000, func() {
		var err error
		buf, err = v.AppendZJSON(buf[:0])
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("AppendZJSON: want 0 allocs, got %v", allocs)
	}
}
