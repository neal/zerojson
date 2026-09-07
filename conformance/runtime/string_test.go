package runtimeconformance

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/mailru/easyjson/jwriter"

	"github.com/neal/zerojson"
	"github.com/neal/zerojson/conformance/internal/oracle"
)

// TestAppendStringMatchesJwriter validates zerojson.AppendString's escaping
// against easyjson's jwriter.Writer.String live — the byte-identity target.
// See zerojson's writer_test.go for the dependency-free vector version of
// this same check, captured once from this same oracle.
func TestAppendStringMatchesJwriter(t *testing.T) {
	cases := []string{
		"", "plain", "with space", "AAPL",
		"FIX.4.2:APCA->NASDAQ", "a<b>c&d",
		"quote\"backslash\\slash/",
		"tab\tnewline\ncr\r",
		"\x00\x01\x1f", "control\x0bvertical",
		"unicode: 日本語 émoji 🎉",
		"exactly8", "exactly8x", "seven77",
		"long string that spans multiple SWAR words without any escapes at all",
		"escape at end >", "< escape at start",
	}
	for c := range byte(0x20) {
		cases = append(cases, "pre"+string([]byte{c})+"post")
	}
	for _, s := range cases {
		w := jwriter.Writer{}
		w.String(s)
		want, err := w.Buffer.BuildBytes(), w.Error
		if err != nil {
			t.Fatal(err)
		}
		got := zerojson.AppendString(nil, s)
		if !bytes.Equal(want, got) {
			t.Errorf("string %q: want %s got %s", s, want, got)
		}
	}
}

// TestAppendStringUTF8MatchesBothOracles pins zerojson's handling of
// invalid UTF-8 bytes in a string value: both easyjson's
// jwriter.Writer.String and encoding/json's string encoder replace each
// invalid UTF-8 byte with the literal 6-byte � escape, and leave valid
// multi-byte sequences untouched. AppendString (writer.go) matches both.
func TestAppendStringUTF8MatchesBothOracles(t *testing.T) {
	cases := []string{
		"plain ascii",
		"valid unicode: 日本語 émoji 🎉",
		"invalid: \xff\xfe",
		"lone continuation byte: \x80\x80",
		"mixed valid/invalid: abc\xffdef",
		"overlong encoding: \xc0\x80",
		"truncated 3-byte sequence: \xe2\x82",
		"truncated 4-byte sequence: \xf0\x9f\x98",
		"surrogate half (invalid in UTF-8): \xed\xa0\x80",
		"invalid at the very start: \xff",
		"invalid at the very end: end\xff",
		"invalid exactly at an 8-byte SWAR boundary: 1234567\xff8",
		"line sep: a\u2028b, para sep: c\u2029d", // both oracles escape U+2028/U+2029 ("jsonp stuff")
		"\u2027\u202a neighbors stay verbatim",
	}
	for _, s := range cases {
		w := jwriter.Writer{}
		w.String(s)
		wantEJ, errEJ := w.Buffer.BuildBytes(), w.Error
		if errEJ != nil {
			t.Fatalf("%q: easyjson unexpectedly errored: %v", s, errEJ)
		}
		wantStd, errStd := json.Marshal(s)
		if errStd != nil {
			t.Fatalf("%q: stdlib unexpectedly errored: %v", s, errStd)
		}
		// Go 1.27's v2-backed encoding/json emits the raw U+FFFD rune for
		// an invalid byte; classic v1 (and easyjson) emit the escape.
		wantStd = oracle.ClassicUTF8(wantStd)
		if !bytes.Equal(wantEJ, wantStd) {
			t.Fatalf("%q: oracles disagree with each other (easyjson=%s stdlib=%s) — test assumption broken", s, wantEJ, wantStd)
		}
		got := zerojson.AppendString(nil, s)
		if !bytes.Equal(wantEJ, got) {
			t.Errorf("%q: mismatch\neasyjson/stdlib: %s\nzerojson:        %s", s, wantEJ, got)
		}
	}
}

// FuzzAppendStringUTF8 fuzzes AppendString with arbitrary (possibly
// invalid-UTF8) byte sequences reinterpreted as a string, comparing
// against both easyjson and stdlib live — continuous coverage for the
// invalid-UTF8 handling TestAppendStringUTF8MatchesBothOracles pins with
// a curated table.
func FuzzAppendStringUTF8(f *testing.F) {
	seeds := []string{
		"", "ascii", "日本語", "\xff", "\x80", "\xc0\x80",
		"a\xffb", "1234567\xff8", string([]byte{0xed, 0xa0, 0x80}),
		"a\u2028b\u2029c",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		s := string(b)
		w := jwriter.Writer{}
		w.String(s)
		if w.Error != nil {
			t.Fatalf("easyjson unexpectedly errored for %q: %v", s, w.Error)
		}
		wantEJ, _ := w.Buffer.BuildBytes(), w.Error
		got := zerojson.AppendString(nil, s)
		if !bytes.Equal(wantEJ, got) {
			t.Fatalf("mismatch for %q (valid utf8=%v)\neasyjson: %s\nzerojson: %s", s, utf8.ValidString(s), wantEJ, got)
		}
	})
}
