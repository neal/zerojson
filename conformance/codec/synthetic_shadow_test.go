package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// synthetic_shadow_test.go replaces unavailable production captures with an
// explicit wire model. The per-commit tier covers every pair of dimension
// levels; ZEROJSON_SYNTHETIC=full covers every triple plus bounded deep, wide,
// long, and duplicate-heavy documents. Grammar-invalid inputs remain the job
// of the existing Valid/truncation/strict-skip suites; this model includes both
// schema-valid and schema-invalid-but-JSON-valid records.

const syntheticFullEnv = "ZEROJSON_SYNTHETIC"

type syntheticFactor struct {
	name   string
	levels []string
}

const (
	synthOp = iota
	synthSet
	synthWhere
	synthOrder
	synthUnknown
	synthWhitespace
	synthLateMember
	synthKeyStyle
)

func syntheticFactors() []syntheticFactor {
	return []syntheticFactor{
		{name: "op", levels: []string{
			"null", "empty", "ascii", "escaped", "unicode", "lone_surrogate",
			"long", "escaped_controls", "number_wrong", "bool_wrong", "object_wrong", "array_wrong",
		}},
		{name: "set", levels: syntheticMapLevelNames()},
		{name: "where", levels: syntheticMapLevelNames()},
		{name: "order", levels: []string{
			"op_set_where_unknown", "where_set_op_unknown", "unknown_op_set_where",
			"op_unknown_where_set", "set_where_unknown_op", "where_unknown_op_set",
		}},
		{name: "unknown", levels: []string{"absent", "scalar", "nested", "array", "unicode_deep"}},
		{name: "whitespace", levels: []string{"compact", "spaces", "multiline"}},
		{name: "late_member", levels: []string{
			"none", "op_same", "op_override", "set_empty", "set_override", "where_empty", "where_override",
		}},
		{name: "key_style", levels: []string{"literal", "op_escaped", "set_escaped", "where_escaped", "all_escaped"}},
	}
}

func syntheticMapLevelNames() []string {
	return []string{
		"absent", "null", "empty_object", "flat", "all_scalars", "nested",
		"arrays", "unicode", "numeric_boundaries", "empty_containers",
		"string_wrong", "array_wrong", "number_wrong", "bool_wrong",
	}
}

func TestSyntheticShadowPairwise(t *testing.T) {
	rows := syntheticCoverageRows(t, 2)
	if len(rows) < 1000 {
		t.Fatalf("pairwise model unexpectedly small: %d rows", len(rows))
	}
	runSyntheticShadowRows(t, rows)
	t.Logf("shadowed %d rows covering every pair across %d wire dimensions", len(rows), len(syntheticFactors()))
}

func TestSyntheticShadowThreeWise(t *testing.T) {
	if os.Getenv(syntheticFullEnv) != "full" {
		t.Skip("run scripts/synthetic-shadow.sh full")
	}
	rows := syntheticCoverageRows(t, 3)
	if len(rows) < 10000 {
		t.Fatalf("three-wise model unexpectedly small: %d rows", len(rows))
	}
	runSyntheticShadowRows(t, rows)
	t.Logf("shadowed %d rows covering every triple across %d wire dimensions", len(rows), len(syntheticFactors()))
}

func TestSyntheticShadowAdversarial(t *testing.T) {
	if os.Getenv(syntheticFullEnv) != "full" {
		t.Skip("run scripts/synthetic-shadow.sh full")
	}
	cases := syntheticAdversarialRecords()
	var pooledCopy, pooledBorrow MapFieldsLikeDecoder
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			shadowCompare(t, shadowVariant(tc), &pooledCopy, &pooledBorrow)
		})
	}
}

