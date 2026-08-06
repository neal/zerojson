package zerojson

import (
	"encoding/json"
	"strings"
	"testing"
)

// api_edge_test.go pins exported API and defensive edge cases first found by
// a merged coverage run (root module + conformance suites, -coverpkg the root
// package). Each test here targets one otherwise-hard-to-reach branch by name;
// the two branches that are provably unreachable are documented at the
// bottom rather than tested, with the reasoning that makes them dead
// (learned from the once-"unreachable"-but-actually-live RFC3339 zone
// check: an unreachable claim has to be argued, not assumed).

// TestAddErrorFirstErrorWins hits the assignment branch of AddError (a delegated
// UnmarshalJSON error recorded into a fresh Lexer).
func TestAddErrorFirstErrorWins(t *testing.T) {
	var l Lexer
	l.AddError(errString("boom"))
	if l.Err() == nil || l.Err().Error() != "boom" {
		t.Fatalf("AddError did not record: %v", l.Err())
	}
	// A second error must not overwrite the first.
	l.AddError(errString("second"))
	if l.Err().Error() != "boom" {
		t.Fatalf("AddError overwrote the first error: %v", l.Err())
	}
	// A nil error is a no-op even on a clean lexer.
	var l2 Lexer
	l2.AddError(nil)
	if l2.Err() != nil {
		t.Fatalf("AddError(nil) set an error: %v", l2.Err())
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestLexerDelimiterHelpers exercises the two exported helpers that the
// generated code does not call but that remain public API for
// hand-written decoders.
func TestLexerDelimiterHelpers(t *testing.T) {
	l := NewLexer([]byte("  , x"), false)
	if !l.IsDelim(',') {
		t.Fatal("IsDelim(',') = false, want true (whitespace-skipped)")
	}
	if l.IsDelim('}') {
		t.Fatal("IsDelim('}') = true, want false")
	}
	l.WantComma() // consumes the comma
	if l.IsDelim(',') {
		t.Fatal("WantComma did not consume the comma")
	}
	// WantComma is a no-op when the next byte is not a comma.
	before := l.Pos()
	l.WantComma()
	if l.Pos() != before {
		t.Fatalf("WantComma consumed a non-comma byte: %d -> %d", before, l.Pos())
	}
}

// TestSkipPreservesExistingErrorPosition hits Skip's guard that returns immediately when
// the Lexer already carries an error.
func TestSkipPreservesExistingErrorPosition(t *testing.T) {
	l := NewLexer([]byte("123"), false)
	l.AddError(errString("pre-existing"))
	posBefore := l.Pos()
	l.Skip()
	if l.Pos() != posBefore {
		t.Fatalf("Skip advanced past an existing error: %d -> %d", posBefore, l.Pos())
	}
}

// TestLexerDataAndPosition exercises the Data/Pos accessors.
func TestLexerDataAndPosition(t *testing.T) {
	in := []byte(`{"a":1}`)
	l := NewLexer(in, false)
	if got := l.Data(); string(got) != string(in) {
		t.Fatalf("Data() = %q, want %q", got, in)
	}
	if l.Pos() != 0 {
		t.Fatalf("Pos() = %d, want 0", l.Pos())
	}
}

// TestReadInterfaceDepthExceeded hits the max-nesting-depth error
// branches of ReadInterface for BOTH objects and arrays (each has its own
// depth check; the merged run showed the object branch uncovered because
// the linearity suite only nests to 8000, under the 10000 cap).
func TestReadInterfaceDepthExceeded(t *testing.T) {
	deep := maxDepth + 1
	cases := map[string][]byte{
		"array":  []byte(strings.Repeat("[", deep)),
		"object": []byte(strings.Repeat(`{"a":`, deep)),
	}
	for name, data := range cases {
		l := NewLexer(data, false)
		_ = l.ReadInterface()
		if l.Err() == nil {
			t.Fatalf("%s: ReadInterface accepted depth %d > maxDepth %d", name, deep, maxDepth)
		}
		if !strings.Contains(l.Err().Error(), "depth") {
			t.Fatalf("%s: expected a depth error, got %v", name, l.Err())
		}
	}
}

// TestReadTimeFastPathFallbacks hits the parseRFC3339Z fast-path
// rejection branches that fall back to time.Parse: a non-digit where a
// digit is required, and a '.' with zero fractional digits.
func TestReadTimeFastPathFallbacks(t *testing.T) {
	for _, s := range []string{
		`"2O26-01-01T00:00:00Z"`,  // letter 'O' in the year: d2 sets bad=true
		`"1970-01-01T00:00:00.Z"`, // '.' then no fraction digits: n == 0
	} {
		l := NewLexer([]byte(s), false)
		_ = l.ReadTime()
		if l.Err() == nil {
			t.Fatalf("ReadTime(%s) accepted a malformed timestamp", s)
		}
	}
}

// TestReadTimeYearZero decodes the year-0000 UTC timestamp, whose
// pre-1970 day-count arithmetic drives floorDiv into its q-- adjustment
// branch (a negative dividend with a nonzero remainder).
func TestReadTimeYearZero(t *testing.T) {
	l := NewLexer([]byte(`"0000-01-01T00:00:00Z"`), false)
	tm := l.ReadTime()
	if l.Err() != nil {
		t.Fatalf("ReadTime year 0000 errored: %v", l.Err())
	}
	if tm.Year() != 0 || tm.Month() != 1 || tm.Day() != 1 {
		t.Fatalf("ReadTime year 0000 = %v", tm)
	}
}

// TestParseUUIDBytesFallbacks hits two non-canonical parseUUIDBytes
// rejection branches: a 45-byte value that is NOT urn:uuid:-prefixed, and
// a 32-byte un-hyphenated value with a non-hex digit.
func TestParseUUIDBytesFallbacks(t *testing.T) {
	// 45 content bytes (36 + 9), first 9 bytes != "urn:uuid:".
	nonURN := `"xxxxxxxxx01234567-89ab-cdef-0123-456789abcdef"`
	if len(nonURN) != 45+2 {
		t.Fatalf("test setup: want 45 content bytes, got %d", len(nonURN)-2)
	}
	l := NewLexer([]byte(nonURN), false)
	_ = l.ReadUUIDBytes()
	if l.Err() == nil {
		t.Fatal("ReadUUIDBytes accepted a non-urn 45-byte value")
	}
	// 32 hex chars but the last is 'Z'.
	badHex := `"0123456789abcdef0123456789abcdeZ"`
	l2 := NewLexer([]byte(badHex), false)
	_ = l2.ReadUUIDBytes()
	if l2.Err() == nil {
		t.Fatal("ReadUUIDBytes accepted a 32-byte value with a non-hex digit")
	}
}

// TestSyntaxErrorError calls SyntaxError.Error via a real Valid
// failure.
func TestSyntaxErrorError(t *testing.T) {
	err := Valid([]byte("{bad"))
	if err == nil {
		t.Fatal("Valid accepted malformed input")
	}
	if err.Error() == "" {
		t.Fatal("SyntaxError.Error() returned empty string")
	}
	if _, ok := err.(*SyntaxError); !ok {
		t.Fatalf("Valid returned %T, want *SyntaxError", err)
	}
}

// TestAppendInterfaceMarshalError hits AppendInterface's json.Marshal
// error path with a value encoding/json cannot marshal.
func TestAppendInterfaceMarshalError(t *testing.T) {
	dst := []byte("PFX")
	got, err := AppendInterface(dst, make(chan int))
	if err == nil {
		t.Fatal("AppendInterface(chan) returned nil error")
	}
	// On error the returned buffer is unchanged (the prefix, no appended value).
	if string(got) != "PFX" {
		t.Fatalf("AppendInterface on error mutated dst: %q", got)
	}
	// Sanity: the same value fails json.Marshal directly (the analogue).
	if _, e := json.Marshal(make(chan int)); e == nil {
		t.Fatal("json.Marshal(chan) unexpectedly succeeded")
	}
}

// --- Provably unreachable branches (documented, not tested) ---
//
// Two runtime branches remain uncovered because no input can reach them.
// Each argument is grounded in the surrounding code, not assumed:
//
//  1. strings.go, unescapeString: the `i++; if i >= len(b) { break }`
//     guard fires only when a backslash is the LAST byte of the content
//     handed to unescapeString. But unescapeString is only ever called on
//     content returned by readStringBytes/readStringSlow, and
//     readStringSlow advances past the byte AFTER every backslash it sees
//     (`case '\\': i++`) before it can accept a closing quote — so the
//     content it returns can never end in a lone trailing backslash (a
//     backslash immediately followed by the closing quote is consumed as
//     an escaped quote, not a terminator; a backslash with nothing after
//     it yields the "unterminated string" error and a nil content, whose
//     len is 0). The guard is correct defensive code for a caller that
//     does not exist; it stays.
//
//  2. strings.go, ReadKey: the `if !plain { b = []byte(unescapeString(b)) }`
//     inside the non-string else branch is unreachable. That else branch
//     is entered only when the byte at the cursor is NOT '"'; it then calls
//     readStringBytes, whose first act is to check that same cursor byte is
//     '"' and, finding it is not, record "expected string" and return
//     (nil, true) — plain is always true here, so the body never runs. The
//     branch mirrors the plain handling of the string path defensively; it
//     is dead only because the dispatch above already guarantees the
//     not-a-string case.
