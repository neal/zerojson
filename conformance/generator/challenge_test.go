package generator

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This suite deliberately does not share schema builders, graph cloners,
// observations, or allowlists with the stateful campaign.
//
//go:embed testdata/challenge/types.go
var challengeTypes string

//go:embed testdata/challenge/challenge_test.go
var challengeTests string

func TestIndependentChallenge(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles independent challenge fixtures")
	}
	root := os.Getenv("ZEROJSON_CHALLENGE_CANDIDATE")
	if root == "" {
		var err error
		root, err = filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	bin := filepath.Join(base, "zerojsongen")
	run := func(dir string, args ...string) ([]byte, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-buildvcs=false")
		return cmd.CombinedOutput()
	}
	if out, err := run(filepath.Join(root, "cmd", "zerojsongen"), "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("candidate generator: %v\n%s", err, out)
	}
	for _, mode := range []string{"ordered", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(base, mode)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, src := range map[string]string{"go.mod": fmt.Sprintf("module zerojsonchallenge\ngo 1.26\nrequire github.com/neal/zerojson v0.0.0\nreplace github.com/neal/zerojson => %q\n", root), "types.go": challengeTypes, "challenge_test.go": challengeTests} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{bin, "-dir", dir, "-types", "Record,RootSlice,RootMap", "-pool"}
			if mode == "fallback" {
				args = append(args, "-no-ordered")
			}
			if out, err := run(dir, args...); err != nil {
				t.Fatalf("challenge generation: %v\n%s", err, out)
			}
			args = []string{"go", "test", "-count=1", "-v", "-timeout=2m"}
			if os.Getenv("ZEROJSON_CHALLENGE_RACE") != "" {
				args = append(args, "-race", "-gcflags=all=-d=checkptr=2")
			}
			if os.Getenv("ZEROJSON_CHALLENGE_NOUNSAFE") != "" {
				args = append(args, "-tags=nounsafe")
			}
			if out, err := run(dir, append(args, ".")...); err != nil {
				t.Fatalf("candidate %s challenge: %v\n%s", root, err, out)
			} else {
				t.Logf("%s", out)
			}
		})
	}
}
