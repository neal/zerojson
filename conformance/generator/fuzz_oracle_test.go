package generator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInvalidTagFuzzKeepsZerojsonOnlyChecks pins that a fuzz target for a
// type whose tag names classic encoding/json rejects is not a NO-OP on
// Go 1.27. The v2-backed implementation truncates such a name instead of
// falling back to the Go field name, so the two write different members
// and every stdlib comparison for that type is invalid there — but only
// those. zerojson's own invariants (no panics, valid output, round-trip,
// and ordered-vs-fallback agreement) must still run.
func TestInvalidTagFuzzKeepsZerojsonOnlyChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generator")
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := buildGenerator(t, moduleRoot)
	dir, err := os.MkdirTemp(moduleRoot, "zjnooracle")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	src := strings.ReplaceAll(`package p

type BadTags struct {
	A  string ~json:"aa\""~
	Z  string ~json:"z\\z"~
	Z2 string ~json:"z"~
	N  Inner  ~json:"n"~
}

type Inner struct {
	V int ~json:"v"~
}

type BadList []BadTags
`, "~", "`")
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gen, "-dir", dir, "-types", "BadTags,BadList", "-out", "zerojson_gen.go", "-tests", "zj_fuzz_test.go")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	fuzz, err := os.ReadFile(filepath.Join(dir, "zj_fuzz_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// The v2 guard hands off to the oracle-free checks instead of
		// returning from the whole fuzz body.
		"zjFuzzNoOracleBadTags(t, data)",
		"zjFuzzNoOracleBadList(t, data)",
		// Which must still assert what does not involve the oracle.
		"func zjFuzzNoOracleBadTags(t *testing.T, data []byte) {",
		"zjDecodeFallback_BadTags(&lFallback, &viaFallback, &zjArenaBadTags{})",
		"could not re-decode its own output",
		"ordered-vs-fallback divergence",
	} {
		if !strings.Contains(string(fuzz), want) {
			t.Errorf("emitted fuzz file does not contain %q", want)
		}
	}
	// And they must hold when actually run, on either toolchain.
	drive := strings.ReplaceAll(`package p

import "testing"

func TestNoOracleArmRuns(t *testing.T) {
	for _, doc := range []string{
		~{"A":"x","Z":"y","z":"w","n":{"v":1}}~,
		~{"n":{"v":-3},"A":"","Z":"é"}~,
		~{}~,
		~{"A":null,"n":null}~,
		~not json~,
	} {
		zjFuzzNoOracleBadTags(t, []byte(doc))
	}
	for _, doc := range []string{~[]~, ~[{"A":"x"},{"n":{"v":2}}]~, ~null~, ~[~} {
		zjFuzzNoOracleBadList(t, []byte(doc))
	}
}
`, "~", "`")
	if err := os.WriteFile(filepath.Join(dir, "drive_test.go"), []byte(drive), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(moduleRoot, dir)
	run := exec.Command("go", "test", "./"+filepath.ToSlash(rel)+"/...")
	run.Dir = moduleRoot
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("generated package tests failed:\n%s", out)
	}
}
