package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const defaultSchemaSeed uint64 = 0x7a65726f6a736f6e
const defaultSchemaCount = 40

type schemaRNG uint64

func (r *schemaRNG) next() uint64 {
	x := uint64(*r)
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	*r = schemaRNG(x)
	return x
}

func (r *schemaRNG) n(n int) int { return int(r.next() % uint64(n)) }

type schemaAtom struct {
	typ          string
	value        string
	forcedOption string
	omitzeroOK   bool
	internOK     bool
}

var schemaAtoms = []schemaAtom{
	{typ: "string", value: `"text"`, omitzeroOK: true, internOK: true},
	{typ: "*string", value: `"pointer"`, omitzeroOK: true},
	{typ: "bool", value: `true`, omitzeroOK: true},
	{typ: "int", value: `-7`, omitzeroOK: true},
	{typ: "int32", value: `-2147483648`, omitzeroOK: true},
	{typ: "uint16", value: `65535`, omitzeroOK: true},
	{typ: "int64", value: `"-17"`, forcedOption: "string", omitzeroOK: true},
	{typ: "uint32", value: `"17"`, forcedOption: "string", omitzeroOK: true},
	{typ: "float32", value: `1.5`, omitzeroOK: true},
	{typ: "float64", value: `2.5`, omitzeroOK: true},
	{typ: "time.Duration", value: `12345`, omitzeroOK: true},
	{typ: "time.Time", value: `"2021-03-04T05:06:07Z"`, omitzeroOK: true},
	{typ: "uuid.UUID", value: `"12345678-1234-1234-1234-1234567890ab"`, omitzeroOK: true},
	{typ: "decimal.Decimal", value: `"12.34"`, omitzeroOK: true},
	{typ: "Mode", value: `"up"`, omitzeroOK: true, internOK: true},
	{typ: "Quoted", value: `"+17"`, omitzeroOK: true},
	{typ: "TextDate", value: `"date"`},
	{typ: "CustomJSON", value: `19`},
	{typ: "Child", value: `{"a":7,"b":"child"}`},
	{typ: "*Child", value: `{"a":8,"b":"ptr"}`, omitzeroOK: true},
	{typ: "[]string", value: `["a","b"]`, omitzeroOK: true},
	{typ: "[]int32", value: `[-1,0,2147483647]`, omitzeroOK: true},
	{typ: "[]Child", value: `[{"a":1,"b":"x"},{"a":2,"b":"y"}]`, omitzeroOK: true},
	{typ: "[]*Child", value: `[{"a":3,"b":"z"},null]`, omitzeroOK: true},
	{typ: "[]CustomJSON", value: `[1,2,3]`, omitzeroOK: true},
	{typ: "[]*TextDate", value: `["date",null]`, omitzeroOK: true},
	{typ: "[]byte", value: `"AQID"`, omitzeroOK: true},
	{typ: "[]byte", value: `[0,1,255]`, omitzeroOK: true},
	{typ: "*[]byte", value: `[4,5,6]`, omitzeroOK: true},
	{typ: "[][]byte", value: `["AQI=",[3,4],null]`, omitzeroOK: true},
	{typ: "map[string]int", value: `{"a":1,"b":2}`, omitzeroOK: true},
	{typ: "map[Key]int32", value: `{"a":-1,"b":2}`, omitzeroOK: true},
	{typ: "map[string]Child", value: `{"a":{"a":1,"b":"map"}}`, omitzeroOK: true},
	{typ: "map[string]CustomJSON", value: `{"a":1,"b":2}`, omitzeroOK: true},
	{typ: "map[string]TextDate", value: `{"a":"date"}`, omitzeroOK: true},
	{typ: "map[string][]byte", value: `{"a":"AQI=","b":[3,4]}`, omitzeroOK: true},
	{typ: "map[string]struct{}", value: `{"a":{},"b":{"ignored":1}}`, omitzeroOK: true},
	{typ: "any", value: `{"nested":[1,"x",true,null]}`, omitzeroOK: true},
}

type generatedSchema struct {
	name   string
	decl   string
	inputs []string
}

