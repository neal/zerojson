package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// behaviorCase is a source-level generator fixture. Unlike the compile matrix,
// this matrix runs an oracle test inside the generated package, so a codec that
// compiles but selects the wrong method, receiver, or wire representation still
// fails. Add new method/addressability shapes here; the runner owns package
// creation, generator invocation, compilation, and behavioral execution.
type behaviorCase struct {
	name         string
	imports      string // extra import lines for types.go, e.g. easyjson's jwriter/jlexer
	declarations string
	types        string
	flags        []string
	testBody     string
	wantGenError string
}

const behaviorTestPreamble = `package p

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

var _ = bytes.Equal
var _ = json.Marshal
var _ = reflect.DeepEqual

func TestGeneratedBehavior(t *testing.T) {
`

// methodAddressabilityCases systematically crosses custom method family,
// receiver kind, and container addressability. The most failure-prone cell is
// a pointer-only method on a map value: map elements are not addressable while
// direct fields and slice elements are, so encoding must fall back to the
// structural representation only for that shape. Decode still gets a mutable
// temporary in all three shapes.
func methodAddressabilityCases() []behaviorCase {
	var cases []behaviorCase
	for _, family := range []string{"json", "text"} {
		for _, receiver := range []string{"value", "pointer"} {
			for _, shape := range []string{"direct", "slice", "map"} {
				recv := "(Custom)"
				if receiver == "pointer" {
					recv = "(*Custom)"
				}
				methods := "\nfunc " + recv + " MarshalJSON() ([]byte, error) { return []byte(`\"custom\"`), nil }\n" +
					"func (v *Custom) UnmarshalJSON([]byte) error { v.Value = 77; return nil }\n"
				if family == "text" {
					methods = "\nfunc " + recv + " MarshalText() ([]byte, error) { return []byte(`custom`), nil }\n" +
						"func (v *Custom) UnmarshalText([]byte) error { v.Value = 77; return nil }\n"
				}

				fieldType := "Custom"
				value := "Custom{Value: 1}"
				input := `{"field":"custom"}`
				switch shape {
				case "slice":
					fieldType = "[]Custom"
					value = "[]Custom{{Value: 1}}"
					input = `{"field":["custom"]}`
				case "map":
					fieldType = "map[string]Custom"
					value = `map[string]Custom{"k": {Value: 1}}`
					input = `{"field":{"k":"custom"}}`
				}

				cases = append(cases, behaviorCase{
					name: family + "_" + receiver + "_" + shape,
					declarations: "\ntype Custom struct { Value int `json:\"value\"` }" + methods +
						"type Root struct { Field " + fieldType + " `json:\"field\"` }\n",
					types: "Root",
					testBody: `
		type oracle Root
		v := Root{Field: ` + value + `}
		ov := oracle(v)
		want, err := json.Marshal(&ov)
		if err != nil { t.Fatal(err) }
		got, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("encode: stdlib=%s zerojson=%s", want, got) }
		input := []byte(` + "`" + input + "`" + `)
		var std oracle
		var zj Root
		if err := json.Unmarshal(input, &std); err != nil { t.Fatal(err) }
		if err := zj.UnmarshalZJSON(input); err != nil { t.Fatal(err) }
		if !reflect.DeepEqual(std, oracle(zj)) { t.Fatalf("decode: stdlib=%+v zerojson=%+v", std, zj) }
`,
				})
			}
		}
	}
	return cases
}

