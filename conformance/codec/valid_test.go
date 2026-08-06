package codec

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neal/zerojson"
)

// valid_test.go is the conformance suite for zerojson.Valid: the
// verdict-parity fuzzer (FuzzValidAgainstStdlib), a deterministic
// truncation sweep (Valid's verdict must track encoding/json.Valid's at
// every prefix length, not just avoid panicking — a stronger property
// than truncation_test.go's decode sweep, which only requires
// error-not-panic since decode success/failure legitimately differs by
// prefix in ways Valid's exact grammar contract does not allow), and an
// adversarial suite with generous-but-present time budgets so a future
// accidental quadratic regression fails the build instead of silently
// getting slow.

// validSeedCorpus collects every message this package's other suites
// already assembled — variants() (Event fixtures), the golden corpus
// (testdata/golden), and the truncation corpus (every leaf kind) — so
// FuzzValidAgainstStdlib starts from documents that already exercise
// every field kind the generator supports, not just ad hoc examples.
func validSeedCorpus(tb testing.TB) [][]byte {
	tb.Helper()
	var seeds [][]byte

	for _, tr := range variants() {
		b, err := tr.AppendZJSON(nil)
		if err != nil {
			tb.Fatal(err)
		}
		seeds = append(seeds, b)
	}

	for _, fx := range goldenFixtures {
		path := filepath.Join("testdata", "golden", fx.name+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		seeds = append(seeds, bytes.TrimRight(raw, "\n"))
	}

	for _, data := range truncationCorpus(tb) {
		seeds = append(seeds, data)
	}

	for _, s := range orderedTrapSeeds {
		seeds = append(seeds, []byte(s))
	}

	return seeds
}

// FuzzValidAgainstStdlib is the direct verdict-parity contract test:
// (zerojson.Valid(data) == nil) == json.Valid(data) for every input,
// fuzzer-mutated or hand-seeded alike. Unlike FuzzDecodeAgainstStdlib
// (which normalizes fuzzer input through a canonical stdlib re-encode
// first, since decode's contract only concerns well-formed input),
// Valid's whole point is grammar-checking arbitrary — including
// malformed — bytes, so raw fuzzer input is fed to both oracles
// unmodified.
func FuzzValidAgainstStdlib(f *testing.F) {
	for _, s := range validSeedCorpus(f) {
		f.Add(s)
	}
	// Hand-picked grammar corners, beyond what mutating well-formed
	// seeds is likely to reach on its own.
	for _, s := range []string{
		"", " ", "\t\n\r ", "-", "01", "-01", "1.", ".1", "1.e5", "1e", "1e+", "1e-",
		"+1", "00", "NaN", "Infinity", "1e999999999",
		`"`, `""`, `"\`, `"\u"`, `"\u12"`, `"\u12zz"`, `"\z"`, "\"\x00\"", "\"\x1f\"",
		"\"\xff\xfe\"", `"\uD800"`, `"\uDC00"`, `"𐀀"`,
		"{", "}", "[", "]", "{,}", "[,]", `{"a":}`, `{:"a"}`, `{"a":1,}`, `[1,]`,
		`{"id":1}garbage`, `{"id":1}   `, `{"id":1}{"id":2}`,
		"nul", "tru", "fals", "truefoo", "0x1", "1_000",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		want := json.Valid(data)
		err := zerojson.Valid(data)
		if got := err == nil; got != want {
			t.Fatalf("verdict mismatch\ninput: %q\nstdlib json.Valid: %v\nzerojson.Valid: %v (err=%v)", data, want, got, err)
		}
	})
}

// TestValidTruncationSweepMatchesStdlib decodes every prefix of every
// corpus message through both oracles and requires the *exact same*
// valid/invalid verdict at every prefix length — not merely "no panic"
// (truncation_test.go's decode sweep settles for that because decode
// success/failure legitimately differs by prefix depending on internal
// field-skip behavior; Valid has no such internal degrees of freedom, so
// nothing less than exact agreement is meaningful here).
func TestValidTruncationSweepMatchesStdlib(t *testing.T) {
	for _, data := range validSeedCorpus(t) {
		for i := 0; i <= len(data); i++ {
			prefix := data[:i:i]
			want := json.Valid(prefix)
			var got bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic validating prefix [:%d] of %q: %v", i, data, r)
					}
				}()
				got = zerojson.Valid(prefix) == nil
			}()
			if got != want {
				t.Fatalf("prefix [:%d] of %q: stdlib valid=%v, zerojson valid=%v", i, data, want, got)
			}
		}
	}
}

// budget runs fn and fails the test if it takes longer than max — a
// tripwire against a future accidental quadratic (or worse) regression in
// Valid's string/number scanning, not a precise performance assertion
// (hence "generous"; these inputs are pathological by construction, not
// representative of real traffic).
func budget(t *testing.T, name string, max time.Duration, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if elapsed := time.Since(start); elapsed > max {
		t.Fatalf("%s: took %v, want under %v", name, elapsed, max)
	}
}

// TestValidAdversarial exercises specific pathological shapes: nesting
// depth at the maxDepth boundary, multi-MB strings, long escape runs, and
// numbers with huge digit counts — each checked for both correctness
// (against live encoding/json.Valid, where that oracle can keep up) and a
// generous time budget.
func TestValidAdversarial(t *testing.T) {
	t.Run("depth_at_cap", func(t *testing.T) {
		for _, d := range []int{9999, 10000, 10001} {
			arr := []byte(strings.Repeat("[", d) + "1" + strings.Repeat("]", d))
			obj := []byte(strings.Repeat(`{"a":`, d) + "1" + strings.Repeat("}", d))
			for name, data := range map[string][]byte{"array": arr, "object": obj} {
				want := json.Valid(data)
				got := zerojson.Valid(data) == nil
				if got != want {
					t.Fatalf("%s depth=%d: stdlib valid=%v zerojson valid=%v", name, d, want, got)
				}
			}
		}
	})

	t.Run("multi_mb_string", func(t *testing.T) {
		big := append([]byte{'"'}, bytes.Repeat([]byte("a"), 8*1024*1024)...)
		big = append(big, '"')
		budget(t, "8MB plain string", 500*time.Millisecond, func() {
			if err := zerojson.Valid(big); err != nil {
				t.Fatalf("valid 8MB string rejected: %v", err)
			}
		})
		// A single invalid byte deep inside a multi-MB string must still
		// be caught (not skipped by an accidentally-too-coarse scan).
		bad := append([]byte(nil), big...)
		bad[4*1024*1024] = 0x01 // raw control byte, unescaped
		budget(t, "8MB string with one bad byte", 500*time.Millisecond, func() {
			if err := zerojson.Valid(bad); err == nil {
				t.Fatal("expected rejection of raw control byte inside long string")
			}
		})
	})

	t.Run("long_escape_run", func(t *testing.T) {
		esc := []byte(`"` + strings.Repeat(`\n`, 500000) + `"`)
		budget(t, "500k escapes", 500*time.Millisecond, func() {
			if err := zerojson.Valid(esc); err != nil {
				t.Fatalf("valid long escape run rejected: %v", err)
			}
		})
	})

	t.Run("pathological_numbers", func(t *testing.T) {
		cases := map[string]bool{
			"1e999999999":                      true, // grammar-valid; Valid never evaluates magnitude
			"-":                                false,
			"01":                               false,
			"1.":                               false,
			strings.Repeat("9", 500000):        true, // huge but grammatically fine mantissa
			"1e" + strings.Repeat("9", 100000): true,
		}
		for c, want := range cases {
			budget(t, "number "+c[:min(20, len(c))], 500*time.Millisecond, func() {
				got := zerojson.Valid([]byte(c)) == nil
				if got != want {
					t.Fatalf("Valid(%q...) = %v, want %v", c[:min(40, len(c))], got, want)
				}
			})
		}
	})
}
