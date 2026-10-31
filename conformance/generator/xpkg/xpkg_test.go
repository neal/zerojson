package xpkg

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"

	"github.com/neal/zerojson/conformance/generator/xpkg/inner"
)

func fullOuter() Outer {
	ext := func(name string) inner.Ext {
		return inner.Ext{
			Name:  name,
			Kind:  inner.KindB,
			N:     -42,
			Score: 1.5,
			Tags:  []string{"t<1>", "t2"},
			Sub:   &inner.Sub{A: "sub-" + name, B: []int{1, 2}},
		}
	}
	p := ext("p")
	return Outer{
		ID:    "o1",
		V:     ext("v"),
		P:     &p,
		S:     []inner.Ext{ext("s0"), {Name: "s1"}},
		SP:    []*inner.Ext{&p, nil},
		M:     map[string]inner.Ext{"k": ext("m")},
		Roots: inner.Exts{ext("r0")},
		Index: inner.ExtIndex{"i": ext("i")},
		Tail:  7,
	}
}

// TestCrossPackageMatchesStdlib pins that a generated type nesting types
// generated in another package encodes byte-identically to encoding/json
// (which reflects both packages structurally) and decodes to the same Go
// value through every zerojson entry point.
func TestCrossPackageMatchesStdlib(t *testing.T) {
	for name, v := range map[string]any{
		"outer zero":  &Outer{},
		"outer full":  ptr(fullOuter()),
		"chain empty": &Chain{Head: "h"},
		"chain full":  &Chain{Head: "h", Outer: ptr(fullOuter())},
	} {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			got, err := v.(interface{ AppendZJSON([]byte) ([]byte, error) }).AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("encode mismatch\nzerojson: %s\nstdlib:   %s", got, want)
			}

			stdVal := reflect.New(reflect.TypeOf(v).Elem())
			if err := json.Unmarshal(want, stdVal.Interface()); err != nil {
				t.Fatal(err)
			}
			for mode, decode := range decoders(reflect.TypeOf(v).Elem()) {
				zjVal := reflect.New(reflect.TypeOf(v).Elem())
				if err := decode(want, zjVal.Interface()); err != nil {
					t.Fatalf("%s: %v", mode, err)
				}
				if !reflect.DeepEqual(zjVal.Interface(), stdVal.Interface()) {
					t.Fatalf("%s decode mismatch\nzerojson: %+v\nstdlib:   %+v", mode, zjVal.Elem(), stdVal.Elem())
				}
			}
		})
	}
}

// TestCrossPackageCopyModeDoesNotAlias scribbles the input after a copy-mode
// decode: strings decoded through the foreign ReadZJSON must not alias it.
// The borrow-mode decode must alias it (that is the point of borrow mode).
func TestCrossPackageCopyModeDoesNotAlias(t *testing.T) {
	data, err := ptr(fullOuter()).AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	var copied Outer
	if err := copied.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	want := fullOuter()
	for i := range data {
		data[i] = 'X'
	}
	if !reflect.DeepEqual(copied, want) {
		t.Fatalf("copy-mode decode aliased the input buffer\ngot:  %+v\nwant: %+v", copied, want)
	}

	data, _ = ptr(fullOuter()).AppendZJSON(nil)
	var borrowed Outer
	if err := borrowed.UnmarshalZJSONBorrow(data); err != nil {
		t.Fatal(err)
	}
	if !aliases(borrowed.V.Name, data) || !aliases(borrowed.M["k"].Sub.A, data) {
		t.Fatal("borrow-mode decode did not alias the input through the foreign Reader")
	}
}

// TestCrossPackageNullAndErrors covers null clearing and error propagation
// out of a foreign ReadZJSON (the error must surface from the outer entry
// point, not be dropped).
func TestCrossPackageNullAndErrors(t *testing.T) {
	v := fullOuter()
	if err := v.UnmarshalZJSON([]byte(`{"p":null,"s":null,"sp":[null],"m":null,"roots":null,"index":null}`)); err != nil {
		t.Fatal(err)
	}
	if v.P != nil || v.S != nil || len(v.SP) != 1 || v.SP[0] != nil || v.M != nil || v.Roots != nil || v.Index != nil {
		t.Fatalf("null did not clear foreign-typed fields: %+v", v)
	}
	for _, in := range []string{
		`{"v":{"n":"5"}}`,
		`{"s":[{"name":1}]}`,
		`{"m":{"k":{"sub":{"b":[1.5]}}}}`,
		`{"roots":[{"tags":"x"}]}`,
		`{"index":{"i":[]}}`,
		`{"v":{"name":"x"`,
	} {
		var o Outer
		if err := o.UnmarshalZJSON([]byte(in)); err == nil {
			t.Errorf("UnmarshalZJSON(%s) succeeded; want error", in)
		}
		var s Outer
		if err := json.Unmarshal([]byte(in), &s); err == nil {
			t.Errorf("test assumption broken: encoding/json accepted %s", in)
		}
	}
}

func decoders(typ reflect.Type) map[string]func([]byte, any) error {
	if typ == reflect.TypeOf(Chain{}) {
		return map[string]func([]byte, any) error{
			"copy":   func(b []byte, v any) error { return v.(*Chain).UnmarshalZJSON(b) },
			"borrow": func(b []byte, v any) error { return v.(*Chain).UnmarshalZJSONBorrow(b) },
			"pooled": func(b []byte, v any) error { var d ChainDecoder; return d.Decode(b, v.(*Chain)) },
		}
	}
	return map[string]func([]byte, any) error{
		"copy":          func(b []byte, v any) error { return v.(*Outer).UnmarshalZJSON(b) },
		"borrow":        func(b []byte, v any) error { return v.(*Outer).UnmarshalZJSONBorrow(b) },
		"pooled":        func(b []byte, v any) error { var d OuterDecoder; return d.Decode(b, v.(*Outer)) },
		"pooled borrow": func(b []byte, v any) error { var d OuterDecoder; return d.DecodeBorrow(b, v.(*Outer)) },
	}
}

func aliases(s string, buf []byte) bool {
	if len(s) == 0 {
		return false
	}
	p := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	start := uintptr(unsafe.Pointer(&buf[0]))
	return p >= start && p < start+uintptr(len(buf))
}

func ptr[T any](v T) *T { return &v }
