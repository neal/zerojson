//go:build ignore

// Command gen emits the type-shape enumeration matrix: a programmatically
// constructed family of struct types covering the cross product of field
// KIND x tag VARIANT x value STATE that the generator's classification and
// composite-value semantics must handle. It is the type-shape-domain analog
// of the runtime's value-domain enumerations (see ../../DOMAINS.md): the
// scalar work enumerated VALUE domains; this enumerates the generator's
// input TYPE-SHAPE domain.
//
// For every generated type it emits:
//   - the zerojson target struct (matrix_gen.go), classified & codec'd by
//     zerojsongen;
//   - an easyjson twin (oracle_gen.go) so each shape has a real, independently
//     generated easyjson oracle alongside encoding/json;
//   - a registry entry (matrix_gen.go: matrixCases) wiring the typed
//     encode/decode/convert closures and the per-cell comparison policy
//     (byte-identical vs stdlib, byte-identical vs easyjson, or the
//     documented-divergence relaxations — the SAME allowlist the README's
//     divergence table states).
//
// Run via `go generate ./...` in this directory. It writes the source files
// and then invokes zerojsongen and easyjson to produce the committed codecs.
package main

import (
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Divergence reasons — the single source of truth shared with the README's
// divergence table and asserted (as a closed set) by the matrix test.
const (
	divNone       = ""
	divMapOrder   = "map-key range order (vs stdlib's sorted keys): perf, semantically meaningless"
	divEmbedOrder = "promoted-field order (vs easyjson): stdlib/json-v2 position, not easyjson's after-direct-fields"
	divOmitzero   = "omitzero honored (vs easyjson, which ignores the option)"
	divInvalidTag = "invalid tag name falls back to the Go field name (vs easyjson, which emits the invalid tag verbatim)"
	testLeafSpec  = "github.com/neal/zerojson/conformance/internal/testleaf.Decimal=github.com/neal/zerojson/conformance/internal/testleaf.Append,github.com/neal/zerojson/conformance/internal/testleaf.Read"
)

// cell is one type-shape row.
type cell struct {
	name   string // struct type name
	fields string // struct body (Go source)
	full   string // statements populating v (*name) into the "full" state
	empty  string // statements for the "empty containers" state ("" if none)
	// comparison policy
	stdSemantic bool   // compare vs stdlib semantically (decode both) not byte-for-byte
	stdReason   string // why stdlib byte-identity is relaxed (allowlist)
	ej          bool   // generate an easyjson twin and compare against it
	ejSemantic  bool   // compare vs easyjson semantically not byte-for-byte
	ejReason    string // why easyjson byte-identity is relaxed (allowlist)
}

// leaf describes a scalar/leaf field type and a round-trippable populated value.
type leaf struct {
	id   string
	typ  string
	full string // expression for a populated value
	ptr  bool   // also emit a pointer variant
}

func leaves() []leaf {
	return []leaf{
		{"string", "string", `"hi"`, true},
		{"bool", "bool", `true`, true},
		{"int", "int", `7`, true},
		{"int32", "int32", `int32(-9)`, true},
		{"uint16", "uint16", `uint16(65000)`, true},
		{"float64", "float64", `2.5`, true},
		{"float32", "float32", `float32(1.5)`, true},
		{"enum", "Mode", `ModeUp`, true},
		{"quotedint", "StrInt64", `StrInt64(42)`, true},
		{"time", "time.Time", `sampleTime`, true},
		{"uuid", "uuid.UUID", `sampleUUID`, true},
		{"decimal", "decimal.Decimal", `sampleDec`, true},
		{"text", "YMD", `YMD{Y: 2021, M: 3, D: 4}`, true},
		{"marshaler", "Tags", `Tags{"a", "b"}`, true},
		{"appender", "Cents", `Cents(12345)`, true},
	}
}

func buildCells() []cell {
	var cells []cell

	// Leaf kinds: plain (non-omitempty), omitempty, and pointer variants.
	// Marshaler/TextMarshaler/appender values are already compact so their
	// verbatim-vs-recompact behavior coincides with stdlib here.
	for _, l := range leaves() {
		cells = append(cells, cell{
			name:   "L_" + l.id,
			fields: "F " + l.typ + " `json:\"f\"`",
			full:   "v.F = " + l.full,
			ej:     true,
		})
		cells = append(cells, cell{
			name:   "L_" + l.id + "_oe",
			fields: "F " + l.typ + " `json:\"f,omitempty\"`",
			full:   "v.F = " + l.full,
			ej:     true,
		})
		if l.ptr {
			cells = append(cells, cell{
				name:   "L_" + l.id + "_ptr",
				fields: "F *" + l.typ + " `json:\"f\"`",
				full:   "x := " + l.full + "\n\tv.F = &x",
				ej:     true,
			})
		}
	}

	// Container kinds: nil (zero state) vs empty-non-nil vs populated — a nil
	// non-omitempty container encodes as null, an empty non-nil one as []/{}.
	container := func(name, typ, full, empty string, opts ...func(*cell)) {
		c := cell{name: name, fields: "F " + typ + " `json:\"f\"`", full: "v.F = " + full, empty: "v.F = " + empty, ej: true}
		for _, o := range opts {
			o(&c)
		}
		cells = append(cells, c)
		// omitempty twin (nil and empty both omit; only populated emits).
		oe := c
		oe.name = name + "_oe"
		oe.fields = "F " + typ + " `json:\"f,omitempty\"`"
		cells = append(cells, oe)
	}
	mapDiv := func(c *cell) {
		c.stdSemantic = true
		c.stdReason = divMapOrder
		c.ejSemantic = true
		c.ejReason = divMapOrder
	}

	container("C_slice_string", "[]string", `[]string{"x", "y"}`, `[]string{}`)
	container("C_slice_int", "[]int", `[]int{1, 2}`, `[]int{}`)
	container("C_slice_struct", "[]Inner", `[]Inner{{A: 1, B: "b"}}`, `[]Inner{}`)
	container("C_slice_ptrstruct", "[]*Inner", `[]*Inner{{A: 1, B: "b"}, nil}`, `[]*Inner{}`)
	container("C_slice_decimal", "[]decimal.Decimal", `[]decimal.Decimal{sampleDec}`, `[]decimal.Decimal{}`)
	container("C_bytes", "[]byte", `[]byte{1, 2, 3}`, `[]byte{}`)
	container("C_bytes_named", "Blob", `Blob{4, 5}`, `Blob{}`)
	container("C_bytes_nested_slice", "[][]byte", `[][]byte{{1}, {2}}`, `[][]byte{}`)
	container("C_map_int", "map[string]int", `map[string]int{"k": 1, "j": 2}`, `map[string]int{}`, mapDiv)
	container("C_map_decimal", "map[string]decimal.Decimal", `map[string]decimal.Decimal{"k": sampleDec}`, `map[string]decimal.Decimal{}`, mapDiv)
	container("C_map_struct", "map[string]Inner", `map[string]Inner{"k": {A: 1, B: "b"}}`, `map[string]Inner{}`, mapDiv)
	container("C_map_set", "map[string]struct{}", `map[string]struct{}{"k": {}}`, `map[string]struct{}{}`, mapDiv)
	container("C_map_bytes", "map[string][]byte", `map[string][]byte{"k": {1, 2}}`, `map[string][]byte{}`, mapDiv)

	// *[]byte pointer variant: null when the pointer is nil.
	cells = append(cells, cell{
		name:   "C_bytes_ptr",
		fields: "F *[]byte `json:\"f\"`",
		full:   "x := []byte{9, 8}\n\tv.F = &x",
		ej:     true,
	})

	// Nested struct value / pointer, any, and json.RawMessage (the stdlib-
	// native passthrough; zerojson classifies it as a Marshaler and appends
	// its already-compact bytes verbatim, matching stdlib).
	cells = append(cells,
		cell{name: "N_struct", fields: "F Inner `json:\"f\"`", full: `v.F = Inner{A: 1, B: "b"}`, ej: true},
		cell{name: "N_struct_ptr", fields: "F *Inner `json:\"f\"`", full: `v.F = &Inner{A: 1, B: "b"}`, ej: true},
		cell{name: "N_any", fields: "F any `json:\"f\"`", full: `v.F = "anyval"`, ej: true},
		cell{name: "N_rawmsg", fields: "F json.RawMessage `json:\"f\"`", full: "v.F = json.RawMessage(`{\"a\":1}`)", ej: true},
	)

	// Embedding: exported value embed declared LAST (coincides with
	// easyjson), exported value embed declared FIRST (embed-order divergence
	// vs easyjson), and an UNEXPORTED value embed whose exported field is
	// promoted. All three match stdlib byte-for-byte.
	cells = append(cells,
		cell{name: "E_embed_last", fields: "First string `json:\"first,omitempty\"`\n\tAudit", full: `v.First = "f"
	v.CreatedBy = "c"
	v.Version = 3`, ej: true},
		cell{name: "E_embed_first", fields: "Audit\n\tLast string `json:\"last,omitempty\"`", full: `v.Last = "l"
	v.CreatedBy = "c"
	v.Version = 3`, ej: true, ejSemantic: true, ejReason: divEmbedOrder},
		cell{name: "E_embed_unexported", fields: "hidden\n\tOwn string `json:\"own,omitempty\"`", full: `v.Visible = 7
	v.Own = "o"`, ej: true, ejSemantic: true, ejReason: divEmbedOrder},
	)

	// (Wrong-signature interface methods are verified in the temp-dir compile
	// matrix, ../compile_test.go: a field of a type with a wrong-signature
	// MarshalJSON that COMPILES proves the generator ignored the method and
	// emitted a structural codec — had it misclassified the type as a
	// Marshaler, the emitted `.MarshalJSON()` call would fail to type-check.
	// That fixture lives in a generated temp package so `go vet` never sees
	// the deliberately-wrong method.)

	// Tag policy (invalid tag name and the ,string option): an invalid tag
	// NAME falls back to the Go field name; ,string quotes int/uint. Both
	// match stdlib.
	cells = append(cells,
		cell{name: "T_invalid_tagname", fields: "A int `json:\"a\\\\b\"`\n\tB int `json:\"ok\"`", full: `v.A = 1
	v.B = 2`, ej: false, ejReason: divInvalidTag},
		cell{name: "T_string_opt", fields: "C uint32 `json:\"c,string\"`\n\tA int64 `json:\"a,string\"`", full: `v.C = 5
	v.A = -6`, ej: true},
	)

	// omitzero (Go 1.24 stdlib parity; easyjson ignores it, so compare vs
	// stdlib only). Exercises the nil-vs-empty slice distinction omitzero
	// keeps but omitempty drops.
	cells = append(cells,
		cell{name: "Z_omitzero", fields: "W time.Time `json:\"w,omitzero\"`\n\tES []string `json:\"es,omitzero\"`\n\tN int `json:\"n,omitzero\"`", full: `v.W = sampleTime
	v.ES = []string{}
	v.N = 4`, empty: `v.ES = []string{}`, ej: false, ejReason: divOmitzero},
	)

	return cells
}

// NOTE: this header deliberately does NOT use the "Code generated ... DO NOT
// EDIT." marker that go/ast.IsGenerated matches: the generator treats methods
// declared in an ast-generated file as replaceable easyjson output (and would
// then classify these helper types — YMD, Tags, Cents, … — as auto-discovered
// structs instead of respecting their hand-written Marshaler/TextMarshaler/
// Appender methods). This file is machine-produced but its methods must read
// as hand-written to zerojsongen.
const preamble = `// Code generated by gen.go. DO NOT EDIT.

package typeshape

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"

	"github.com/neal/zerojson"
)

// Deterministic, round-trippable sample values shared by populated states.
var (
	sampleTime = time.Date(2021, 3, 4, 5, 6, 7, 890000000, time.UTC)
	sampleUUID = uuid.MustParse("12345678-1234-1234-1234-1234567890ab")
	sampleDec  = decimal.RequireFromString("12.34")
)

// Mode is a string enum.
type Mode string

const ModeUp Mode = "up"

// StrInt64 marshals as a quoted string (quoted-int64 convention).
type StrInt64 int64

func (i StrInt64) MarshalJSON() ([]byte, error) {
	return []byte(` + "`\"`" + ` + strconv.FormatInt(int64(i), 10) + ` + "`\"`" + `), nil
}
func (StrInt64) ZeroJSONQuotedInt64() {}
func (i *StrInt64) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), ` + "`\"`" + `), 10, 64)
	if err != nil {
		return err
	}
	*i = StrInt64(v)
	return nil
}

// Tags is a slice-shaped custom Marshaler (comma-joined).
type Tags []string

func (t Tags) MarshalJSON() ([]byte, error) { return json.Marshal(strings.Join(t, ",")) }
func (t *Tags) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*t = nil
	} else {
		*t = strings.Split(s, ",")
	}
	return nil
}

// YMD is a TextMarshaler.
type YMD struct{ Y, M, D int }

func (d YMD) MarshalText() ([]byte, error) {
	return []byte(strconv.Itoa(d.Y*10000 + d.M*100 + d.D)), nil
}
func (d *YMD) UnmarshalText(b []byte) error {
	n, err := strconv.Atoi(string(b))
	if err != nil {
		return err
	}
	d.Y, d.M, d.D = n/10000, n/100%100, n%100
	return nil
}

// Cents implements the zerojson Appender/Reader pair and MarshalJSON/
// UnmarshalJSON (both quoted-int); the generator gives AppendZJSON/ReadZJSON
// precedence.
type Cents int64

func (c Cents) MarshalJSON() ([]byte, error) {
	return []byte(` + "`\"`" + ` + strconv.FormatInt(int64(c), 10) + ` + "`\"`" + `), nil
}
func (c *Cents) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), ` + "`\"`" + `), 10, 64)
	if err != nil {
		return err
	}
	*c = Cents(v)
	return nil
}
func (c Cents) AppendZJSON(dst []byte) ([]byte, error) {
	dst = append(dst, '"')
	dst = strconv.AppendInt(dst, int64(c), 10)
	return append(dst, '"'), nil
}
func (c *Cents) ReadZJSON(l *zerojson.Lexer) {
	b := l.ReadNumericBytes()
	if l.Err() != nil {
		return
	}
	v, err := strconv.ParseInt(strings.Trim(string(b), ` + "`\"`" + `), 10, 64)
	if err != nil {
		l.Fail("cents: %v", err)
		return
	}
	*c = Cents(v)
}

// Blob is a named byte slice (base64, like []byte).
type Blob []byte

// Inner is an auto-discovered nested struct.
type Inner struct {
	A int    ` + "`json:\"a\"`" + `
	B string ` + "`json:\"b\"`" + `
}

// Audit is an exported embeddable struct (promoted fields).
type Audit struct {
	CreatedBy string ` + "`json:\"created_by,omitempty\"`" + `
	Version   int    ` + "`json:\"version,omitempty\"`" + `
}

// hidden is an UNEXPORTED embeddable struct with an exported (promoted)
// field and an unexported (ignored) field.
type hidden struct {
	Visible int ` + "`json:\"visible\"`" + `
	secret  int
}

`

func main() {
	cells := buildCells()

	// matrix_gen.go — helper types + the matrix struct types. This is a
	// NON-test file (the codegen tools compile it), so it must NOT reference
	// any generated method (AppendZJSON etc.): those don't exist until
	// zerojsongen runs. The registry that DOES reference them lives in a
	// _test.go, compiled only at `go test` time (after codegen).
	var b strings.Builder
	b.WriteString(preamble)
	b.WriteString("\n// ---- matrix struct types ----\n")
	for _, c := range cells {
		fmt.Fprintf(&b, "\ntype %s struct {\n\t%s\n}\n", c.name, c.fields)
	}
	writeFile("matrix_gen.go", b.String())

	// matrix_cases_test.go — the registry, wiring the generated codecs and
	// easyjson twins into typed closures. Test-only, so it can reference the
	// generated methods.
	var r strings.Builder
	r.WriteString("// Code generated by gen.go. DO NOT EDIT.\n\npackage typeshape\n\nimport (\n\t\"encoding/json\"\n\n\tdecimal \"github.com/neal/zerojson/conformance/internal/testleaf\"\n)\n\n")
	r.WriteString("var matrixCases = []matrixCase{\n")
	for _, c := range cells {
		writeCase(&r, c)
	}
	r.WriteString("}\n")
	writeFile("matrix_cases_test.go", r.String())

	// oracle_gen.go — easyjson twins for cells with ej=true.
	var o strings.Builder
	o.WriteString("// Code generated by gen.go. DO NOT EDIT.\n\npackage typeshape\n\n")
	for _, c := range cells {
		if c.ej {
			fmt.Fprintf(&o, "//easyjson:json\ntype %sEJ %s\n\n", c.name, c.name)
		}
	}
	writeFile("oracle_gen.go", o.String())

	// Type list for zerojsongen (only the top-level cell structs; Inner is
	// auto-discovered).
	names := make([]string, len(cells))
	for i, c := range cells {
		names[i] = c.name
	}
	sort.Strings(names)

	// zerojsongen first: it tolerates its own stale output (a previous
	// zerojson_gen.go that no longer compiles against the runtime is
	// blanked out while the package is loaded), whereas easyjson's
	// bootstrap compiles the whole package and would fail on that file.
	run("zerojsongen", "go", "run", "github.com/neal/zerojson/cmd/zerojsongen",
		"-dir", ".", "-types", strings.Join(names, ","), "-leaf", testLeafSpec,
		"-out", "zerojson_gen.go")
	run("easyjson", "go", "run", "github.com/mailru/easyjson/easyjson", "-all", "oracle_gen.go")
	fmt.Printf("typeshape matrix: %d cells\n", len(cells))
}

// writeCase emits one matrixCase literal for cell c.
func writeCase(b *strings.Builder, c cell) {
	fmt.Fprintf(b, "\t{\n\t\tname: %q,\n", c.name)
	// states
	b.WriteString("\t\tstates: []matrixState{\n")
	fmt.Fprintf(b, "\t\t\t{\"zero\", func() any { return &%s{} }},\n", c.name)
	fmt.Fprintf(b, "\t\t\t{\"full\", func() any { v := &%s{}\n\t\t\t\t%s\n\t\t\t\treturn v }},\n", c.name, c.full)
	if c.empty != "" {
		fmt.Fprintf(b, "\t\t\t{\"empty\", func() any { v := &%s{}\n\t\t\t\t%s\n\t\t\t\treturn v }},\n", c.name, c.empty)
	}
	b.WriteString("\t\t},\n")
	fmt.Fprintf(b, "\t\tencZJ: func(v any) ([]byte, error) { return v.(*%s).AppendZJSON(nil) },\n", c.name)
	fmt.Fprintf(b, "\t\tdecZJ: func(d []byte) (any, error) { v := &%s{}; return v, v.UnmarshalZJSON(d) },\n", c.name)
	if c.ej {
		fmt.Fprintf(b, "\t\tencEJ: func(v any) ([]byte, error) { return json.Marshal(%sEJ(*v.(*%s))) },\n", c.name, c.name)
	}
	if c.stdSemantic {
		fmt.Fprintf(b, "\t\tstdSemantic: true,\n\t\tstdReason: %q,\n", c.stdReason)
	}
	if c.ejSemantic {
		fmt.Fprintf(b, "\t\tejSemantic: true,\n\t\tejReason: %q,\n", c.ejReason)
	}
	if !c.ej {
		fmt.Fprintf(b, "\t\tejReason: %q,\n", c.ejReason)
	}
	b.WriteString("\t},\n")
}

func writeFile(name, src string) {
	formatted, err := format.Source([]byte(src))
	if err != nil {
		// Write the unformatted source for debugging, then fail.
		_ = os.WriteFile(name, []byte(src), 0o644)
		fmt.Fprintf(os.Stderr, "format %s: %v\n", name, err)
		os.Exit(1)
	}
	if err := os.WriteFile(name, formatted, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(label, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", label, err)
		os.Exit(1)
	}
}
