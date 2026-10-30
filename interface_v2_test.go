//go:build go1.27

package zerojson

import (
	"encoding/json"
	"encoding/json/jsontext"
	"testing"
)

// MarshalJSONTo outranks both MarshalJSON and MarshalText in encoding/json
// built on encoding/json/v2; a pointer method counts only through a pointer.
type (
	ifaceJSONTo     struct{ calls *int }
	ifaceJSONToPtr  struct{ calls *int }
	ifaceTextJSONTo struct{ calls *int }
)

func (v ifaceJSONTo) MarshalJSON() ([]byte, error) { *v.calls++; return []byte("1"), nil }
func (ifaceJSONTo) MarshalJSONTo(e *jsontext.Encoder) error {
	return e.WriteToken(jsontext.Float(1.5))
}
func (v ifaceJSONToPtr) MarshalJSON() ([]byte, error) { *v.calls++; return []byte("1"), nil }
func (*ifaceJSONToPtr) MarshalJSONTo(e *jsontext.Encoder) error {
	return e.WriteToken(jsontext.Float(2.5))
}
func (v ifaceTextJSONTo) MarshalText() ([]byte, error) { *v.calls++; return []byte("text"), nil }
func (ifaceTextJSONTo) MarshalJSONTo(e *jsontext.Encoder) error {
	return e.WriteToken(jsontext.String("json to"))
}

// TestAppendInterfaceMethodPrecedence pins AppendInterface to json.Marshal
// for values whose MarshalJSONTo outranks the method its fast paths call:
// the same bytes, and the outranked method not called.
func TestAppendInterfaceMethodPrecedence(t *testing.T) {
	for _, c := range []struct {
		name          string
		v             func(calls *int) any
		outrankedUsed bool // json.Marshal itself calls the method AppendInterface would
	}{
		{"value", func(n *int) any { return ifaceJSONTo{n} }, false},
		{"pointer", func(n *int) any { return &ifaceJSONTo{n} }, false},
		{"pointer method on value", func(n *int) any { return ifaceJSONToPtr{n} }, true},
		{"pointer method on pointer", func(n *int) any { return &ifaceJSONToPtr{n} }, false},
		{"text", func(n *int) any { return ifaceTextJSONTo{n} }, false},
		{"nil pointer", func(*int) any { return (*ifaceJSONTo)(nil) }, false},
	} {
		var stdCalls, zjCalls int
		want, wantErr := json.Marshal(c.v(&stdCalls))
		got, gotErr := AppendInterface(nil, c.v(&zjCalls))
		if string(got) != string(want) || (wantErr == nil) != (gotErr == nil) {
			t.Errorf("%s: json.Marshal %q, %v; AppendInterface %q, %v", c.name, want, wantErr, got, gotErr)
		}
		if !c.outrankedUsed && zjCalls != 0 {
			t.Errorf("%s: AppendInterface called the outranked method %d times", c.name, zjCalls)
		}
	}
}
