package compat

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mailru/easyjson"
	"github.com/mailru/easyjson/jlexer"
)

// trailing_test.go covers the compat/compat-easyjson wrappers' trailing-
// data behavior: json.Unmarshal already gets this for free by routing
// through UnmarshalJSON -> UnmarshalZJSON (which now calls Lexer.Finish);
// easyjson.Unmarshal needed its own fix (UnmarshalEasyJSON now calls
// jlexer.Lexer.Consumed when — and only when — this call is genuinely the
// top-level entry point, guarded by IsStart(), matching easyjson's own
// generated decoders — see compat.go's genCompat and its doc comment).
func TestCompatTrailingDataRejected(t *testing.T) {
	p := Point{X: 3, Y: -4, Name: "a<b>&c"}
	base, err := p.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"value_only", base, false},
		{"value_plus_garbage", append(bytes.Clone(base), "garbage"...), true},
		{"value_plus_whitespace", append(bytes.Clone(base), " \t\n\r"...), false},
		{"value_plus_second_value", append(bytes.Clone(base), base...), true},
		{"empty_input", []byte{}, true},
		{"whitespace_only", []byte("  \n"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var viaStd Point
			if err := json.Unmarshal(tc.data, &viaStd); (err != nil) != tc.wantErr {
				t.Errorf("json.Unmarshal: err=%v, want error=%v (input %q)", err, tc.wantErr, tc.data)
			}
			var viaEJ Point
			if err := easyjson.Unmarshal(tc.data, &viaEJ); (err != nil) != tc.wantErr {
				t.Errorf("easyjson.Unmarshal: err=%v, want error=%v (input %q)", err, tc.wantErr, tc.data)
			}
		})
	}
}

// TestCompatEasyjsonNestedFieldNotFalselyRejected proves the IsStart()
// guard in the generated UnmarshalEasyJSON actually matters: it must NOT
// call jlexer.Lexer.Consumed on the *outer* lexer when Point is decoded
// as an ordinary nested field partway through a larger document, since
// that would misread the parent's own remaining bytes (its next field,
// its closing brace) as trailing garbage. This drives a jlexer.Lexer
// directly, the way a real easyjson-generated parent struct decoder
// would (see easyjson's gen/decoder.go: Delim('{')/UnsafeFieldName/
// WantColon/<field decode>/WantComma per member), rather than going
// through encoding/json (which never calls UnmarshalEasyJSON at all) or
// easyjson.Unmarshal (which only ever calls it at the top level).
func TestCompatEasyjsonNestedFieldNotFalselyRejected(t *testing.T) {
	data := []byte(`{"pt":{"x":1,"y":2},"tag":"after"}`)
	l := &jlexer.Lexer{Data: data}

	l.Delim('{')
	if key := l.UnsafeFieldName(false); key != "pt" {
		t.Fatalf("unexpected key %q", key)
	}
	l.WantColon()

	var pt Point
	pt.UnmarshalEasyJSON(l) // nested call: l is NOT at the start of data
	if l.Error() != nil {
		t.Fatalf("Point.UnmarshalEasyJSON (nested, mid-document): %v", l.Error())
	}
	if pt.X != 1 || pt.Y != 2 {
		t.Fatalf("unexpected Point: %+v", pt)
	}

	l.WantComma()
	if key := l.UnsafeFieldName(false); key != "tag" {
		t.Fatalf("sibling field after nested Point decode: got key %q, want \"tag\" (a false Consumed() rejection would surface here)", key)
	}
	l.WantColon()
	if tag := l.String(); tag != "after" {
		t.Fatalf("unexpected tag value %q", tag)
	}
	if l.Error() != nil {
		t.Fatalf("parent document rejected after nested Point decode: %v", l.Error())
	}
}
