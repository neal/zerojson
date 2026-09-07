package runtimeconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/neal/zerojson"
)

// jsontestsuite_test.go runs the industry-standard JSONTestSuite parsing
// corpus (github.com/nst/JSONTestSuite, vendored under
// testdata/jsontestsuite/ with its MIT LICENSE) against BOTH of
// zerojson's untrusted-boundary surfaces:
//
//   - zerojson.Valid, the full-payload grammar validator, whose
//     documented contract is exact verdict parity with
//     encoding/json.Valid. The corpus classifies each file as y_ (must
//     parse), n_ (must not parse), or i_ (implementation-defined). We
//     assert every y_ is Valid, every n_ is not, and every i_ verdict is
//     RECORDED in a golden file (testdata/jsontestsuite/i_golden.json) so
//     any future drift shows up as a diff — AND that every verdict, of
//     every class, matches encoding/json.Valid's verdict on the same
//     bytes (the parity contract, checked here on 318 real corpus files,
//     not just fuzzer-reachable inputs).
//
//   - the ReadInterface decode surface (JSON -> any), compared against
//     json.Unmarshal into an any. This surface is the trusted-path
//     decoder, which is deliberately lenient (see zerojson's package
//     doc): its leaf readers accept a bounded set of inputs that RFC 8259
//     — and thus encoding/json — reject. Those divergences are enumerated
//     below in decodeSurfaceDivergences, every one justified, and every
//     one in the single direction "zerojson accepts what stdlib rejects"
//     (leniency); a divergence in the other direction, or any new lenient
//     divergence outside the allowlist, fails the test.
//
// The corpus itself is the reason this layer exists: no constructor in
// this repo could express "\uD800\u" or "[.123]" or a UTF-16LE-BOM
// document; JSONTestSuite can, and does.

const jtsDir = "../testdata/jsontestsuite/test_parsing"

func jtsFiles(tb testing.TB) []string {
	tb.Helper()
	ents, err := os.ReadDir(jtsDir)
	if err != nil {
		tb.Fatalf("reading JSONTestSuite corpus (did the testdata vendor step run?): %v", err)
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) < 300 {
		tb.Fatalf("expected the full ~318-file JSONTestSuite corpus, found only %d", len(names))
	}
	sort.Strings(names)
	return names
}

func jtsRead(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(jtsDir, name))
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// TestJSONTestSuiteValid is the Valid-surface contract over the whole
// corpus: y_ parses, n_ does not, i_ matches its recorded golden verdict,
// and — for every file regardless of class — zerojson.Valid agrees with
// encoding/json.Valid (the documented parity contract).
func TestJSONTestSuiteValid(t *testing.T) {
	golden := loadIGolden(t)
	seenI := map[string]bool{}

	for _, name := range jtsFiles(t) {
		data := jtsRead(t, name)
		zjValid := zerojson.Valid(data) == nil
		stdValid := json.Valid(data)

		// The parity contract, on real corpus bytes.
		if zjValid != stdValid {
			t.Errorf("%s: zerojson.Valid=%v but encoding/json.Valid=%v (parity contract broken)", name, zjValid, stdValid)
		}

		switch name[0] {
		case 'y':
			if !zjValid {
				t.Errorf("%s: y_ file must be Valid, but zerojson rejected it", name)
			}
		case 'n':
			if zjValid {
				t.Errorf("%s: n_ file must be rejected, but zerojson accepted it", name)
			}
		case 'i':
			seenI[name] = true
			want, ok := golden[name]
			if !ok {
				t.Errorf("%s: i_ file has no recorded golden verdict — regenerate testdata/jsontestsuite/i_golden.json", name)
				continue
			}
			if zjValid != want {
				t.Errorf("%s: i_ verdict drifted: golden=%v now=%v (intentional? update i_golden.json)", name, want, zjValid)
			}
		}
	}

	// The golden file must not contain stale entries for i_ files that no
	// longer exist (or were reclassified), so drift is visible in both
	// directions.
	for name := range golden {
		if !seenI[name] {
			t.Errorf("i_golden.json has a verdict for %q, which is not an i_ corpus file anymore", name)
		}
	}
}

func loadIGolden(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "jsontestsuite", "i_golden.json"))
	if err != nil {
		t.Fatalf("reading i_golden.json: %v", err)
	}
	var m map[string]bool
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing i_golden.json: %v", err)
	}
	return m
}

