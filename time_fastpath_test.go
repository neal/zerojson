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