func schemaCampaignConfig(t *testing.T) (uint64, int) {
	t.Helper()
	seed := defaultSchemaSeed
	count := defaultSchemaCount
	if value := os.Getenv("ZEROJSON_SCHEMA_SEED"); value != "" {
		parsed, err := strconv.ParseUint(value, 0, 64)
		if err != nil {
			t.Fatalf("ZEROJSON_SCHEMA_SEED: %v", err)
		}
		seed = parsed
	}
	if value := os.Getenv("ZEROJSON_SCHEMA_COUNT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			t.Fatalf("ZEROJSON_SCHEMA_COUNT must be in [1,500], got %q", value)
		}
		count = parsed
	}
	return seed, count
}

func buildGeneratedSchemas(seed uint64, count int) []generatedSchema {
	rng := schemaRNG(seed)
	schemas := make([]generatedSchema, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("Schema%03d", i)
		fieldCount := 4 + rng.n(6)
		embed := rng.n(3) == 0
		var decl strings.Builder
		fmt.Fprintf(&decl, "type %s struct {\n", name)
		members := make([]string, 0, fieldCount+2)
		if embed {
			decl.WriteString("\tEmbed\n")
			members = append(members, `"embed_s":"embedded"`, `"embed_n":42`)
		}
		for field := 0; field < fieldCount; field++ {
			atom := schemaAtoms[rng.n(len(schemaAtoms))]
			jsonName := fmt.Sprintf("f_%d", field)
			if rng.n(5) == 0 {
				jsonName = fmt.Sprintf("f-%d", field)
			}
			option := atom.forcedOption
			if option == "" {
				switch rng.n(5) {
				case 1:
					option = "omitempty"
				case 2:
					if atom.omitzeroOK {
						option = "omitzero"
					}
				}
			}
			tag := jsonName
			if option != "" {
				tag += "," + option
			}
			zeroTag := ""
			if atom.internOK && rng.n(4) == 0 {
				zeroTag = " zerojson:\"intern\""
			}
			fmt.Fprintf(&decl, "\tF%d %s `json:\"%s\"%s`\n", field, atom.typ, tag, zeroTag)
			members = append(members, strconv.Quote(jsonName)+":"+atom.value)
		}
		decl.WriteString("}\n")

		full := "{" + strings.Join(members, ",") + "}"
		reversed := append([]string(nil), members...)
		for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
			reversed[left], reversed[right] = reversed[right], reversed[left]
		}
		inputs := []string{
			`{}`,
			`null`,
			full,
			"{ " + strings.Join(reversed, " , ") + " }",
			"{" + strings.Join(append(append([]string(nil), members...), `"unknown":{"x":[1,2,3]}`), ",") + "}",
		}
		if len(members) > 0 {
			inputs = append(inputs, "{"+strings.Join(append(append([]string(nil), members...), members[0]), ",")+"}")
		}
		schemas = append(schemas, generatedSchema{name: name, decl: decl.String(), inputs: inputs})
	}
	return schemas
}

const schemaPackagePreamble = `package p

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	decimal "github.com/neal/zerojson/conformance/internal/testleaf"
)

var _ = time.Second
var _ = uuid.Nil
var _ = decimal.Zero

type Embed struct {
	EmbeddedS string ` + "`json:\"embed_s\"`" + `
	EmbeddedN int    ` + "`json:\"embed_n\"`" + `
}

type Child struct {
	A int    ` + "`json:\"a\"`" + `
	B string ` + "`json:\"b\"`" + `
}

type Key string
type Mode string
const ModeUp Mode = "up"

type Quoted int64
func (v Quoted) MarshalJSON() ([]byte, error) { return []byte(` + "`\"`" + ` + strconv.FormatInt(int64(v), 10) + ` + "`\"`" + `), nil }
func (Quoted) ZeroJSONQuotedInt64() {}
func (v *Quoted) UnmarshalJSON(data []byte) error {
	n, err := strconv.ParseInt(strings.Trim(string(data), ` + "`\"`" + `), 10, 64)
	if err == nil { *v = Quoted(n) }
	return err
}

type TextDate int
func (TextDate) MarshalText() ([]byte, error) { return []byte("date"), nil }
func (v *TextDate) UnmarshalText(data []byte) error { if string(data) == "date" { *v = 1 }; return nil }

type CustomJSON int
func (v CustomJSON) MarshalJSON() ([]byte, error) { return strconv.AppendInt(nil, int64(v), 10), nil }
func (v *CustomJSON) UnmarshalJSON(data []byte) error { var n int; if err := json.Unmarshal(data, &n); err != nil { return err }; *v = CustomJSON(n); return nil }

`

