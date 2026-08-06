package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// TestMapFieldsLikeOmitemptyGatesMapMarshaler pins that omitempty gates
// map-shaped custom-Marshaler fields: MapFieldsLike is a struct with
// map-shaped Marshaler fields (GenMap) tagged omitempty. easyjson's own
// generated code gates such a field on `len(...) != 0`, so a nil or
// non-nil-empty map is omitted rather than emitted as `"where":null` or
// `"where":{}`; zerojson must match.
func TestMapFieldsLikeOmitemptyGatesMapMarshaler(t *testing.T) {
	cases := map[string]MapFieldsLike{
		"all nil":       {Op: "INSERT"},
		"set only":      {Op: "UPDATE", Set: GenMap{"a": 1.0}},
		"where only":    {Op: "DELETE", Where: GenMap{"id": "x"}},
		"both set":      {Op: "UPDATE", Set: GenMap{"a": 1.0}, Where: GenMap{"id": "x"}},
		"empty non-nil": {Op: "UPDATE", Set: GenMap{}, Where: GenMap{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(&c)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
			}

			var wantRT, gotRT MapFieldsLike
			if err := json.Unmarshal(want, &wantRT); err != nil {
				t.Fatal(err)
			}
			if err := gotRT.UnmarshalZJSON(got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(&wantRT, &gotRT) {
				t.Fatalf("round-trip mismatch\nstdlib:   %+v\nzerojson: %+v", &wantRT, &gotRT)
			}
		})
	}

	// A nil Set/Where must not appear on the wire at all — not as
	// `"where":null`, and not as an emitted empty object.
	nilBoth := MapFieldsLike{Op: "INSERT"}
	got, err := nilBoth.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("set")) || bytes.Contains(got, []byte("where")) {
		t.Fatalf("nil map-shaped Marshaler field(s) not omitted: %s", got)
	}
}

// TestMapFieldsLikeNullClearsMapMarshaler pins isNullableField's extension to
// map-shaped Marshalers: a JSON null for Set/Where must clear the field
// to nil, exactly as it already did for slice-shaped Marshalers and every
// other reference kind.
func TestMapFieldsLikeNullClearsMapMarshaler(t *testing.T) {
	base := []byte(`{"op":"UPDATE","set":{"a":1},"where":{"id":"x"}}`)
	var got MapFieldsLike
	if err := got.UnmarshalZJSON(base); err != nil {
		t.Fatal(err)
	}
	if got.Set == nil || got.Where == nil {
		t.Fatalf("setup failed: %+v", got)
	}

	if err := got.UnmarshalZJSON([]byte(`{"op":"UPDATE","set":null,"where":null}`)); err != nil {
		t.Fatal(err)
	}
	if got.Set != nil || got.Where != nil {
		t.Fatalf("null did not clear map-shaped Marshaler field(s): Set=%#v Where=%#v", got.Set, got.Where)
	}
}
