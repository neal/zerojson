package p

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/neal/zerojson/conformance/internal/oracle"
)

type codec interface {
	AppendZJSON([]byte) ([]byte, error)
	UnmarshalZJSON([]byte) error
}

func encodeParity(t *testing.T, v codec) {
	t.Helper()
	want, se := json.Marshal(v)
	got, ze := v.AppendZJSON(nil)
	if se != nil || ze != nil {
		t.Fatalf("encode errors: stdlib=%v zerojson=%v", se, ze)
	}
	// Map order is intentionally unspecified.
	var a, b any
	if err := json.Unmarshal(want, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("encode: stdlib=%s zerojson=%s", want, got)
	}
}

func TestEmptyStructMethods(t *testing.T) {
	encodeParity(t, &MapHolder{V: map[string]Token{"k": {}}})
	j, tx, p := TokenIndex{"k": {}}, TextIndex{"k": {}}, PointerIndex{"k": {}}
	encodeParity(t, &j)
	encodeParity(t, &tx)
	encodeParity(t, &p) // pointer-only MarshalJSON must NOT run for map values
	for _, makePair := range []func() (codec, codec){
		func() (codec, codec) { return new(TokenIndex), new(TokenIndex) },
		func() (codec, codec) { return new(TextIndex), new(TextIndex) },
		func() (codec, codec) { return new(PointerIndex), new(PointerIndex) },
	} {
		for _, raw := range []string{`"token"`, `"bad"`, `{}`, `null`} {
			zj, std := makePair()
			data := []byte(`{"k":` + raw + `}`)
			calls = 0
			se := json.Unmarshal(data, std)
			n := calls
			calls = 0
			ze := zj.UnmarshalZJSON(data)
			if (se == nil) != (ze == nil) || calls != n {
				t.Errorf("%T %s: stdlib=%v calls=%d; zerojson=%v calls=%d", zj, data, se, n, ze, calls)
			}
		}
	}
}

func TestTaggedHiddenEmbeds(t *testing.T) {
	for _, v := range []codec{
		&Named{hidden: hidden{Visible: 7}, Own: 8},
		&Options{hidden: hidden{Visible: 7}, Own: 8},
		&Ignored{hidden: hidden{Visible: 7}, Own: 8},
	} {
		encodeParity(t, v)
	}
	for _, pair := range [][2]codec{{new(Named), new(Named)}, {new(Options), new(Options)}, {new(Ignored), new(Ignored)}} {
		data, _ := json.Marshal(map[string]any{"nested": map[string]int{"visible": 7}, "visible": 7, "own": 8})
		if err := pair[0].UnmarshalZJSON(data); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, pair[1]); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("hidden fields: zerojson=%+v stdlib=%+v", pair[0], pair[1])
		}
	}
}

func TestDefinedByteSlices(t *testing.T) {
	for _, plain := range [][]Byte{nil, {}, {0, 65, 255}} {
		named := Bytes(plain)
		v := ByteHolder{Plain: plain, Alias: plain, Named: &named, Nested: [][]Byte{plain}, Map: map[string][]Byte{"k": plain}, Custom: []CustomByte{65, 255}, Via: []CustomAlias{65, 255}}
		v.External = append(v.External, 65, 255)
		encodeParity(t, &v)
	}
	var pool ByteHolderDecoder
	for _, doc := range []string{
		`{"plain":"QUI=","alias":"AP8=","named":"","nested":[null,"","QUI="],"map":{"k":"QUI="},"custom":["65","255"],"via":["65","255"]}`,
		`{"plain":[0,65,255],"alias":[],"named":[65],"nested":[[0,255]],"map":{"k":[65]},"external":"AP8="}`,
		`{"plain":null,"alias":null,"named":null,"nested":null,"map":null,"custom":null,"via":null}`,
	} {
		for _, mode := range []string{"copy", "borrow", "pool"} {
			var zj, std ByteHolder
			if err := json.Unmarshal([]byte(doc), &std); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "copy":
				err = zj.UnmarshalZJSON([]byte(doc))
			case "borrow":
				err = zj.UnmarshalZJSONBorrow([]byte(doc))
			case "pool":
				err = pool.Decode([]byte(doc), &zj)
			}
			if err != nil || !reflect.DeepEqual(zj, std) {
				t.Fatalf("%s %s: zerojson=%+v (%v) stdlib=%+v", mode, doc, zj, err, std)
			}
			encodeParity(t, &zj)
		}
	}
}