func schemaBehaviorSource(seed uint64, schemas []generatedSchema) string {
	var b strings.Builder
	b.WriteString(`package p

import (
	"encoding/json"
	"reflect"
	"testing"
)

type schemaZJSON interface {
	AppendZJSON([]byte) ([]byte, error)
	UnmarshalZJSON([]byte) error
}

type schemaCase struct {
	name string
	inputs [][]byte
	newStd func() any
	newZJ func() schemaZJSON
}

func schemaSemanticEqual(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil { return false }
	return reflect.DeepEqual(av, bv)
}

func TestGeneratedSchemaCampaign(t *testing.T) {
`)
	fmt.Fprintf(&b, "\tt.Logf(\"schema campaign seed=%#x count=%d\")\n", seed, len(schemas))
	b.WriteString("\tcases := []schemaCase{\n")
	for _, schema := range schemas {
		fmt.Fprintf(&b, "\t\t{name: %q, inputs: [][]byte{", schema.name)
		for _, input := range schema.inputs {
			fmt.Fprintf(&b, "[]byte(%q),", input)
		}
		fmt.Fprintf(&b, "}, newStd: func() any { return &%s{} }, newZJ: func() schemaZJSON { return &%s{} }},\n", schema.name, schema.name)
	}
	b.WriteString(`	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for index, input := range tc.inputs {
				std, zj := tc.newStd(), tc.newZJ()
				stdErr := json.Unmarshal(input, std)
				zjErr := zj.UnmarshalZJSON(input)
				if (stdErr != nil) != (zjErr != nil) {
					t.Fatalf("input[%d]=%s error mismatch: stdlib=%v zerojson=%v", index, input, stdErr, zjErr)
				}
				if stdErr != nil { continue }
				stdJSON, err := json.Marshal(std)
				if err != nil { t.Fatal(err) }
				zjJSON, err := zj.AppendZJSON(nil)
				if err != nil { t.Fatal(err) }
				if !schemaSemanticEqual(stdJSON, zjJSON) {
					t.Fatalf("input[%d]=%s semantic mismatch\nstdlib: %s\nzerojson: %s", index, input, stdJSON, zjJSON)
				}
				stdAgain := tc.newStd()
				if err := json.Unmarshal(zjJSON, stdAgain); err != nil {
					t.Fatalf("stdlib rejected zerojson output %s: %v", zjJSON, err)
				}
			}
		})
	}
}
`)
	return b.String()
}

func TestGeneratedSchemaCampaign(t *testing.T) {
	if testing.Short() {
		t.Skip("schema campaign generates and tests a temporary package")
	}
	seed, count := schemaCampaignConfig(t)
	schemas := buildGeneratedSchemas(seed, count)
	t.Logf("schema campaign seed=%#x count=%d", seed, count)

	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjschemas")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	var typesSource strings.Builder
	typesSource.WriteString(schemaPackagePreamble)
	typeNames := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		typesSource.WriteString(schema.decl)
		typesSource.WriteByte('\n')
		typeNames = append(typeNames, schema.name)
	}
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(typesSource.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema_test.go"), []byte(schemaBehaviorSource(seed, schemas)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(gen,
		"-dir", dir,
		"-types", strings.Join(typeNames, ","),
		"-leaf", testLeafSpec,
		"-pool",
		"-out", filepath.Join(dir, "zerojson_gen.go"),
		"-tests", filepath.Join(dir, "fuzz_gen_test.go"),
	)
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed for seed %#x: %v\n%s", seed, err, out)
	}

	cmd = exec.Command("go", "test", "-count=1", "-timeout=3m", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated schema campaign failed; reproduce with ZEROJSON_SCHEMA_SEED=%#x ZEROJSON_SCHEMA_COUNT=%d\n%v\n%s", seed, count, err, out)
	}
}
