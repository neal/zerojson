package compat

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mailru/easyjson"
)

func TestStdlibRoutesThroughZJSON(t *testing.T) {
	p := Point{X: 3, Y: -4, Name: "a<b>&c"}

	want, err := p.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	// MarshalJSON is a value-receiver method: encoding/json only invokes a
	// pointer-receiver Marshaler for addressable values, so a *Point method
	// would be silently skipped (falling back to default struct encoding) for
	// a top-level value or a map value. The value receiver must fire in every
	// context. Each of these routes through zerojson and must equal AppendZJSON.
	if _, ok := any(p).(json.Marshaler); !ok {
		t.Fatal("Point value does not implement json.Marshaler (MarshalJSON must be a value receiver)")
	}
	if _, ok := any(&p).(json.Marshaler); !ok {
		t.Fatal("*Point does not implement json.Marshaler")
	}

	cases := map[string][]byte{}
	for _, c := range []struct {
		name string
		v    any
		want []byte
	}{
		{"top-level value", p, want},
		{"top-level pointer", &p, want},
		{"slice element", []Point{p}, []byte("[" + string(want) + "]")},
		{"array element", [1]Point{p}, []byte("[" + string(want) + "]")},
		{"map value", map[string]Point{"k": p}, []byte(`{"k":` + string(want) + "}")},
		{"struct field (parent by value)", struct {
			F Point `json:"F"`
		}{p}, []byte(`{"F":` + string(want) + "}")},
	} {
		got, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		cases[c.name] = got
		if !bytes.Equal(got, c.want) {
			t.Fatalf("%s: json.Marshal routed around zerojson\n got: %s\nwant: %s", c.name, got, c.want)
		}
	}

	// Round-trip via json.Unmarshal (routes to UnmarshalZJSON on *Point).
	var back Point
	if err := json.Unmarshal(cases["top-level value"], &back); err != nil {
		t.Fatal(err)
	}
	if back != p {
		t.Fatalf("stdlib round-trip: got %+v want %+v", back, p)
	}
}

func TestEasyjsonRoutesThroughZJSON(t *testing.T) {
	p := Point{X: 7, Name: "héllo"}

	data, err := easyjson.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Point
	if err := easyjson.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("easyjson round-trip: got %+v want %+v", got, p)
	}
}