func syntheticFuzzSeeds() [][]byte {
	factors := syntheticFactors()
	maxLevels := 0
	for _, factor := range factors {
		maxLevels = max(maxLevels, len(factor.levels))
	}
	seeds := make([][]byte, 0, maxLevels)
	for i := 0; i < maxLevels; i++ {
		row := make([]int, len(factors))
		for fi, factor := range factors {
			row[fi] = (i + fi) % len(factor.levels)
		}
		seeds = append(seeds, renderSyntheticRow(row))
	}
	return seeds
}

func runSyntheticShadowRows(t *testing.T, rows [][]int) {
	t.Helper()
	var pooledCopy, pooledBorrow MapFieldsLikeDecoder
	for _, row := range rows {
		data := renderSyntheticRow(row)
		name := syntheticRowName(row)
		t.Run(name, func(t *testing.T) {
			shadowCompare(t, shadowVariant{name: name, data: data}, &pooledCopy, &pooledBorrow)
		})
	}
}

func syntheticRowName(row []int) string {
	factors := syntheticFactors()
	var b strings.Builder
	for fi, level := range row {
		if fi > 0 {
			b.WriteString("__")
		}
		b.WriteString(factors[fi].name)
		b.WriteByte('_')
		b.WriteString(factors[fi].levels[level])
	}
	return b.String()
}

// syntheticCoverageRows constructs a deterministic covering array. For every
// chosen pair/triple of factors and every tuple of their levels, it emits a row
// containing that tuple and deterministically fills the remaining factors.
// Identical full rows are deduplicated; coverage is verified independently.
func syntheticCoverageRows(t *testing.T, strength int) [][]int {
	t.Helper()
	factors := syntheticFactors()
	if strength < 1 || strength > len(factors) {
		t.Fatalf("invalid synthetic coverage strength %d", strength)
	}
	var rows [][]int
	seenRows := make(map[string]bool)
	for _, combo := range factorCombinations(len(factors), strength) {
		forEachLevelTuple(factors, combo, func(levels []int) {
			row := make([]int, len(factors))
			var seed uint64 = 1469598103934665603
			for i, fi := range combo {
				seed ^= uint64((fi + 1) * (levels[i] + 17))
				seed *= 1099511628211
			}
			for fi, factor := range factors {
				row[fi] = int((seed + uint64((fi+1)*7919)) % uint64(len(factor.levels)))
			}
			for i, fi := range combo {
				row[fi] = levels[i]
			}
			key := syntheticRowKey(row)
			if !seenRows[key] {
				seenRows[key] = true
				rows = append(rows, row)
			}
		})
	}
	verifySyntheticCoverage(t, rows, strength)
	return rows
}

func verifySyntheticCoverage(t *testing.T, rows [][]int, strength int) {
	t.Helper()
	factors := syntheticFactors()
	for _, combo := range factorCombinations(len(factors), strength) {
		product := 1
		for _, fi := range combo {
			product *= len(factors[fi].levels)
		}
		seen := make([]bool, product)
		for _, row := range rows {
			index := 0
			stride := 1
			for i := len(combo) - 1; i >= 0; i-- {
				fi := combo[i]
				if row[fi] < 0 || row[fi] >= len(factors[fi].levels) {
					t.Fatalf("factor %s level %d out of range", factors[fi].name, row[fi])
				}
				index += row[fi] * stride
				stride *= len(factors[fi].levels)
			}
			seen[index] = true
		}
		for index, ok := range seen {
			if !ok {
				t.Fatalf("missing strength-%d interaction for factors %v at tuple index %d", strength, combo, index)
			}
		}
	}
}

func factorCombinations(n, choose int) [][]int {
	var out [][]int
	var walk func(start int, current []int)
	walk = func(start int, current []int) {
		if len(current) == choose {
			out = append(out, append([]int(nil), current...))
			return
		}
		for i := start; i <= n-(choose-len(current)); i++ {
			walk(i+1, append(current, i))
		}
	}
	walk(0, nil)
	return out
}

