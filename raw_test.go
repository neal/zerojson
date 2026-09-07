package zerojson

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRawUnmarshalDetachesBorrowedStorage(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		input := []byte(`{"long_field":1234567890}`)
		original := bytes.Clone(input)
		l := NewLexer(input, true)
		var raw Raw
		ReadRaw(&l, &raw)
		l.Finish()
		if l.Err() != nil || &raw[0] != &input[0] {
			t.Fatal("test must begin with borrowed storage")
		}
		var err error
		if pointer {
			v := struct {
				R *Raw `json:"r"`
			}{R: &raw}
			err = json.Unmarshal([]byte(`{"r":{"b":2}}`), &v)
		} else {
			v := struct {
				R Raw `json:"r"`
			}{R: raw}
			err = json.Unmarshal([]byte(`{"r":{"b":2}}`), &v)
			raw = v.R
		}
		if err != nil || string(raw) != `{"b":2}` {
			t.Fatalf("replacement=%s err=%v", raw, err)
		}
		if !bytes.Equal(input, original) {
			t.Fatalf("borrowed input overwritten: %s", input)
		}
		raw[0] = '['
		if !bytes.Equal(input, original) {
			t.Fatal("replacement still aliases borrowed input")
		}
	}
}

// TestRawInteropAndChecked pins Raw's encoding/json interoperability and
// the checked append the generated code uses.
func TestRawInteropAndChecked(t *testing.T) {
	var r Raw
	if b, _ := r.MarshalJSON(); string(b) != "null" {
		t.Fatalf("nil Raw MarshalJSON = %s", b)
	}
	if b, _ := (Raw{}).MarshalJSON(); string(b) != "null" {
		t.Fatalf("empty Raw MarshalJSON = %s", b)
	}
	if err := r.UnmarshalJSON([]byte(`{"a":1}`)); err != nil || string(r) != `{"a":1}` {
		t.Fatalf("UnmarshalJSON: %q %v", r, err)
	}
	if out := AppendRaw(nil, &Raw{}); string(out) != "null" {
		t.Fatalf("AppendRaw(empty) = %s, want null", out)
	}
	bad := Raw{'{'}
	if _, err := AppendRawChecked(nil, &bad); err == nil {
		t.Fatal("invalid Raw appended without error")
	}
	valid := Raw(`[1,2]`)
	if out, err := AppendRawChecked(nil, &valid); err != nil || string(out) != "[1,2]" {
		t.Fatalf("valid Raw: %s %v", out, err)
	}
}
