package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genCase is one generator invocation over a small package: the source,
// the -types list, extra flags, and whether generation must succeed (and
// the package then build) or fail with a message containing wantErr.
type genCase struct {
	name    string
	src     string
	types   string
	flags   []string
	wantErr string // "" = must succeed and `go vet` the package
	test    string // optional _test.go source to run with `go test` after generation
}

// TestGeneratorShapeValidation checks supported shapes and rejects invalid
// type/method/flag combinations before they can produce unusable output.
func TestGeneratorShapeValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	cases := []genCase{
		{name: "named bool", src: "package p\n\ntype Flag bool\n\ntype T struct {\n\tF Flag `json:\"f\"`\n\tP *Flag `json:\"p\"`\n\tS []Flag `json:\"s\"`\n}\n", types: "T"},
		{name: "nested non-ascii name under ordered parent", src: "package p\n\ntype Inner struct {\n\tCafe string `json:\"café\"`\n\tN int `json:\"n\"`\n}\n\ntype Outer struct {\n\tIn Inner `json:\"in\"`\n\tPtr *Inner `json:\"ptr\"`\n}\n", types: "Outer"},
		{name: "slice of hand-written Appender struct", src: "package p\n\nimport \"github.com/neal/zerojson\"\n\ntype L struct{ X int }\n\nfunc (l *L) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(l.X)), nil }\nfunc (l *L) ReadZJSON(lx *zerojson.Lexer) { l.X = int(lx.ReadInt64()) }\n\ntype T struct {\n\tA []L `json:\"a\"`\n}\n", types: "T", wantErr: "Appender"},
		{name: "explicitly listed hand-written Appender struct", src: "package p\n\nimport \"github.com/neal/zerojson\"\n\ntype L struct{ X int }\n\nfunc (l *L) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(l.X)), nil }\nfunc (l *L) ReadZJSON(lx *zerojson.Lexer) { l.X = int(lx.ReadInt64()) }\n", types: "L", wantErr: "hand-written"},
		{name: "map key with UnmarshalText", src: "package p\n\ntype K string\n\nfunc (k K) MarshalText() ([]byte, error) { return []byte(\"k:\" + string(k)), nil }\nfunc (k *K) UnmarshalText(b []byte) error { *k = K(b); return nil }\n\ntype T struct {\n\tM map[K]int `json:\"m\"`\n}\n", types: "T", wantErr: "MarshalText/UnmarshalText"},
		{name: "compat with tests", src: "package p\n\ntype T struct {\n\tA int `json:\"a\"`\n}\n", types: "T", flags: []string{"-compat", "-tests", "zj_fuzz_test.go"}, wantErr: "-compat"},
		{name: "tests without _test.go suffix", src: "package p\n\ntype T struct {\n\tA int `json:\"a\"`\n}\n", types: "T", flags: []string{"-tests", "fuzz.go"}, wantErr: "_test.go"},
		{name: "empty types entry", src: "package p\n\ntype T struct {\n\tA int `json:\"a\"`\n}\n", types: "T,", wantErr: "empty name"},
		{name: "pointer-element slice of Appender+Marshaler type", src: "package p\n\nimport (\n\t\"strconv\"\n\n\t\"github.com/neal/zerojson\"\n)\n\ntype C int64\n\nfunc (c C) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(c)), nil }\nfunc (c *C) ReadZJSON(l *zerojson.Lexer) { *c = C(l.ReadInt64()) }\nfunc (c C) MarshalJSON() ([]byte, error) { return []byte(strconv.FormatInt(int64(c), 10)), nil }\nfunc (c *C) UnmarshalJSON(b []byte) error { v, err := strconv.ParseInt(string(b), 10, 64); *c = C(v); return err }\n\ntype T struct {\n\tMany []*C `json:\"many\"`\n}\n", types: "T"},
		{name: "explicit alias type refused", src: "package p\n\ntype Real struct {\n\tA int `json:\"a\"`\n}\n\ntype Alias = Real\n", types: "Real,Alias", wantErr: "alias"},
		{name: "-pool with a dependency package named atomic", src: "package p\n\nimport \"github.com/neal/zerojson/conformance/internal/collide/atomic\"\n\ntype T struct {\n\tD *atomic.Token `json:\"d\"`\n}\n", types: "T", flags: []string{"-pool", "-leaf", "github.com/neal/zerojson/conformance/internal/collide/atomic.Token=github.com/neal/zerojson/conformance/internal/collide/atomic.Append,github.com/neal/zerojson/conformance/internal/collide/atomic.Read"}},
		{name: "enum helper names that sanitize to the same identifier", src: "package p\n\nimport (\n\tfoo_bar \"github.com/neal/zerojson/conformance/internal/enumcollide/foo_bar\"\n\tbar \"github.com/neal/zerojson/conformance/internal/enumcollide/foo/bar\"\n)\n\ntype T struct {\n\tA []foo_bar.Mode_X `json:\"a\"`\n\tB []bar.X `json:\"b\"`\n}\n", types: "T"},
		{name: "omitempty and omitzero together on scalars vet cleanly", src: "package p\n\ntype T struct {\n\tVI int    `json:\"vi,omitempty,omitzero,string\"`\n\tVU uint64 `json:\"vu,omitempty,omitzero\"`\n\tVS string `json:\"vs,omitempty,omitzero\"`\n\tVB bool   `json:\"vb,omitempty,omitzero\"`\n\tVF float32 `json:\"vf,omitempty,omitzero\"`\n\tVP *int   `json:\"vp,omitempty,omitzero\"`\n\tVL []int  `json:\"vl,omitempty,omitzero\"`\n}\n", types: "T",
			test: "package p\n\nimport (\n\t\"encoding/json\"\n\t\"testing\"\n)\n\nfunc TestGates(t *testing.T) {\n\tfor _, v := range []T{{}, {VI: 1, VU: 2, VS: \"s\", VB: true, VF: 1.5, VL: []int{}}} {\n\t\tzj, err := v.AppendZJSON(nil)\n\t\tif err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tstd, _ := json.Marshal(v)\n\t\tif string(zj) != string(std) {\n\t\t\tt.Fatalf(\"zj=%s std=%s\", zj, std)\n\t\t}\n\t}\n}\n"},
		{name: "null delivered to an Appender type that also has UnmarshalJSON", src: "package p\n\nimport (\n\t\"strconv\"\n\n\t\"github.com/neal/zerojson\"\n)\n\ntype C int64\n\nfunc (c C) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(c)), nil }\nfunc (c *C) ReadZJSON(l *zerojson.Lexer) { *c = C(l.ReadInt64()) }\nfunc (c C) MarshalJSON() ([]byte, error) { return []byte(strconv.FormatInt(int64(c), 10)), nil }\nfunc (c *C) UnmarshalJSON(b []byte) error {\n\tif string(b) == \"null\" {\n\t\t*c = 0\n\t\treturn nil\n\t}\n\tv, err := strconv.ParseInt(string(b), 10, 64)\n\t*c = C(v)\n\treturn err\n}\n\ntype T struct {\n\tC C `json:\"c\"`\n}\n", types: "T",
			test: "package p\n\nimport (\n\t\"encoding/json\"\n\t\"testing\"\n)\n\nfunc TestNull(t *testing.T) {\n\tfor _, doc := range []string{`{\"c\":null}`, `{\"c\":5}`, `{\"c\":5,\"c\":null}`} {\n\t\tzj, std := T{C: 100}, T{C: 100}\n\t\tif err := zj.UnmarshalZJSON([]byte(doc)); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tif err := json.Unmarshal([]byte(doc), &std); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tif zj != std {\n\t\t\tt.Fatalf(\"%s: zj=%+v std=%+v\", doc, zj, std)\n\t\t}\n\t}\n}\n"},
		{name: "invalid tag names with -tests: fuzz seeds green on both toolchains", src: "package p\n\ntype BadTags struct {\n\tA  string `json:\"aa\\\"\"`\n\tZ  string `json:\"z\\\\z\"`\n\tZ2 string `json:\"z\"`\n\tE  string `json:\"😀\"`\n}\n", types: "BadTags", flags: []string{"-tests", "zj_fuzz_test.go"}},
		{name: "fresh storage preserves allocation bounds across rewinds and duplicate keys", src: "package p\n\ntype T struct {\n\tP int            `json:\"p\"`\n\tS []int          `json:\"s\"`\n\tM map[string]int `json:\"m\"`\n}\n", types: "T", flags: []string{"-pool"},
			test: "package p\n\nimport \"testing\"\n\n// Pin fresh arena, slice growth and map allocation costs across paths.\nfunc TestPooledReuseSurvivesRewind(t *testing.T) {\n\tfor name, doc := range map[string]string{\n\t\t\"canonical\":  `{\"p\":1,\"s\":[1,2,3],\"m\":{\"a\":1}}`,\n\t\t\"outoforder\": `{\"s\":[1,2,3],\"m\":{\"a\":1},\"p\":1}`,\n\t\t\"dupkey\":     `{\"p\":1,\"s\":[1,2,3],\"s\":[4],\"m\":{\"a\":1},\"m\":{\"b\":2}}`,\n\t\t\"bailfirst\":  `{\"z\":0,\"s\":[1,2,3],\"m\":{\"a\":1},\"p\":1}`,\n\t} {\n\t\tvar d TDecoder\n\t\tdata := []byte(doc)\n\t\tvar v T\n\t\tfor i := 0; i < 3; i++ {\n\t\t\tif err := d.DecodeBorrow(data, &v); err != nil {\n\t\t\t\tt.Fatal(err)\n\t\t\t}\n\t\t}\n\t\twant := 5.0\n\t\tif n := testing.AllocsPerRun(20, func() {\n\t\t\tif err := d.DecodeBorrow(data, &v); err != nil {\n\t\t\t\tt.Fatal(err)\n\t\t\t}\n\t\t}); n > want {\n\t\t\tt.Errorf(\"%s: %v allocations per pooled decode, budget %v\", name, n, want)\n\t\t}\n\t}\n\t// Caller-supplied storage on a pooled target is still never recycled.\n\tvar d TDecoder\n\tcaller := make([]int, 0, 8)\n\tv := T{S: caller}\n\tif err := d.Decode([]byte(`{\"s\":[7]}`), &v); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif len(caller[:1]) == 1 && caller[:1][0] == 7 {\n\t\tt.Fatal(\"pooled decode wrote into caller-supplied storage\")\n\t}\n}\n"},
		{name: "array-form byte slices reuse existing bytes like stdlib", src: "package p\n\ntype MyByte uint8\n\ntype T struct {\n\tB  []byte   `json:\"b\"`\n\tN  []MyByte `json:\"n\"`\n\tU8 []uint8  `json:\"u8\"`\n}\n", types: "T",
			test: "package p\n\nimport (\n\t\"encoding/json\"\n\t\"reflect\"\n\t\"testing\"\n)\n\n// The array-of-bytes input form decoded into an existing slice must\n// follow the ordinary slice-reuse rule: bytes are written in place and a\n// null element keeps the existing byte. A fresh slice would zero bytes\n// stdlib preserves, silently changing values.\nfunc TestByteArrayReuse(t *testing.T) {\n\tstart := func() T { return T{B: []byte(\"hi\"), N: []MyByte(\"hey\"), U8: []uint8(\"hello\")} }\n\tfor _, doc := range []string{\n\t\t`{\"b\":\"aGVsbG8=\",\"b\":[null,null,null,null,null]}`,\n\t\t`{\"b\":\"aGVsbG8=\",\"b\":[1,null,3]}`,\n\t\t`{\"b\":[104,105],\"b\":[null,null,null]}`,\n\t\t`{\"b\":[null,null]}`,\n\t\t`{\"n\":[null]}`,\n\t\t`{\"u8\":[null,1,null]}`,\n\t\t`{\"u8\":[]}`,\n\t\t`{\"u8\":\"YQ==\"}`,\n\t\t`{\"b\":[7,8,9,10,11,12]}`,\n\t\t`{\"b\":null,\"b\":[null]}`,\n\t} {\n\t\tfor _, fresh := range []bool{false, true} {\n\t\t\tvar zj, std T\n\t\t\tif !fresh {\n\t\t\t\tzj, std = start(), start()\n\t\t\t}\n\t\t\tzjErr := zj.UnmarshalZJSON([]byte(doc))\n\t\t\tstdErr := json.Unmarshal([]byte(doc), &std)\n\t\t\tif (zjErr != nil) != (stdErr != nil) {\n\t\t\t\tt.Fatalf(\"%s fresh=%v: zj err %v, std err %v\", doc, fresh, zjErr, stdErr)\n\t\t\t}\n\t\t\tif !reflect.DeepEqual(zj, std) {\n\t\t\t\tt.Errorf(\"%s fresh=%v: zj=%q std=%q\", doc, fresh, zj, std)\n\t\t\t}\n\t\t}\n\t}\n}\n"},
		{name: "null delivered to an Appender type with UnmarshalJSON but no MarshalJSON", src: "package p\n\nimport (\n\t\"strconv\"\n\n\t\"github.com/neal/zerojson\"\n)\n\ntype C int64\n\nfunc (c C) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(c)), nil }\nfunc (c *C) ReadZJSON(l *zerojson.Lexer) { *c = C(l.ReadInt64()) }\nfunc (c *C) UnmarshalJSON(b []byte) error {\n\tif string(b) == \"null\" {\n\t\t*c = 0\n\t\treturn nil\n\t}\n\tv, err := strconv.ParseInt(string(b), 10, 64)\n\t*c = C(v)\n\treturn err\n}\n\ntype T struct {\n\tC C `json:\"c\"`\n}\n", types: "T",
			test: "package p\n\nimport (\n\t\"encoding/json\"\n\t\"testing\"\n)\n\n// encoding/json routes a null through UnmarshalJSON on the strength of\n// that method alone, so a type carrying only UnmarshalJSON (no\n// MarshalJSON) must receive it too.\nfunc TestNullReachesUnmarshalerOnlyAppender(t *testing.T) {\n\tfor _, doc := range []string{`{\"c\":null}`, `{\"c\":5}`, `{\"c\":5,\"c\":null}`} {\n\t\tzj, std := T{C: 7}, T{C: 7}\n\t\tif err := zj.UnmarshalZJSON([]byte(doc)); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tif err := json.Unmarshal([]byte(doc), &std); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tif zj != std {\n\t\t\tt.Errorf(\"%s: zj=%+v std=%+v\", doc, zj, std)\n\t\t}\n\t}\n}\n"},
		{name: "package-scope identifier named zerojson refused", src: "package p\n\nvar zerojson = 1\n\nfunc use() int { return zerojson }\n\ntype T struct {\n\tA int `json:\"a\"`\n}\n", types: "T", wantErr: "collide with the runtime import"},
		{name: "dependency package named zerojson", src: "package p\n\nimport tok \"github.com/neal/zerojson/conformance/internal/collide/zerojson\"\n\ntype T struct {\n\tTok  tok.Token   `json:\"tok\"`\n\tList []tok.Token `json:\"list\"`\n}\n", types: "T",
			flags: []string{"-leaf", "github.com/neal/zerojson/conformance/internal/collide/zerojson.Token=github.com/neal/zerojson/conformance/internal/collide/zerojson.Append,github.com/neal/zerojson/conformance/internal/collide/zerojson.Read"},
			test:  "package p\n\nimport (\n\t\"testing\"\n\n\ttok \"github.com/neal/zerojson/conformance/internal/collide/zerojson\"\n)\n\n// The runtime is imported under the fixed `zerojson` qualifier, so a\n// dependency package of the same name must be aliased instead of\n// claiming it — the file would otherwise not compile.\nfunc TestAliasedSameNameDependency(t *testing.T) {\n\tvar v T\n\tif err := v.UnmarshalZJSON([]byte(`{\"tok\":\"a\",\"list\":[\"b\",\"c\"]}`)); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif v.Tok != tok.Token(\"a\") || len(v.List) != 2 || v.List[1] != tok.Token(\"c\") {\n\t\tt.Fatalf(\"decoded %+v\", v)\n\t}\n\tenc, err := v.AppendZJSON(nil)\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif string(enc) != `{\"tok\":\"a\",\"list\":[\"b\",\"c\"]}` {\n\t\tt.Fatalf(\"encoded %s\", enc)\n\t}\n}\n"},
		{name: "slice and map of Appender+Marshaler type delegate to the JSON methods", src: "package p\n\nimport (\n\t\"strconv\"\n\n\t\"github.com/neal/zerojson\"\n)\n\ntype C int64\n\nfunc (c C) AppendZJSON(dst []byte) ([]byte, error) { return zerojson.AppendInt64(dst, int64(c)), nil }\nfunc (c *C) ReadZJSON(l *zerojson.Lexer) { *c = C(l.ReadInt64()) }\nfunc (c C) MarshalJSON() ([]byte, error) { return []byte(strconv.FormatInt(int64(c), 10)), nil }\nfunc (c *C) UnmarshalJSON(b []byte) error { v, err := strconv.ParseInt(string(b), 10, 64); *c = C(v); return err }\n\ntype T struct {\n\tOne C `json:\"one\"`\n\tMany []C `json:\"many\"`\n\tIdx map[string]C `json:\"idx\"`\n}\n", types: "T"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, err := os.MkdirTemp(moduleRoot, "zjshape")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-dir", dir, "-types", c.types, "-out", "zerojson_gen.go"}, c.flags...)
			cmd := exec.Command(gen, args...)
			cmd.Dir = moduleRoot
			out, err := cmd.CombinedOutput()
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("generator succeeded, want refusal containing %q:\n%s", c.wantErr, out)
				}
				if !strings.Contains(string(out), c.wantErr) {
					t.Fatalf("refusal message does not mention %q:\n%s", c.wantErr, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("generator failed: %v\n%s", err, out)
			}
			// -out is relative to -dir.
			if _, err := os.Stat(filepath.Join(dir, "zerojson_gen.go")); err != nil {
				t.Fatalf("output not written next to the package: %v", err)
			}
			rel, _ := filepath.Rel(moduleRoot, dir)
			vet := exec.Command("go", "vet", "./"+filepath.ToSlash(rel)+"/...")
			vet.Dir = moduleRoot
			if out, err := vet.CombinedOutput(); err != nil {
				t.Fatalf("generated package does not compile/vet:\n%s", out)
			}
			if c.test != "" {
				if err := os.WriteFile(filepath.Join(dir, "case_test.go"), []byte(c.test), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if c.test != "" || hasFlag(c.flags, "-tests") {
				run := exec.Command("go", "test", "./"+filepath.ToSlash(rel)+"/...")
				run.Dir = moduleRoot
				if out, err := run.CombinedOutput(); err != nil {
					t.Fatalf("generated package tests failed:\n%s", out)
				}
			}
		})
	}
}

func hasFlag(flags []string, name string) bool {
	for _, f := range flags {
		if f == name {
			return true
		}
	}
	return false
}
