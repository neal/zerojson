package generator

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Independent typed observations: no schema campaign builders, cloning,
// projection, or divergence allowlists are used by these fixtures.
//
//go:embed testdata/graph_acceptance/*.go
var graphAcceptanceSource embed.FS

func graphInstrumentation(race, checkptr, explicit bool) []string {
	var args []string
	if race || explicit {
		args = append(args, "-race")
	}
	if checkptr || explicit {
		args = append(args, "-gcflags=all=-d=checkptr=2")
	}
	return args
}

func TestGraphInstrumentation(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		race, checkptr, explicit bool
		want                     []string
	}{
		{"ordinary", false, false, false, nil},
		{"race", true, false, false, []string{"-race"}},
		{"checkptr", false, true, false, []string{"-gcflags=all=-d=checkptr=2"}},
		{"both", true, true, false, []string{"-race", "-gcflags=all=-d=checkptr=2"}},
		{"explicit", false, false, true, []string{"-race", "-gcflags=all=-d=checkptr=2"}},
		{"no_duplicates", true, true, true, []string{"-race", "-gcflags=all=-d=checkptr=2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := graphInstrumentation(tc.race, tc.checkptr, tc.explicit); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("instrumentation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGraphAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles independent acceptance fixtures")
	}
	root := os.Getenv("ZEROJSON_GRAPH_CANDIDATE")
	if root == "" {
		root = "../.."
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
	if out, err := run(filepath.Join(root, "cmd/zerojsongen"), "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("generator: %v\n%s", err, out)
	}
	for _, mode := range []string{"ordered", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(base, mode)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			mod := fmt.Sprintf("module graphacceptance\ngo 1.26\nrequire github.com/neal/zerojson v0.0.0\nreplace github.com/neal/zerojson => %q\n", root)
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
				t.Fatal(err)
			}
			entries, err := graphAcceptanceSource.ReadDir("testdata/graph_acceptance")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				data, err := graphAcceptanceSource.ReadFile("testdata/graph_acceptance/" + entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{bin, "-dir", dir, "-types", "Plain,Observed", "-pool"}
			if mode == "fallback" {
				args = append(args, "-no-ordered")
			}
			if out, err := run(dir, args...); err != nil {
				t.Fatalf("generate: %v\n%s", err, out)
			}
			args = []string{"go", "test", ".", "-count=1", "-v", "-timeout=5m"}
			// Child go commands do not inherit the parent's instrumentation.
			args = append(args, graphInstrumentation(raceDetectorEnabled,
				os.Getenv("ZEROJSON_CHECKPTR") != "", os.Getenv("ZEROJSON_GRAPH_RACE") != "")...)
			t.Logf("fixture command: %s", strings.Join(args, " "))
			if out, err := run(dir, args...); err != nil {
				t.Fatalf("acceptance candidate=%s: %v\n%s", root, err, out)
			} else {
				t.Logf("%s", out)
			}
			if mode != "ordered" || os.Getenv("ZEROJSON_GRAPH_CALIBRATE") == "" {
				return
			}
			path := filepath.Join(dir, "zerojson_gen.go")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			guardStart := strings.Index(string(original), "func zjDecodeOrdered_Plain(")
			if guardStart < 0 {
				t.Fatal("missing ordered guard")
			}
			guardLength := strings.Index(string(original)[guardStart:], "\n\tdata, pos :=")
			if guardLength < 0 {
				t.Fatal("missing ordered body anchor")
			}
			guardEnd := guardLength + guardStart
			guard := string(original)[guardStart:guardEnd]
			lineStart := strings.Index(guard, "\n\tif ")
			if lineStart < 0 {
				t.Fatal("missing ordered reuse guard")
			}
			lineLength := strings.Index(guard[lineStart:], "\n\t}")
			if lineLength < 0 {
				t.Fatal("missing ordered reuse guard terminator")
			}
			lineEnd := lineLength + lineStart + len("\n\t}")
			withoutGuard := guard[:lineStart] + guard[lineEnd:]
			for _, fault := range []struct{ name, old, new, marker string }{
				{"replay_shared_storage", guard, withoutGuard, "graph mismatch"},
				{"reset_ordinary_target", "func (v *Plain) unmarshalZJSON(data []byte, borrow bool) error {", "func (v *Plain) unmarshalZJSON(data []byte, borrow bool) error {\n*v = Plain{}", "graph mismatch"},
				{"repeat_callback", "v.Before.UnmarshalJSON(raw)", "func() error { if e := v.Before.UnmarshalJSON(raw); e != nil { return e }; return v.Before.UnmarshalJSON(raw) }()", "callback mismatch"},
			} {
				t.Run(fault.name, func(t *testing.T) {
					if strings.Count(string(original), fault.old) != 1 {
						t.Fatal("stale fault anchor", fault.name)
					}
					mutated := strings.Replace(string(original), fault.old, fault.new, 1)
					if err := os.WriteFile(path, []byte(mutated), 0600); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := os.WriteFile(path, original, 0600); err != nil {
							t.Fatal(err)
						}
					}()
					out, err := run(dir, "go", "test", ".", "-count=1", "-timeout=5m")
					if err == nil || !strings.Contains(string(out), fault.marker) || strings.Contains(string(out), "[build failed]") || strings.Contains(string(out), "test timed out") {
						t.Fatalf("fault not behaviorally detected: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