func TestArrayEmptinessAndNull(t *testing.T) {
	v := Arrays{}
	encodeParity(t, &v)
	got, err := v.AppendZJSON(nil)
	if err != nil || !bytes.Equal(got, []byte(`{"full":"present"}`)) {
		t.Fatalf("omitempty: %s %v", got, err)
	}
	calls = 0
	if err := v.UnmarshalZJSON([]byte(`{"empty":null,"text":null,"full":null}`)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 { // JSON methods see null; text arrays remain untouched.
		t.Fatalf("null callbacks=%d want 2", calls)
	}
}

func freshRefs() Refs {
	i, c, e, s, m, str := 0, Child{}, Known, []int(nil), map[string]int(nil), "old"
	return Refs{&i, &i, &c, &c, &e, &e, &s, &s, &m, &m, &str, &str}
}

func TestSharedPointers(t *testing.T) {
	for _, doc := range []string{
		`{"a":1,"b":2,"c":{"n":1},"d":{"n":2},"e":"known","f":"unknown","g":[1],"h":[2],"i":{"x":1},"j":{"y":2},"k":"a","l":"b"}`,
		`{"b":2,"a":1,"d":{"n":2},"c":{"n":1},"f":"unknown","e":"known","h":[2],"g":[1],"j":{"y":2},"i":{"x":1}}`,
		`{"a":null,"b":1,"a":2,"g":null,"h":[1],"g":[2],"i":null,"j":{"x":1},"i":{"y":2}}`,
		`{"a":1,"c":{"n":1},"d":{},"d":{"n":2},"unknown":0,"b":2}`, // late fallback/replay
	} {
		for _, borrow := range []bool{false, true} {
			zj, std := freshRefs(), freshRefs()
			original, wantOriginal := zj, std
			se := json.Unmarshal([]byte(doc), &std)
			var ze error
			if borrow {
				ze = zj.UnmarshalZJSONBorrow([]byte(doc))
			} else {
				ze = zj.UnmarshalZJSON([]byte(doc))
			}
			if se != nil || ze != nil || !reflect.DeepEqual(zj, std) || !reflect.DeepEqual(original, wantOriginal) {
				t.Fatalf("%s: zerojson=%+v (%v), stdlib=%+v (%v); external pointers differ=%v", doc, zj, ze, std, se, !reflect.DeepEqual(original, wantOriginal))
			}
		}
	}
	var pool RefsDecoder
	for range 3 {
		zj := freshRefs()
		doc := []byte(`{"a":1,"b":2,"c":{"n":1},"d":{"n":2},"i":{"x":1},"j":{}}`)
		var std Refs // pool deliberately resets the target
		if err := json.Unmarshal(doc, &std); err != nil {
			t.Fatal(err)
		}
		if err := pool.Decode(doc, &zj); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(zj, std) {
			t.Fatalf("pool zerojson=%+v stdlib=%+v", zj, std)
		}
	}
}

func TestSharedCustomReceiver(t *testing.T) {
	for _, doc := range []string{`{"a":1,"b":2}`, `{"a":1,"unknown":0,"a":2,"b":3}`, `{"a":null,"b":2,"a":1}`} {
		a, b := Acc(7), Acc(7)
		zj, std := CustomRefs{&a, &a}, CustomRefs{&b, &b}
		calls = 0
		se := json.Unmarshal([]byte(doc), &std)
		n := calls
		calls = 0
		ze := zj.UnmarshalZJSON([]byte(doc))
		if se != nil || ze != nil || calls != n || a != b || !reflect.DeepEqual(zj, std) {
			t.Fatalf("%s: zerojson=%+v %v calls=%d external=%d stdlib=%+v %v calls=%d external=%d", doc, zj, ze, calls, a, std, se, n, b)
		}
	}
}

func TestInterfaceReceivers(t *testing.T) {
	for _, makeValue := range []func() any{
		func() any { v := Positive(7); return &v },
		func() any { v := Positive(7); p := &v; return &p },
		func() any { v := Acc(7); return &v },
		func() any { v := 7; return &v },
		func() any { var v *Positive; return v },
		func() any { return 7 },
	} {
		for _, raw := range []string{`8`, `-1`, `null`} {
			for _, doc := range []string{
				`{"v":VALUE,"p":VALUE,"box":{"v":VALUE},"tail":1}`,
				`{"v":VALUE,"unknown":0,"box":{"v":VALUE},"v":VALUE,"tail":1}`,
				`{"box":{"v":VALUE},"v":VALUE,"p":VALUE}`, // nested dynamic callback
			} {
				doc = strings.ReplaceAll(doc, "VALUE", raw)
				for _, borrow := range []bool{false, true} {
					fresh := func() Interfaces {
						p := makeValue()
						return Interfaces{V: makeValue(), P: &p, Box: AnyBox{V: makeValue()}}
					}
					zj, std := fresh(), fresh()
					calls = 0
					se := json.Unmarshal([]byte(doc), &std)
					n := calls
					calls = 0
					wantCalls := n
					if se != nil {
						// zerojson is fail-fast. Classic v1 also stops at a
						// custom error; v1-on-v2 continues these other fields.
						wantCalls = 1
						stdCalls := 1
						if oracle.StdlibJSONV2 {
							stdCalls = strings.Count(doc, ":-1")
						}
						if n != stdCalls {
							t.Fatalf("stdlib callback-error behavior changed: calls=%d want=%d", n, stdCalls)
						}
					}
					var ze error
					if borrow {
						ze = zj.UnmarshalZJSONBorrow([]byte(doc))
					} else {
						ze = zj.UnmarshalZJSON([]byte(doc))
					}
					if (se == nil) != (ze == nil) || calls != wantCalls || (se == nil && !reflect.DeepEqual(zj, std)) {
						t.Fatalf("%T %s: zerojson=%+v %v calls=%d, stdlib=%+v %v calls=%d", makeValue(), doc, zj, ze, calls, std, se, n)
					}
				}
			}
		}
	}
	// Whole-document validation rejects malformed numbers before mutation.
	v := Interfaces{}
	if err := v.UnmarshalZJSON([]byte(`{"v":1,"v":007}`)); err == nil || v.V != nil {
		t.Fatalf("generic any reader changed: %#v %v", v.V, err)
	}
}

func TestTextCallbackGrammar(t *testing.T) {
	for _, raw := range []string{`"\q"`, "\"raw\ncontrol\"", `"\u12x4"`, `"unterminated`} {
		for _, doc := range []string{`{"v":VALUE}`, `{"p":VALUE}`, `{"s":[VALUE]}`, `{"m":{"k":VALUE}}`, `{"l":[VALUE]}`} {
			doc = strings.ReplaceAll(doc, "VALUE", raw)
			var v TextHolder
			calls = 0
			if err := v.UnmarshalZJSON([]byte(doc)); err == nil || calls != 0 {
				t.Errorf("malformed text reached callback: %q err=%v calls=%d", doc, err, calls)
			}
		}
		var list TextList
		calls = 0
		if err := list.UnmarshalZJSON([]byte("[" + raw + "]")); err == nil || calls != 0 {
			t.Errorf("root text reached callback: %q err=%v calls=%d", raw, err, calls)
		}
	}
	for _, doc := range []string{`{"v":"ok","p":"\u0061","s":["\n"],"m":{"k":"é"},"l":["x"]}`, "{\"v\":\"\xff\"}"} {
		var zj, std TextHolder
		if err := json.Unmarshal([]byte(doc), &std); err != nil {
			t.Fatal(err)
		}
		if err := zj.UnmarshalZJSON([]byte(doc)); err != nil || !reflect.DeepEqual(zj, std) {
			t.Fatalf("valid text: zerojson=%+v stdlib=%+v err=%v", zj, std, err)
		}
	}
}