func forEachLevelTuple(factors []syntheticFactor, combo []int, fn func([]int)) {
	levels := make([]int, len(combo))
	var walk func(int)
	walk = func(pos int) {
		if pos == len(combo) {
			fn(append([]int(nil), levels...))
			return
		}
		for level := range len(factors[combo[pos]].levels) {
			levels[pos] = level
			walk(pos + 1)
		}
	}
	walk(0)
}

func syntheticRowKey(row []int) string {
	var b strings.Builder
	for _, level := range row {
		b.WriteByte(byte(level + 1))
	}
	return b.String()
}

type syntheticMember struct {
	key   string
	value string
}

func renderSyntheticRow(row []int) []byte {
	if len(row) != len(syntheticFactors()) {
		panic("synthetic row width mismatch")
	}
	keys := map[string]string{"op": `"op"`, "set": `"set"`, "where": `"where"`}
	applySyntheticKeyStyle(keys, row[synthKeyStyle])
	members := map[string]*syntheticMember{
		"op": {key: keys["op"], value: syntheticOpValue(row[synthOp])},
	}
	if value, present := syntheticMapValue(row[synthSet]); present {
		members["set"] = &syntheticMember{key: keys["set"], value: value}
	}
	if value, present := syntheticMapValue(row[synthWhere]); present {
		members["where"] = &syntheticMember{key: keys["where"], value: value}
	}
	if member, present := syntheticUnknownMember(row[synthUnknown]); present {
		members["unknown"] = &member
	}

	orders := [][]string{
		{"op", "set", "where", "unknown"},
		{"where", "set", "op", "unknown"},
		{"unknown", "op", "set", "where"},
		{"op", "unknown", "where", "set"},
		{"set", "where", "unknown", "op"},
		{"where", "unknown", "op", "set"},
	}
	var ordered []syntheticMember
	for _, name := range orders[row[synthOrder]] {
		if member := members[name]; member != nil {
			ordered = append(ordered, *member)
		}
	}
	if late, present := syntheticLateMember(row[synthLateMember], members["op"]); present {
		ordered = append(ordered, late)
	}
	return renderSyntheticMembers(ordered, row[synthWhitespace])
}

func syntheticOpValue(level int) string {
	long, _ := json.Marshal(strings.Repeat("operation-", 128))
	values := []string{
		"null", `""`, `"INSERT"`, `"UP\u0044ATE"`, `"同步"`, `"\ud800"`,
		string(long), `"line\nquote\"slash\\"`, "42", "true", `{"wrong":1}`, `["wrong"]`,
	}
	return values[level]
}

func syntheticMapValue(level int) (string, bool) {
	values := []string{
		"", "null", `{}`, `{"id":"x","count":1}`,
		`{"string":"x","integer":-17,"float":1.25,"bool":true,"null":null}`,
		`{"outer":{"middle":{"inner":{"value":"x"}}}}`,
		`{"items":[1,"two",true,null,{"nested":[-1,0,1]}]}`,
		`{"κλειδί":"τιμή","emoji":"🦙","escaped\u005fkey":"line\nvalue"}`,
		`{"max":1.7976931348623157e308,"min":5e-324,"negzero":-0,"large":9007199254740991}`,
		`{"array":[],"object":{},"string":""}`,
		`"wrong-string"`, `[1,2,3]`, "42", "false",
	}
	return values[level], level != 0
}

func syntheticUnknownMember(level int) (syntheticMember, bool) {
	members := []syntheticMember{
		{},
		{key: `"unknown"`, value: "17"},
		{key: `"unknown"`, value: `{"nested":{"value":true}}`},
		{key: `"unknown"`, value: `[null,1,"x",{"y":2}]`},
		{key: `"\u00fcnknown"`, value: `{"a":{"b":{"c":[{"d":"é"}]}}}`},
	}
	return members[level], level != 0
}

