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
		zjDecodeErr := zj.UnmarshalZJSON(input)
		if stdDecodeErr == nil || zjDecodeErr == nil { t.Fatalf("decode errors: stdlib=%v zerojson=%v", stdDecodeErr, zjDecodeErr) }
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
	cases = append(cases, customCodecFaultCases()...)

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
)

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
