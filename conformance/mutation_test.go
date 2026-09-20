package conformance

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMutationDetectors proves that the regression suite kills realistic,
// compile-valid faults in the runtime and generator. It is opt-in because it
// copies the repository, regenerates fixtures, and launches nested go tests.
// Use scripts/mutation-test.sh; quick is the developer tier and full is the
// broader pre-release tier.
func TestMutationCatalogAnchors(t *testing.T) {
	for _, m := range mutationCatalog() {
		data, err := os.ReadFile(filepath.Join("..", m.file))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(data), m.old); n != m.wantCount {
			t.Errorf("%s: anchor count %d, want %d", m.name, n, m.wantCount)
		}
	}
}

func TestMutationDetectors(t *testing.T) {
	mode := os.Getenv("ZEROJSON_MUTATION")
	if mode == "" {
		t.Skip("run scripts/mutation-test.sh [quick|full]")
	}
	if mode != "quick" && mode != "full" {
		t.Fatalf("ZEROJSON_MUTATION=%q, want quick or full", mode)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	mutantRoot := filepath.Join(t.TempDir(), "zerojson-mutant")
	if err := copyMutationTree(repoRoot, mutantRoot); err != nil {
		t.Fatalf("copy repository: %v", err)
	}

	mutations := mutationCatalog()
	var selected []mutationCase
	seenNames := make(map[string]bool)
	for _, m := range mutations {
		if m.name == "" || seenNames[m.name] {
			t.Fatalf("mutation name %q is empty or duplicated", m.name)
		}
		seenNames[m.name] = true
		if m.tier != "quick" && m.tier != "full" {
			t.Fatalf("mutation %s has invalid tier %q", m.name, m.tier)
		}
		if m.old == m.new {
			t.Fatalf("mutation %s does not change its source anchor", m.name)
		}
		if m.tier == "quick" || mode == "full" {
			selected = append(selected, m)
		}
	}
	t.Logf("validating %d of %d curated mutants (%s tier)", len(selected), len(mutations), mode)

	// A pre-existing detector failure must never count as a killed mutant.
	// Run every distinct detector once against the pristine temporary tree.
	baselines := make(map[string]commandSpec)
	for _, m := range selected {
		baselines[m.detector.key()] = m.detector
	}
	for _, detector := range baselines {
		if out, err := runMutationCommand(mutantRoot, detector); err != nil {
			t.Fatalf("baseline detector %s failed: %v\n%s", detector, err, out)
		}
	}

	for _, m := range selected {
		m := m
		t.Run(m.name, func(t *testing.T) {
			t.Logf("injecting %s mutant in %s", m.tier, m.file)
			restore, err := applyMutation(mutantRoot, m)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := restore(); err != nil {
					t.Errorf("restore mutant: %v", err)
				}
			})

			if m.regenerate {
				if out, err := runMutationCommand(mutantRoot, commandSpec{
					dir:  "conformance",
					args: []string{"go", "generate", "./codec"},
				}); err != nil {
					t.Fatalf("mutant did not regenerate cleanly: %v\n%s", err, out)
				}
			}

			out, err := runMutationCommand(mutantRoot, m.detector)
			if err == nil {
				t.Fatalf("MUTANT SURVIVED: detector %s passed", m.detector)
			}
			if !strings.Contains(out, m.wantOutput) {
				t.Fatalf("mutant died for the wrong reason; want detector output containing %q\ncommand: %s\nerror: %v\n%s", m.wantOutput, m.detector, err, out)
			}
		})
	}
}

type mutationCase struct {
	name       string
	tier       string
	file       string
	old        string
	new        string
	occurrence int // zero-based; -1 replaces all occurrences
	wantCount  int
	regenerate bool
	detector   commandSpec
	wantOutput string
}

type commandSpec struct {
	dir  string
	args []string
}

func (c commandSpec) key() string { return c.dir + "\x00" + strings.Join(c.args, "\x00") }

func (c commandSpec) String() string {
	return fmt.Sprintf("(cd %s && %s)", c.dir, strings.Join(c.args, " "))
}

func codecDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  "conformance",
		args: []string{"go", "test", "./codec", "-run", pattern, "-count=1"},
	}
}

func rootDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  ".",
		args: []string{"go", "test", "./...", "-run", pattern, "-count=1"},
	}
}

func generatorDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  "conformance",
		args: []string{"go", "test", "./generator", "-run", pattern, "-count=1"},
	}
}

func applyMutation(root string, m mutationCase) (func() error, error) {
	path := filepath.Join(root, filepath.FromSlash(m.file))
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.file, err)
	}
	text := string(original)
	if count := strings.Count(text, m.old); count != m.wantCount {
		return nil, fmt.Errorf("%s anchor count = %d, want %d; mutation catalog is stale", m.file, count, m.wantCount)
	}
	mutated, err := replaceOccurrence(text, m.old, m.new, m.occurrence)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.name, err)
	}

	generated := []string{
		"conformance/codec/zerojson_gen.go",
		"conformance/codec/zerojson_gen_fuzz_test.go",
	}
	snapshots := map[string][]byte{path: original}
	if m.regenerate {
		for _, name := range generated {
			p := filepath.Join(root, filepath.FromSlash(name))
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("snapshot %s: %w", name, err)
			}
			snapshots[p] = b
		}
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		return nil, fmt.Errorf("write mutant %s: %w", m.file, err)
	}
	return func() error {
		for p, b := range snapshots {
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func replaceOccurrence(s, old, replacement string, occurrence int) (string, error) {
	if occurrence == -1 {
		return strings.ReplaceAll(s, old, replacement), nil
	}
	start := 0
	for i := 0; i <= occurrence; i++ {
		rel := strings.Index(s[start:], old)
		if rel < 0 {
			return "", fmt.Errorf("occurrence %d not found", occurrence)
		}
		at := start + rel
		if i == occurrence {
			return s[:at] + replacement + s[at+len(old):], nil
		}
		start = at + len(old)
	}
	panic("unreachable")
}

func runMutationCommand(root string, spec commandSpec) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.args[0], spec.args[1:]...)
	cmd.Dir = filepath.Join(root, filepath.FromSlash(spec.dir))
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s: %w", spec, ctx.Err())
	}
	return string(out), err
}

func copyMutationTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".codex" || d.Name() == ".agents") {
			return filepath.SkipDir
		}
		outPath := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(outPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, outPath)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