// decodeSurfaceDivergences is the complete, justified allowlist of
// JSONTestSuite files on which zerojson's ReadInterface decode surface
// disagrees with json.Unmarshal-into-any. Every entry is a file
// encoding/json REJECTS but zerojson's lenient trusted-path leaf readers
// ACCEPT — the divergence is always in that one direction (leniency),
// never the reverse. There are exactly two classes:
//
//   - Lenient numbers: ReadInterface reads a numeric token with the
//     trusted-path bare-number scanner and hands it to strconv.ParseFloat
//     after a cheap grammar check (bareFloatOK) that rejects the forms
//     strconv would otherwise take — a leading '+', a missing integer
//     part, a bare/trailing '.'. The ONE remaining leniency is a leading
//     zero (`007`, `-01`), the documented trusted-path trade. (For
//     untrusted number grammar, callers use zerojson.Valid, which rejects
//     that too — see TestJSONTestSuiteValid above, where every one of
//     these files is a correctly-rejected n_.)
//
//   - Lenient strings: ReadInterface reads a string value with the
//     trusted-path readStringBytes/unescapeString, which accepts raw
//     unescaped control bytes, unknown/invalid backslash escapes, and
//     lone/incomplete/invalid \u surrogate escapes (replacing bad
//     escapes with U+FFFD the way encoding/json does only at the
//     value-unescape step, never as a grammar rejection). Object KEYS are
//     NOT in this class: ReadKey is grammar-strict (see strings.go), so a
//     bad key is rejected on both surfaces.
//
// A future change that made ReadInterface reject something stdlib accepts
// would surface as a NEW mismatch not in this map and fail the test; so
// would a new lenient acceptance in some third class.
var decodeSurfaceDivergences = map[string]string{
	// --- lenient numbers (strconv.ParseFloat accepts, RFC 8259 rejects) ---
	"n_number_-01.json":                        "leading zero after sign",
	"n_number_neg_int_starting_with_zero.json": "leading zero after sign",
	"n_number_with_leading_zero.json":          "leading zero",

	// --- lenient strings (readStringBytes/unescapeString accept, RFC rejects) ---
	"n_string_1_surrogate_then_escape_u.json":           "incomplete \\u escape after lone surrogate -> U+FFFD",
	"n_string_1_surrogate_then_escape_u1.json":          "incomplete \\u escape after lone surrogate -> U+FFFD",
	"n_string_1_surrogate_then_escape_u1x.json":         "invalid \\u hex after lone surrogate -> U+FFFD",
	"n_string_backslash_00.json":                        "raw NUL control byte in escape body",
	"n_string_escape_x.json":                            "unknown '\\x' escape passed through",
	"n_string_escaped_ctrl_char_tab.json":               "raw tab control byte after backslash",
	"n_string_escaped_emoji.json":                       "backslash before a raw multi-byte rune",
	"n_string_incomplete_escaped_character.json":        "3-hex-digit \\u escape",
	"n_string_incomplete_surrogate.json":                "incomplete second \\u surrogate -> U+FFFD",
	"n_string_incomplete_surrogate_escape_invalid.json": "invalid escape after surrogate pair",
	"n_string_invalid-utf-8-in-escape.json":             "non-hex byte inside \\u escape",
	"n_string_invalid_backslash_esc.json":               "unknown '\\a' escape passed through",
	"n_string_invalid_unicode_escape.json":              "non-hex '\\uqqqq' -> U+FFFD",
	"n_string_invalid_utf8_after_escape.json":           "invalid UTF-8 byte after backslash",
	"n_string_unescaped_ctrl_char.json":                 "raw unescaped control byte in string",
	"n_string_unescaped_newline.json":                   "raw unescaped newline in string",
	"n_string_unescaped_tab.json":                       "raw unescaped tab in string",
	"n_string_unicode_CapitalU.json":                    "unknown '\\U' escape passed through",
}

// TestJSONTestSuiteDecodeSurface compares zerojson's ReadInterface decode
// (JSON -> any, then Finish) against json.Unmarshal into any, error-vs-
// success, for every corpus file. Agreement is required except on the
// enumerated, justified decodeSurfaceDivergences; an unlisted mismatch —
// in either direction — fails.
func TestJSONTestSuiteDecodeSurface(t *testing.T) {
	usedAllow := map[string]bool{}

	for _, name := range jtsFiles(t) {
		data := jtsRead(t, name)

		var v any
		stdOK := json.Unmarshal(data, &v) == nil
		zjOK := zjDecodeInterface(t, name, data) == nil

		if stdOK == zjOK {
			if _, listed := decodeSurfaceDivergences[name]; listed {
				t.Errorf("%s: listed as a decode divergence but surfaces now AGREE (std=%v zj=%v) — remove the allowlist entry", name, stdOK, zjOK)
			}
			continue
		}

		reason, listed := decodeSurfaceDivergences[name]
		if !listed {
			t.Errorf("%s: UNDOCUMENTED decode divergence: stdlib ok=%v, zerojson ok=%v — real finding or new allowlist entry needed", name, stdOK, zjOK)
			continue
		}
		usedAllow[name] = true
		// Every allowlisted divergence must be the lenient direction only.
		if !(zjOK && !stdOK) {
			t.Errorf("%s: allowlisted divergence is in the WRONG direction (zerojson rejects what stdlib accepts): %s", name, reason)
		}
	}

	for name := range decodeSurfaceDivergences {
		if !usedAllow[name] {
			t.Errorf("decodeSurfaceDivergences lists %q, but it did not actually diverge — the surfaces agree, so drop the stale entry", name)
		}
	}
}

// zjDecodeInterface decodes data through the ReadInterface any-decode
// surface, returning the resulting error (and never panicking — a panic
// here is itself a reportable bug, not a normal error result).
func zjDecodeInterface(t *testing.T, name string, data []byte) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: ReadInterface PANICKED on corpus input (bug): %v", name, r)
		}
	}()
	l := zerojson.NewLexer(data, false)
	_ = l.ReadInterface()
	l.Finish()
	return l.Err()
}
