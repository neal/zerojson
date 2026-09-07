package zerojson

import (
	"testing"
	"time"
)

// TestParseRFC3339ZFastPath pins the RFC3339-Z fast path directly, independent
// of ReadTime's time.Parse fallback (which masks fast-path defects because it
// produces the identical result). Regression for a digit-validation bug where
// the per-field check OR-ed digit *values* (`bad |= hi | lo`) and rejected on
// `bad > 9` — two valid digits can OR above 9 (e.g. 2|9 == 11), so any
// timestamp carrying both a digit >= 8 and a digit in 2..7 was silently kicked
// to the slow fallback. These cases all contain such digits and must be served
// by the fast path.
func TestParseRFC3339ZFastPath(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"2026-01-29T15:24:38Z", true},  // day 29 + 8/9 + 2..7 digits
		{"2026-08-09T18:29:47Z", true},  // dense 8/9 with 2..7
		{"2020-02-29T00:00:00Z", true},  // valid leap day — fast path must accept
		{"2021-02-29T00:00:00Z", false}, // invalid leap day — fast path must reject
		{"2019-02-28T23:59:59Z", true},  // day 28, non-leap
		{"2026-13-01T00:00:00Z", false}, // month out of range
		{"2026-01-32T00:00:00Z", false}, // day out of range
		{"2026-01-08T24:00:00Z", false}, // hour out of range
	}
	for _, c := range cases {
		got, ok := parseRFC3339Z([]byte(c.in))
		if ok != c.ok {
			t.Errorf("parseRFC3339Z(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok {
			want, err := time.Parse(time.RFC3339, c.in)
			if err != nil {
				t.Errorf("%q: fast path accepted but time.Parse rejected: %v", c.in, err)
			} else if !got.Equal(want) {
				t.Errorf("%q: fast path = %v, want %v", c.in, got, want)
			}
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

func TestEscapedTimestamp(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2024-05-06T07:08:09Z")
	l := NewLexer([]byte(`"\u0032024-05-06T07:08:09Z"`), false) // \u0032 is "2"
	if got := l.ReadTime(); l.Err() != nil || !got.Equal(want) {
		t.Fatalf("escaped timestamp: %v err=%v", got, l.Err())
	}
	l = NewLexer([]byte(`"2024-05-06T07:08:09\u005a"`), false)
	if got := l.ReadTime(); l.Err() != nil || got.Year() != 2024 || got.Second() != 9 {
		t.Fatalf("escaped Z timestamp: %v err=%v", got, l.Err())
	}
}