// customCodecFaultCases exercises callback behavior that ordinary successful
// oracle comparisons cannot see: receiver mutation before failure, exactly-once
// invocation, partial-buffer handling, malformed successful output, explicit
// lexer failure, and panic propagation. These are source fixtures so method
// discovery and the generated call site are both under test.
func customCodecFaultCases() []behaviorCase {
	return []behaviorCase{
		{
			name: "json_decode_mutates_then_errors",
			declarations: `
var jsonDecodeCalls int
type FaultJSON struct{ Value int }
func (FaultJSON) MarshalJSON() ([]byte, error) { return []byte("0"), nil }
func (v *FaultJSON) UnmarshalJSON([]byte) error {
	jsonDecodeCalls++
	v.Value = 91
	return errors.New("json decode fault")
}
type Root struct { Field FaultJSON ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		input := []byte(` + "`{\"field\":1}`" + `)
		jsonDecodeCalls = 0
		var std oracle
		stdErr := json.Unmarshal(input, &std)
		stdCalls := jsonDecodeCalls
		jsonDecodeCalls = 0
		var zj Root
		zjErr := zj.UnmarshalZJSON(input)
		if stdErr == nil || zjErr == nil { t.Fatalf("decode errors: stdlib=%v zerojson=%v", stdErr, zjErr) }
		if stdCalls != 1 || jsonDecodeCalls != 1 { t.Fatalf("decode calls: stdlib=%d zerojson=%d", stdCalls, jsonDecodeCalls) }
		if std.Field.Value != 91 || zj.Field.Value != std.Field.Value { t.Fatalf("mutation: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
		{
			name: "text_decode_mutates_then_errors",
			declarations: `
var textDecodeCalls int
type FaultText struct{ Value int }
func (FaultText) MarshalText() ([]byte, error) { return []byte("ok"), nil }
func (v *FaultText) UnmarshalText([]byte) error {
	textDecodeCalls++
	v.Value = 92
	return errors.New("text decode fault")
}
type Root struct { Field FaultText ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		input := []byte(` + "`{\"field\":\"bad\"}`" + `)
		textDecodeCalls = 0
		var std oracle
		stdErr := json.Unmarshal(input, &std)
		stdCalls := textDecodeCalls
		textDecodeCalls = 0
		var zj Root
		zjErr := zj.UnmarshalZJSON(input)
		if stdErr == nil || zjErr == nil { t.Fatalf("decode errors: stdlib=%v zerojson=%v", stdErr, zjErr) }
		if stdCalls != 1 || textDecodeCalls != 1 { t.Fatalf("decode calls: stdlib=%d zerojson=%d", stdCalls, textDecodeCalls) }
		if std.Field.Value != 92 || zj.Field.Value != std.Field.Value { t.Fatalf("mutation: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
		{
			name: "json_encode_mutates_then_errors",
			declarations: `
var jsonEncodeCalls int
type FaultJSON struct{ Value int }
func (v *FaultJSON) MarshalJSON() ([]byte, error) {
	jsonEncodeCalls++
	v.Value = 93
	return []byte("123"), errors.New("json encode fault")
}
func (*FaultJSON) UnmarshalJSON([]byte) error { return nil }
type Root struct {
	Before int ` + "`json:\"before\"`" + `
	Field FaultJSON ` + "`json:\"field\"`" + `
}
`,
			types: "Root",
			testBody: `
		type oracle Root
		std := oracle{Before: 1, Field: FaultJSON{Value: 1}}
		jsonEncodeCalls = 0
		_, stdErr := json.Marshal(&std)
		stdCalls := jsonEncodeCalls
		zj := Root{Before: 1, Field: FaultJSON{Value: 1}}
		jsonEncodeCalls = 0
		prefix := []byte("prefix")
		got, zjErr := zj.AppendZJSON(append([]byte(nil), prefix...))
		if stdErr == nil || zjErr == nil { t.Fatalf("encode errors: stdlib=%v zerojson=%v", stdErr, zjErr) }
		if stdCalls != 1 || jsonEncodeCalls != 1 { t.Fatalf("encode calls: stdlib=%d zerojson=%d", stdCalls, jsonEncodeCalls) }
		if std.Field.Value != 93 || zj.Field.Value != std.Field.Value { t.Fatalf("mutation: stdlib=%+v zerojson=%+v", std, zj) }
		if !bytes.HasPrefix(got, prefix) { t.Fatalf("partial result lost caller prefix: %q", got) }
		if bytes.Contains(got[len(prefix):], []byte("123")) { t.Fatalf("errored MarshalJSON bytes were appended: %q", got) }
`,
		},
		{
			name: "text_encode_errors_once",
			declarations: `
var textEncodeCalls int
type FaultText int
func (FaultText) MarshalText() ([]byte, error) {
	textEncodeCalls++
	return []byte("partial"), errors.New("text encode fault")
}
func (*FaultText) UnmarshalText([]byte) error { return nil }
type Root struct { Field FaultText ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		std := oracle{Field: 1}
		textEncodeCalls = 0
		_, stdErr := json.Marshal(&std)
		stdCalls := textEncodeCalls
		zj := Root{Field: 1}
		textEncodeCalls = 0
		prefix := []byte("prefix")
		got, zjErr := zj.AppendZJSON(append([]byte(nil), prefix...))
		if stdErr == nil || zjErr == nil { t.Fatalf("encode errors: stdlib=%v zerojson=%v", stdErr, zjErr) }
		if stdCalls != 1 || textEncodeCalls != 1 { t.Fatalf("encode calls: stdlib=%d zerojson=%d", stdCalls, textEncodeCalls) }
		if !bytes.HasPrefix(got, prefix) { t.Fatalf("partial result lost caller prefix: %q", got) }
		if bytes.Contains(got[len(prefix):], []byte("partial")) { t.Fatalf("errored MarshalText bytes were appended: %q", got) }
`,
		},
		{
			name: "malformed_json_marshaler_rejected_in_every_shape",
			declarations: `
var malformedCalls int
type Malformed int
func (Malformed) MarshalJSON() ([]byte, error) { malformedCalls++; return []byte("{"), nil }
func (*Malformed) UnmarshalJSON([]byte) error { return nil }
type DirectRoot struct { Field Malformed ` + "`json:\"field\"`" + ` }
type SliceRoot struct { Field []Malformed ` + "`json:\"field\"`" + ` }
type MapRoot struct { Field map[string]Malformed ` + "`json:\"field\"`" + ` }
type MalformedList []Malformed
type MalformedMap map[string]Malformed
`,
			types: "DirectRoot,SliceRoot,MapRoot,MalformedList,MalformedMap",
			testBody: `
		check := func(name string, std any, encode func([]byte) ([]byte, error)) {
			t.Helper()
			malformedCalls = 0
			_, stdErr := json.Marshal(std)
			stdCalls := malformedCalls
			malformedCalls = 0
			got, zjErr := encode([]byte("prefix"))
			if stdErr == nil || zjErr == nil { t.Fatalf("%s malformed output errors: stdlib=%v zerojson=%v; output=%q", name, stdErr, zjErr, got) }
			if stdCalls != 1 || malformedCalls != 1 { t.Fatalf("%s malformed calls: stdlib=%d zerojson=%d", name, stdCalls, malformedCalls) }
			if !bytes.HasPrefix(got, []byte("prefix")) { t.Fatalf("%s partial result lost caller prefix: %q", name, got) }
		}
		check("direct", DirectRoot{Field: 1}, func(dst []byte) ([]byte, error) { v := DirectRoot{Field: 1}; return v.AppendZJSON(dst) })
		check("slice field", SliceRoot{Field: []Malformed{1}}, func(dst []byte) ([]byte, error) { v := SliceRoot{Field: []Malformed{1}}; return v.AppendZJSON(dst) })
		check("map field", MapRoot{Field: map[string]Malformed{"k": 1}}, func(dst []byte) ([]byte, error) { v := MapRoot{Field: map[string]Malformed{"k": 1}}; return v.AppendZJSON(dst) })
		check("slice root", MalformedList{1}, func(dst []byte) ([]byte, error) { v := MalformedList{1}; return v.AppendZJSON(dst) })
		check("map root", MalformedMap{"k": 1}, func(dst []byte) ([]byte, error) { v := MalformedMap{"k": 1}; return v.AppendZJSON(dst) })
`,
		},
		{
			name: "json_decode_panic_propagates_once",
			declarations: `
var panicCalls int
type Panicker int
func (Panicker) MarshalJSON() ([]byte, error) { return []byte("0"), nil }
func (*Panicker) UnmarshalJSON([]byte) error { panicCalls++; panic("codec panic") }
type Root struct { Field Panicker ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		input := []byte(` + "`{\"field\":1}`" + `)
		panicCalls = 0
		var stdPanic any
		func() {
			defer func() { stdPanic = recover() }()
			var std oracle
			_ = json.Unmarshal(input, &std)
		}()
		stdCalls := panicCalls
		panicCalls = 0
		var zjPanic any
		func() {
			defer func() { zjPanic = recover() }()
			var zj Root
			_ = zj.UnmarshalZJSON(input)
		}()
		if stdPanic == nil || zjPanic == nil { t.Fatalf("panic propagation: stdlib=%v zerojson=%v", stdPanic, zjPanic) }
		if stdCalls != 1 || panicCalls != 1 { t.Fatalf("panic calls: stdlib=%d zerojson=%d", stdCalls, panicCalls) }
`,
		},
		{
			name: "appender_faults_are_single_shot",
			declarations: `
var appEncodeCalls, appDecodeCalls, jsonEncodeCalls, jsonDecodeCalls int
type FaultAppender int
func (FaultAppender) AppendZJSON(dst []byte) ([]byte, error) {
	appEncodeCalls++
	return append(dst, '7'), errors.New("appender encode fault")
}
func (v *FaultAppender) ReadZJSON(l *zerojson.Lexer) {
	appDecodeCalls++
	*v = 97
	l.Fail("appender decode fault")
}
func (FaultAppender) MarshalJSON() ([]byte, error) {
	jsonEncodeCalls++
	return []byte("7"), errors.New("json encode fault")
}
func (v *FaultAppender) UnmarshalJSON([]byte) error {
	jsonDecodeCalls++
	*v = 97
	return errors.New("json decode fault")
}
type Root struct { Field FaultAppender ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		_, stdEncodeErr := json.Marshal(oracle{Field: 1})
		got, zjEncodeErr := (&Root{Field: 1}).AppendZJSON([]byte("prefix"))
		if stdEncodeErr == nil || zjEncodeErr == nil { t.Fatalf("encode errors: stdlib=%v zerojson=%v", stdEncodeErr, zjEncodeErr) }
		if jsonEncodeCalls != 1 || appEncodeCalls != 1 { t.Fatalf("encode calls: json=%d appender=%d", jsonEncodeCalls, appEncodeCalls) }
		if !bytes.HasPrefix(got, []byte("prefix")) { t.Fatalf("partial result lost caller prefix: %q", got) }

		input := []byte(` + "`{\"field\":7}`" + `)
		var std oracle
		stdDecodeErr := json.Unmarshal(input, &std)
		var zj Root
		zjDecode_Err := zj.UnmarshalZJSON(input)
		if stdDecodeErr == nil || zjDecode_Err == nil { t.Fatalf("decode errors: stdlib=%v zerojson=%v", stdDecodeErr, zjDecode_Err) }
		if jsonDecodeCalls != 1 || appDecodeCalls != 1 { t.Fatalf("decode calls: json=%d appender=%d", jsonDecodeCalls, appDecodeCalls) }
		if std.Field != 97 || zj.Field != Root(std).Field { t.Fatalf("decode mutation: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
	}
}

func TestGeneratorBehaviorMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("behavior matrix spawns the generator and a nested go test")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	base, err := os.MkdirTemp(moduleRoot, "zjbehavior")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	cases := []behaviorCase{
		{
			name: "json_value_pair",
			declarations: `
type Custom struct{ Value int }
func (Custom) MarshalJSON() ([]byte, error) { return []byte("42"), nil }
func (v *Custom) UnmarshalJSON(b []byte) error {
	if string(b) == "42" { v.Value = 42 }
	return nil
}
type Root struct { Field Custom ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		v := Root{Field: Custom{Value: 1}}
		want, err := json.Marshal(oracle(v))
		if err != nil { t.Fatal(err) }
		got, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("encode: stdlib=%s zerojson=%s", want, got) }
		input := []byte(` + "`{\"field\":42}`" + `)
		var std oracle
		var zj Root
		if err := json.Unmarshal(input, &std); err != nil { t.Fatal(err) }
		if err := zj.UnmarshalZJSON(input); err != nil { t.Fatal(err) }
		if zj.Field.Value != std.Field.Value { t.Fatalf("decode: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
		{
			name: "text_value_pair",
			declarations: `
type Text int
func (Text) MarshalText() ([]byte, error) { return []byte("text-value"), nil }
func (v *Text) UnmarshalText(b []byte) error {
	if string(b) == "text-value" { *v = 17 }
	return nil
}
type Root struct { Field Text ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		v := Root{Field: 1}
		want, err := json.Marshal(oracle(v))
		if err != nil { t.Fatal(err) }
		got, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("encode: stdlib=%s zerojson=%s", want, got) }
		input := []byte(` + "`{\"field\":\"text-value\"}`" + `)
		var std oracle
		var zj Root
		if err := json.Unmarshal(input, &std); err != nil { t.Fatal(err) }
		if err := zj.UnmarshalZJSON(input); err != nil { t.Fatal(err) }
		if zj.Field != std.Field { t.Fatalf("decode: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
		{
			name: "wrong_signature_is_structural",
			declarations: `
type Wrong struct { Value int ` + "`json:\"value\"`" + ` }
func (Wrong) MarshalJSON() string { return "not-an-interface-method" }
type Root struct { Field Wrong ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		v := Root{Field: Wrong{Value: 3}}
		want, err := json.Marshal(oracle(v))
		if err != nil { t.Fatal(err) }
		got, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("encode: stdlib=%s zerojson=%s", want, got) }
`,
		},
		{
			name: "arbitrary_int64_marshaler_delegates",
			declarations: `
type WeirdInt64 int64
func (WeirdInt64) MarshalJSON() ([]byte, error) { return []byte("\"weird\""), nil }
func (v *WeirdInt64) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil { return err }
	if s == "weird" { *v = 77 }
	return nil
}
type Root struct { Field WeirdInt64 ` + "`json:\"field\"`" + ` }
`,
			types: "Root",
			testBody: `
		type oracle Root
		v := Root{Field: 12}
		want, err := json.Marshal(oracle(v))
		if err != nil { t.Fatal(err) }
		got, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("encode: stdlib=%s zerojson=%s", want, got) }
		input := []byte(` + "`{\"field\":\"weird\"}`" + `)
		var std oracle
		var zj Root
		if err := json.Unmarshal(input, &std); err != nil { t.Fatal(err) }
		if err := zj.UnmarshalZJSON(input); err != nil { t.Fatal(err) }
		if zj.Field != std.Field { t.Fatalf("decode: stdlib=%+v zerojson=%+v", std, zj) }
`,
		},
		{
			// -compat emits MarshalJSON on a VALUE receiver, so json.Marshal
			// routes through zerojson in every context — value, pointer, and
			// map value — where a pointer receiver would be silently skipped
			// for non-addressable values (see cmd/zerojsongen/compat.go). The
			// oracle is easyjson, the migration's byte-identity target, NOT
			// stdlib's default encoding of a methodless twin: the nested Child
			// has a pointer-receiver MarshalJSON, which zerojson (like easyjson)
			// fires uniformly regardless of the outer value's addressability.
			// stdlib's default encoder would skip it for a value marshal — the
			// exact addressability quirk that breaks easyjson byte-identity and
			// that -compat must NOT reproduce. (Verified out-of-band that
			// easyjson emits {"child":"pointer-method"} here in all contexts.)
			name: "compat_marshaler_fires_uniformly",
			declarations: `
type Child struct { Value int ` + "`json:\"value\"`" + ` }
func (*Child) MarshalJSON() ([]byte, error) { return []byte("\"pointer-method\""), nil }
func (v *Child) UnmarshalJSON([]byte) error { v.Value = 99; return nil }
type Root struct { Child Child ` + "`json:\"child\"`" + ` }
`,
			types: "Root",
			flags: []string{"-compat"},
			testBody: `
		v := Root{Child: Child{Value: 1}}
		canonical, err := v.AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if _, ok := any(v).(json.Marshaler); !ok { t.Fatal("value must implement json.Marshaler (MarshalJSON must be a value receiver)") }
		if _, ok := any(&v).(json.Marshaler); !ok { t.Fatal("pointer must implement json.Marshaler") }
		gotValue, err := json.Marshal(v)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(gotValue, canonical) { t.Fatalf("value marshal routed around zerojson: got %s want %s", gotValue, canonical) }
		gotPointer, err := json.Marshal(&v)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(gotPointer, canonical) { t.Fatalf("pointer marshal: got %s want %s", gotPointer, canonical) }
		gotMap, err := json.Marshal(map[string]Root{"k": v})
		if err != nil { t.Fatal(err) }
		wantMap := append(append([]byte("{\"k\":"), canonical...), '}')
		if !bytes.Equal(gotMap, wantMap) { t.Fatalf("map-value marshal routed around zerojson: got %s want %s", gotMap, wantMap) }
`,
		},
		{
			name: "explicit_custom_json_refused",
			declarations: `
type Child struct { Value int ` + "`json:\"value\"`" + ` }
func (Child) MarshalJSON() ([]byte, error) { return []byte("\"custom-child\""), nil }
func (*Child) UnmarshalJSON([]byte) error { return nil }
type Root struct { Child Child ` + "`json:\"child\"`" + ` }
`,
			types:        "Root,Child",
			wantGenError: "explicitly listed type has hand-written JSON/Text methods",
		},
		{
			// easyjson calls MarshalEasyJSON ahead of anything else; zerojson
			// cannot, so a type whose only custom codec is easyjson's must be
			// refused rather than silently encoded by its Go structure.
			name: "easyjson_only_field_refused",
			imports: `	"github.com/mailru/easyjson/jlexer"
	"github.com/mailru/easyjson/jwriter"
`,
			declarations: `
type Cents struct{ V int64 }
func (c Cents) MarshalEasyJSON(w *jwriter.Writer) { w.RawString("\"1.00\"") }
func (c *Cents) UnmarshalEasyJSON(l *jlexer.Lexer) { l.Skip() }
type Root struct { Amount Cents ` + "`json:\"amount\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has a hand-written MarshalEasyJSON method but no MarshalJSON/UnmarshalJSON",
		},
		{
			// An alias for easyjson's Writer or Lexer names the same type, so
			// the refusal must see through it, in the pointee and in the
			// parameter type itself.
			name: "easyjson_only_aliased_pointee_refused",
			imports: `	"github.com/mailru/easyjson/jlexer"
	"github.com/mailru/easyjson/jwriter"
`,
			declarations: `
type WriterAlias = jwriter.Writer
type LexerAlias = jlexer.Lexer
type Cents struct{ V int64 }
func (c Cents) MarshalEasyJSON(w *WriterAlias) { w.RawString("\"1.00\"") }
func (c *Cents) UnmarshalEasyJSON(l *LexerAlias) { l.Skip() }
type Root struct { Amount Cents ` + "`json:\"amount\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has a hand-written MarshalEasyJSON method but no MarshalJSON/UnmarshalJSON",
		},
		{
			name: "easyjson_only_aliased_pointer_refused",
			imports: `	"github.com/mailru/easyjson/jlexer"
	"github.com/mailru/easyjson/jwriter"
`,
			declarations: `
type WriterRef = *jwriter.Writer
type LexerRef = *jlexer.Lexer
type Cents struct{ V int64 }
func (c Cents) MarshalEasyJSON(w WriterRef) { w.RawString("\"1.00\"") }
func (c *Cents) UnmarshalEasyJSON(l LexerRef) { l.Skip() }
type Root struct { Amount Cents ` + "`json:\"amount\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has a hand-written MarshalEasyJSON method but no MarshalJSON/UnmarshalJSON",
		},
		{
			name: "easyjson_only_scalar_field_refused",
			imports: `	"github.com/mailru/easyjson/jlexer"
`,
			declarations: `
type Code int
func (c *Code) UnmarshalEasyJSON(l *jlexer.Lexer) { *c = Code(len(l.String())) }
type Root struct { Code Code ` + "`json:\"code\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has a hand-written UnmarshalEasyJSON method but no MarshalJSON/UnmarshalJSON",
		},
		{
			name: "easyjson_only_root_refused",
			imports: `	"github.com/mailru/easyjson/jwriter"
`,
			declarations: `
type Root struct { V int ` + "`json:\"v\"`" + ` }
func (r Root) MarshalEasyJSON(w *jwriter.Writer) { w.RawString("1") }
`,
			types:        "Root",
			wantGenError: "type has a hand-written MarshalEasyJSON method",
		},
		{
			// With a JSON pair alongside, zerojson delegates to MarshalJSON,
			// which (by the type author's contract) matches MarshalEasyJSON.
			name: "easyjson_and_json_pair_delegates",
			imports: `	"github.com/mailru/easyjson/jlexer"
	"github.com/mailru/easyjson/jwriter"
`,
			declarations: `
type Cents struct{ V int64 }
func (c Cents) MarshalEasyJSON(w *jwriter.Writer) { w.RawString("\"1.00\"") }
func (c *Cents) UnmarshalEasyJSON(l *jlexer.Lexer) { l.Skip(); c.V = 100 }
func (c Cents) MarshalJSON() ([]byte, error) { w := jwriter.Writer{}; c.MarshalEasyJSON(&w); return w.BuildBytes() }
func (c *Cents) UnmarshalJSON(b []byte) error { l := jlexer.Lexer{Data: b}; c.UnmarshalEasyJSON(&l); return l.Error() }
type Root struct { Amount Cents ` + "`json:\"amount\"`" + ` }
`,
			types: "Root",
			testBody: `
		got, err := (&Root{}).AppendZJSON(nil)
		if err != nil { t.Fatal(err) }
		if string(got) != ` + "`{\"amount\":\"1.00\"}`" + ` { t.Fatalf("encode: got %s", got) }
		var zj Root
		if err := zj.UnmarshalZJSON([]byte(` + "`{\"amount\":\"x\"}`" + `)); err != nil { t.Fatal(err) }
		if zj.Amount.V != 100 { t.Fatalf("decode did not delegate: %+v", zj) }
`,
		},
		{
			name: "marshal_text_only_refused",
			declarations: `
type Text int
func (Text) MarshalText() ([]byte, error) { return []byte("text"), nil }
type Root struct { Field Text ` + "`json:\"field\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has only one of MarshalText/UnmarshalText",
		},
		{
			name: "unmarshal_text_only_refused",
			declarations: `
type Text int
func (*Text) UnmarshalText([]byte) error { return nil }
type Root struct { Field Text ` + "`json:\"field\"`" + ` }
`,
			types:        "Root",
			wantGenError: "has only one of MarshalText/UnmarshalText",
		},
	}
	cases = append(cases, methodAddressabilityCases()...)
	cases = append(cases, zeroMethodCases()...)
	cases = append(cases, ownershipCases()...)
	cases = append(cases, publicationCases()...)
	cases = append(cases, byteMethodSetCases()...)
	cases = append(cases, customCodecFaultCases()...)
	cases = append(cases, appenderSliceReuseCases()...)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, tc.name)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			typesSrc := `package p

import (
	"encoding/json"
	"errors"

	"github.com/neal/zerojson"
` + tc.imports + `)

var _ = json.Marshal
var _ = errors.New
var _ = zerojson.Valid
` + tc.declarations
			if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(typesSrc), 0o644); err != nil {
				t.Fatal(err)
			}

			args := []string{"-dir", dir, "-types", tc.types, "-out", filepath.Join(dir, "zerojson_gen.go")}
			args = append(args, tc.flags...)
			cmd := exec.Command(gen, args...)
			cmd.Dir = moduleRoot
			out, genErr := cmd.CombinedOutput()
			if tc.wantGenError != "" {
				if genErr == nil || !strings.Contains(string(out), tc.wantGenError) {
					t.Fatalf("generator error: got err=%v output=%s; want substring %q", genErr, out, tc.wantGenError)
				}
				return
			}
			if genErr != nil {
				t.Fatalf("generator failed: %v\n%s", genErr, out)
			}

			testSrc := behaviorTestPreamble + tc.testBody + "\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "behavior_test.go"), []byte(testSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd = exec.Command("go", "test", ".")
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("behavior test failed: %v\n%s", err, out)
			}
		})
	}
}

// appenderSliceReuseCases pins that slices of Appender/Reader values reuse
// their elements as encoding/json reuses a slice's elements: the Reader
// decodes into the element a slot already holds, and a pointer slot keeps
// its pointer. Item's Reader merges into its receiver through
// encoding/json, exactly as encoding/json decodes a plain struct, so any
// reset slot shows up as a lost field.
func appenderSliceReuseCases() []behaviorCase {
	return []behaviorCase{{
		name: "appender_slice_element_reuse", types: "Root,Items,ItemRefs",
		declarations: strings.ReplaceAll(`
type Item struct {
	Name string ~json:"name"~
	N    int    ~json:"n"~
}
type itemPlain Item
func (v Item) AppendZJSON(dst []byte) ([]byte, error) {
	b, err := json.Marshal(itemPlain(v))
	if err != nil { return dst, err }
	return append(dst, b...), nil
}
func (v *Item) ReadZJSON(l *zerojson.Lexer) {
	if raw := l.RawValue(); l.Err() == nil {
		if err := json.Unmarshal(raw, (*itemPlain)(v)); err != nil { l.AddError(err) }
	}
}
type Root struct {
	S  []Item  ~json:"s"~
	SP []*Item ~json:"sp"~
}
type Items []Item
type ItemRefs []*Item
`, "~", "`"),
		testBody: `
 seeded := func(r *Root) { r.S = []Item{{"a", 1}}; r.SP = []*Item{{"b", 2}} }
 hidden := func(r *Root) { s := []Item{{"a", 1}, {"b", 2}}; r.S = s[:0] }
 for _, c := range []struct{ doc string; seed func(*Root) }{
  {"{\"s\":[{\"name\":\"old\",\"n\":42}],\"s\":[{\"name\":\"new\"}],\"sp\":[{\"name\":\"old\",\"n\":7}],\"sp\":[{\"name\":\"new\"}]}", func(*Root) {}},
  {"{\"s\":[{\"name\":\"x\"}],\"sp\":[{\"name\":\"y\"}]}", seeded},
  {"{\"s\":[null],\"sp\":[null]}", seeded},
  {"{\"s\":[{\"name\":\"x\"},{\"name\":\"y\"},{\"name\":\"z\"}]}", seeded},
  {"{\"s\":[{\"name\":\"x\"},{\"name\":\"y\"}]}", hidden},
 } {
  for _, borrow := range []bool{false, true} {
   var want, got Root
   c.seed(&want); c.seed(&got)
   wantPtrs, gotPtrs := append([]*Item(nil), want.SP...), append([]*Item(nil), got.SP...)
   if err := json.Unmarshal([]byte(c.doc), &want); err != nil { t.Fatal(err) }
   decode := got.UnmarshalZJSON
   if borrow { decode = got.UnmarshalZJSONBorrow }
   if err := decode([]byte(c.doc)); err != nil { t.Fatal(err) }
   if !reflect.DeepEqual(&want, &got) { t.Fatalf("appender slice reuse borrow=%v %s\nwant %+v\ngot  %+v", borrow, c.doc, want, got) }
   for i := range wantPtrs {
    if i < len(want.SP) && (want.SP[i] == wantPtrs[i]) != (got.SP[i] == gotPtrs[i]) {
     t.Fatalf("appender slice reuse borrow=%v %s: pointer %d reused by stdlib=%v, zerojson=%v", borrow, c.doc, i, want.SP[i] == wantPtrs[i], got.SP[i] == gotPtrs[i])
    }
   }
   if len(got.S) > 0 && len(want.S) > 0 && &got.S[0] == &want.S[0] { t.Fatal("decoders shared storage") }
  }
 }
 for _, doc := range []string{"[{\"name\":\"x\"}]", "[null]"} {
  wantList, gotList := Items{{"a", 1}}, Items{{"a", 1}}
  if err := json.Unmarshal([]byte(doc), &wantList); err != nil { t.Fatal(err) }
  if err := gotList.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
  if !reflect.DeepEqual(wantList, gotList) { t.Fatalf("appender root slice reuse %s\nwant %+v\ngot  %+v", doc, wantList, gotList) }
  wantRef, gotRef := &Item{"b", 2}, &Item{"b", 2}
  wantRefs, gotRefs := ItemRefs{wantRef}, ItemRefs{gotRef}
  if err := json.Unmarshal([]byte(doc), &wantRefs); err != nil { t.Fatal(err) }
  if err := gotRefs.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
  if !reflect.DeepEqual(wantRefs, gotRefs) || (wantRefs[0] == wantRef) != (gotRefs[0] == gotRef) {
   t.Fatalf("appender root pointer slice reuse %s\nwant %+v\ngot  %+v", doc, wantRefs, gotRefs)
  }
 }
`,
	}, {
		// null clears an Appender-only element whose type is a slice or
		// map, as encoding/json clears one it decodes structurally, while
		// a reused slot keeps everything else.
		name: "appender_slice_null_clears_nullable", types: "Holder,Bags,TagsList",
		declarations: strings.ReplaceAll(`
type Bag []int
type bagPlain []int
func (b Bag) AppendZJSON(dst []byte) ([]byte, error) {
	out, err := json.Marshal(bagPlain(b))
	if err != nil { return dst, err }
	return append(dst, out...), nil
}
func (b *Bag) ReadZJSON(l *zerojson.Lexer) {
	if raw := l.RawValue(); l.Err() == nil {
		if err := json.Unmarshal(raw, (*bagPlain)(b)); err != nil { l.AddError(err) }
	}
}
type Tags map[string]int
type tagsPlain map[string]int
func (m Tags) AppendZJSON(dst []byte) ([]byte, error) {
	out, err := json.Marshal(tagsPlain(m))
	if err != nil { return dst, err }
	return append(dst, out...), nil
}
func (m *Tags) ReadZJSON(l *zerojson.Lexer) {
	if raw := l.RawValue(); l.Err() == nil {
		if err := json.Unmarshal(raw, (*tagsPlain)(m)); err != nil { l.AddError(err) }
	}
}
type Holder struct {
	Bags []Bag  ~json:"bags"~
	Tags []Tags ~json:"tags"~
}
type Bags []Bag
type TagsList []Tags
`, "~", "`"),
		testBody: `
 seed := func(h *Holder) { h.Bags = []Bag{{42}, {7}}; h.Tags = []Tags{{"a": 1}, {"b": 2}} }
 for _, doc := range []string{
  "{\"bags\":[null],\"tags\":[null]}",
  "{\"bags\":[[1],null],\"tags\":[{\"c\":3},null]}",
  "{\"bags\":[null,[5]],\"tags\":[null,{\"d\":4}]}",
 } {
  for _, borrow := range []bool{false, true} {
   var want, got Holder
   seed(&want); seed(&got)
   if err := json.Unmarshal([]byte(doc), &want); err != nil { t.Fatal(err) }
   decode := got.UnmarshalZJSON
   if borrow { decode = got.UnmarshalZJSONBorrow }
   if err := decode([]byte(doc)); err != nil { t.Fatal(err) }
   if !reflect.DeepEqual(&want, &got) { t.Fatalf("appender null element borrow=%v %s\nwant %+v\ngot  %+v", borrow, doc, want, got) }
  }
 }
 for _, doc := range []string{"[null]", "[[1],null]"} {
  wantBags, gotBags := Bags{{42}, {7}}, Bags{{42}, {7}}
  if err := json.Unmarshal([]byte(doc), &wantBags); err != nil { t.Fatal(err) }
  if err := gotBags.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
  if !reflect.DeepEqual(wantBags, gotBags) { t.Fatalf("appender null root element %s\nwant %+v\ngot  %+v", doc, wantBags, gotBags) }
  wantTags, gotTags := TagsList{{"a": 1}, {"b": 2}}, TagsList{{"a": 1}, {"b": 2}}
  if err := json.Unmarshal([]byte(doc), &wantTags); err == nil {
   if err := gotTags.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
   if !reflect.DeepEqual(wantTags, gotTags) { t.Fatalf("appender null root map element %s\nwant %+v\ngot  %+v", doc, wantTags, gotTags) }
  }
 }
`,
	}, {
		// Reuse holds through a slice of structs that contain Appender
		// slices: the enclosing slots, their omitted fields, and the
		// nested elements all keep what they held, as in encoding/json.
		name: "appender_nested_slice_reuse", types: "Root,Wrappers,WrapperRefs", flags: []string{"-trusted"},
		declarations: strings.ReplaceAll(`
type Item struct {
	Name string ~json:"name"~
	N    int    ~json:"n"~
}
type itemPlain Item
func (v Item) MarshalJSON() ([]byte, error) { return json.Marshal(itemPlain(v)) }
func (v *Item) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, (*itemPlain)(v)) }
func (v Item) AppendZJSON(dst []byte) ([]byte, error) {
	b, err := json.Marshal(itemPlain(v))
	if err != nil { return dst, err }
	return append(dst, b...), nil
}
func (v *Item) ReadZJSON(l *zerojson.Lexer) {
	if raw := l.RawValue(); l.Err() == nil {
		if err := json.Unmarshal(raw, (*itemPlain)(v)); err != nil { l.AddError(err) }
	}
}
type Wrapper struct {
	Name  string ~json:"name"~
	Extra int    ~json:"extra"~
	Items []Item ~json:"items"~
}
type Root struct {
	W  []Wrapper  ~json:"w"~
	WP []*Wrapper ~json:"wp"~
}
type Wrappers []Wrapper
type WrapperRefs []*Wrapper
`, "~", "`"),
		testBody: `
 mk := func() Wrapper { return Wrapper{Name: "w", Extra: 5, Items: []Item{{"a", 1}}} }
 seed := func(r *Root) { w := mk(); r.W = []Wrapper{mk()}; r.WP = []*Wrapper{&w} }
 for _, doc := range []string{
  "{\"w\":[{\"items\":[{\"name\":\"x\"}]}],\"wp\":[{\"items\":[{\"name\":\"y\"}]}]}",
  "{\"w\":[{\"name\":\"v\"}],\"wp\":[{\"extra\":6}]}",
  "{\"w\":[{\"items\":[null]}],\"wp\":[null]}",
 } {
  for mode, decode := range map[string]func(*Root, []byte) error{"copy": (*Root).UnmarshalZJSON, "borrow": (*Root).UnmarshalZJSONBorrow, "trusted": (*Root).UnmarshalZJSONTrusted} {
   var want, got Root
   seed(&want); seed(&got)
   wantPtr, gotPtr := want.WP[0], got.WP[0]
   if err := json.Unmarshal([]byte(doc), &want); err != nil { t.Fatal(err) }
   if err := decode(&got, []byte(doc)); err != nil { t.Fatal(err) }
   if !reflect.DeepEqual(&want, &got) { t.Fatalf("nested appender reuse %s %s\nwant %+v\ngot  %+v", mode, doc, want, got) }
   if len(want.WP) > 0 && (want.WP[0] == wantPtr) != (got.WP[0] == gotPtr) { t.Fatalf("nested appender reuse %s %s: pointer reuse differs", mode, doc) }
  }
 }
 doc := "[{\"items\":[{\"name\":\"x\"}]}]"
 wantList, gotList := Wrappers{mk()}, Wrappers{mk()}
 if err := json.Unmarshal([]byte(doc), &wantList); err != nil { t.Fatal(err) }
 if err := gotList.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
 if !reflect.DeepEqual(wantList, gotList) { t.Fatalf("nested appender root reuse\nwant %+v\ngot  %+v", wantList, gotList) }
 w1, w2 := mk(), mk()
 wantRefs, gotRefs := WrapperRefs{&w1}, WrapperRefs{&w2}
 if err := json.Unmarshal([]byte(doc), &wantRefs); err != nil { t.Fatal(err) }
 if err := gotRefs.UnmarshalZJSON([]byte(doc)); err != nil { t.Fatal(err) }
 if !reflect.DeepEqual(wantRefs, gotRefs) || (wantRefs[0] == &w1) != (gotRefs[0] == &w2) { t.Fatalf("nested appender root pointer reuse\nwant %+v\ngot  %+v", wantRefs, gotRefs) }
`,
	}}
}
