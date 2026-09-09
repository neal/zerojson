package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedDecodeOracleMirrorsRejections pins the stdlib-rejects arm of
// the emitted fuzzers: a codec that silently ACCEPTS what encoding/json
// rejects for a non-grammar reason must die from the seed corpus alone.
// Three realistic faults are injected into a generated codec — trailing
// data no longer rejected (Finish dropped), a TextUnmarshaler's error
// dropped, and a null no longer delivered to a value-typed Unmarshaler —
// each of which passed the previous oracle, which enforced only wrong
// value kinds. A fourth mutant checks the integer-boundary seeds.
func TestGeneratedDecodeOracleMirrorsRejections(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and mutates a generated fixture")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, root)
	dir, err := os.MkdirTemp(root, "zjdecodeoracle")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("types.go", `package p

import "errors"

// Day rejects anything but "ok" through UnmarshalText.
type Day string

func (d Day) MarshalText() ([]byte, error) { return []byte(d), nil }
func (d *Day) UnmarshalText(b []byte) error {
	if string(b) != "ok" {
		return errors.New("bad day")
	}
	*d = Day(b)
	return nil
}

// Opt is a value-typed json.Unmarshaler whose null has meaning: it errors.
type Opt struct{ V int }

func (o Opt) MarshalJSON() ([]byte, error) { return []byte("1"), nil }
func (o *Opt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return errors.New("null not allowed")
	}
	o.V = len(b)
	return nil
}

type Root struct {
	N   int  `+"`"+`json:"n"`+"`"+`
	I8  int8 `+"`"+`json:"i8"`+"`"+`
	Day Day  `+"`"+`json:"day"`+"`"+`
	Opt Opt  `+"`"+`json:"opt"`+"`"+`
}
`)
	cmd := exec.Command(gen, "-dir", dir, "-types", "Root", "-tests", "codec_fuzz_test.go")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "zerojson_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	run := func() ([]byte, error) {
		cmd := exec.Command("go", "test", ".", "-run", "^FuzzRootZJSON$", "-count=1", "-timeout=120s")
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run(); err != nil {
		t.Fatalf("baseline seeds: %v %s", err, out)
	}
	mutate := func(name, old, new string) string {
		t.Helper()
		if !strings.Contains(original, old) {
			t.Fatalf("%s: anchor %q not found in the generated codec", name, old)
		}
		return strings.ReplaceAll(original, old, new)
	}
	for _, m := range []struct{ name, src string }{
		{"trailing data accepted (both grammar guards dropped)", strings.ReplaceAll(mutate("finish", "\tl.Finish()\n", "\n"), "\tif err := zerojson.Valid(data); err != nil {\n\t\treturn err\n\t}\n", "")},
		{"TextUnmarshaler error dropped", mutate("text", "if err := v.Day.UnmarshalText(txt); err != nil {\n\t\t\t\t\tl.AddError(err)\n\t\t\t\t}", "_ = v.Day.UnmarshalText(txt)")},
		{"null not delivered to value Unmarshaler", mutate("null", "if err := v.Opt.UnmarshalJSON(l.NullBytes()); err != nil {\n\t\t\t\t\tl.AddError(err)\n\t\t\t\t}", "_ = l.NullBytes()")},
	} {
		t.Run(m.name, func(t *testing.T) {
			write("zerojson_gen.go", m.src)
			defer write("zerojson_gen.go", original)
			out, err := run()
			if err == nil || !strings.Contains(string(out), "but zerojson accepted it") {
				t.Fatalf("mutant survived the seed corpus or died for the wrong reason: %v\n%s", err, out)
			}
		})
	}
}