func syntheticLateMember(level int, op *syntheticMember) (syntheticMember, bool) {
	members := []syntheticMember{
		{},
		{key: `"op"`, value: op.value},
		{key: `"op"`, value: `"OVERRIDE"`},
		{key: `"set"`, value: `{}`},
		{key: `"set"`, value: `{"late":{"value":2}}`},
		{key: `"where"`, value: `{}`},
		{key: `"where"`, value: `{"late":{"value":3}}`},
	}
	return members[level], level != 0
}

func applySyntheticKeyStyle(keys map[string]string, level int) {
	if level == 1 || level == 4 {
		keys["op"] = `"o\u0070"`
	}
	if level == 2 || level == 4 {
		keys["set"] = `"s\u0065t"`
	}
	if level == 3 || level == 4 {
		keys["where"] = `"w\u0068ere"`
	}
}

func renderSyntheticMembers(members []syntheticMember, whitespace int) []byte {
	var out bytes.Buffer
	switch whitespace {
	case 0:
		out.WriteByte('{')
		for i, member := range members {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(member.key)
			out.WriteByte(':')
			out.WriteString(member.value)
		}
		out.WriteByte('}')
	case 1:
		out.WriteString("{ ")
		for i, member := range members {
			if i > 0 {
				out.WriteString(", ")
			}
			out.WriteString(member.key)
			out.WriteString(" : ")
			out.WriteString(member.value)
		}
		out.WriteString(" }")
	case 2:
		out.WriteString("{\n\t")
		for i, member := range members {
			if i > 0 {
				out.WriteString(",\n\t")
			}
			out.WriteString(member.key)
			out.WriteString("\t:\n\t")
			out.WriteString(member.value)
		}
		out.WriteString("\n}\n")
	}
	return out.Bytes()
}

type syntheticAdversarial struct {
	name string
	data []byte
}

func syntheticAdversarialRecords() []syntheticAdversarial {
	return []syntheticAdversarial{
		{name: "deep_unknown_512", data: syntheticDeepRecord("unknown", 512, false)},
		{name: "deep_known_256", data: syntheticDeepRecord("set", 256, true)},
		{name: "wide_array_4096", data: syntheticWideArray(4096)},
		{name: "wide_object_4096", data: syntheticWideObject(4096)},
		{name: "long_strings_1mib", data: syntheticLongStrings(1 << 20)},
		{name: "duplicate_set_1024", data: syntheticDuplicateSet(1024)},
		{name: "whitespace_1mib", data: []byte(`{"op"` + strings.Repeat(" ", 1<<20) + `:"SPACE"}`)},
	}
}

func syntheticDeepRecord(field string, depth int, objects bool) []byte {
	var b strings.Builder
	b.WriteString(`{"op":"DEEP","` + field + `":`)
	for i := 0; i < depth; i++ {
		if objects {
			b.WriteString(`{"v":`)
		} else {
			b.WriteByte('[')
		}
	}
	b.WriteByte('0')
	for i := 0; i < depth; i++ {
		if objects {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	b.WriteByte('}')
	return []byte(b.String())
}

func syntheticWideArray(width int) []byte {
	var b strings.Builder
	b.WriteString(`{"op":"WIDE","set":{"items":[`)
	for i := 0; i < width; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(`]}}`)
	return []byte(b.String())
}

func syntheticWideObject(width int) []byte {
	var b strings.Builder
	b.WriteString(`{"op":"WIDE","set":{`)
	for i := 0; i < width; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(fmt.Sprintf("k%04d", i)))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

func syntheticLongStrings(size int) []byte {
	value, _ := json.Marshal(strings.Repeat("x", size))
	return []byte(`{"op":"LONG","set":{"value":` + string(value) + `}}`)
}

func syntheticDuplicateSet(count int) []byte {
	var b strings.Builder
	b.WriteString(`{"op":"DUP"`)
	for i := 0; i < count; i++ {
		b.WriteString(`,"set":{"n":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return []byte(b.String())
}
