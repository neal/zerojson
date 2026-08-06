package zerojson

import (
	"bytes"
	"math"
	"strconv"
	"testing"
	"time"
)

func TestAppendTimeMatchesMarshalJSON(t *testing.T) {
	locs := []*time.Location{
		time.UTC,
		time.FixedZone("IST", 5*3600+1800),
		time.FixedZone("EDT", -4*3600),
	}
	base := time.Date(2026, 7, 2, 8, 26, 39, 0, time.UTC)
	nanos := []int{
		0, 1, 999, 1000, 123000, 170079000, 500000000, 999999999,
		100000000, 120000000, 123456789,
	}
	for _, loc := range locs {
		for _, ns := range nanos {
			tm := base.Add(time.Duration(ns)).In(loc)
			want, err := tm.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			got, err := AppendTime(nil, tm)
			if err != nil {
				t.Fatalf("time %v: %v", tm, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("time %v: want %s got %s", tm, want, got)
			}
		}
	}
	// Zero time.
	var zero time.Time
	want, _ := zero.MarshalJSON()
	if got, err := AppendTime(nil, zero); err != nil || !bytes.Equal(want, got) {
		t.Errorf("zero time: want %s got %s (err %v)", want, got, err)
	}

	// Years outside [0,9999]: both time.Time.MarshalJSON (and therefore
	// encoding/json) and easyjson (which delegates time fields to that
	// same MarshalJSON) refuse with an error — AppendTime must too,
	// never silently emitting a non-RFC3339 string.
	for _, tm := range []time.Time{
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond),
	} {
		if _, err := tm.MarshalJSON(); err == nil {
			t.Fatalf("oracle assumption broken: stdlib accepted %v", tm)
		}
		dst := []byte("prefix")
		got, err := AppendTime(dst, tm)
		if err == nil {
			t.Errorf("time %v: want error, got output %s", tm, got)
		}
		if string(got) != "prefix" {
			t.Errorf("time %v: dst modified on error path: %s", tm, got)
		}
	}
}

// TestAppendTimeZoneOffsetRange pins AppendTime against time.Time.MarshalJSON
// across adversarial FixedZone offsets: an offset of a whole day or more is
// not RFC3339-representable and the oracle errors ("timezone hour outside of
// range [0,23]") — AppendTime must error too rather than emit an invalid
// "+24:00"-style string, or (at >= 100h) panic indexing append2's digit-pair
// table. Also covers the boundary (±23:59:59), non-minute-aligned
// seconds-bearing offsets (truncated to whole minutes by both, not an
// error), and the ±14h real-world extremes.
func TestAppendTimeZoneOffsetRange(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	offsets := []int{
		0, 60, 90, -90, 3600, 3690, 3599, -3600,
		-30, -1, -59, 30, 59, // sub-minute: truncate to 0min; sign follows the truncated value ("+00:00")
		14 * 3600, -14 * 3600, // real-world extremes
		23*3600 + 59*60 + 59, -(23*3600 + 59*60 + 59), // last valid magnitude (seconds truncated)
		86340, 86399, -86399, // 23:59, seconds-bearing but still hour 23
		86400, -86400, // exactly 24h: oracle errors
		86460, 90000, // just over / well over 24h
		100 * 3600, -100 * 3600, // the panic repro: hour field 3 digits
		359999, -359999,
		86340, -86340, // 24*3600-60: legal extreme (whole day minus a minute)
		math.MaxInt, math.MinInt + 1, math.MinInt, // integer extremes; MinInt overflows a magnitude negation
	}
	for _, off := range offsets {
		tm := base.In(time.FixedZone("x", off))
		want, wantErr := tm.MarshalJSON()

		dst := []byte("keep")
		got, gotErr := AppendTime(dst, tm)

		if (gotErr != nil) != (wantErr != nil) {
			t.Errorf("offset %d: error mismatch: stdlib err=%v zerojson err=%v", off, wantErr, gotErr)
			continue
		}
		if wantErr != nil {
			if string(got) != "keep" {
				t.Errorf("offset %d: dst modified on error path: %q", off, got)
			}
			continue
		}
		if string(got) != "keep"+string(want) {
			t.Errorf("offset %d: want %q got %q", off, "keep"+string(want), got)
		}
	}
}

// TestAppendTimeMinIntOffsetNoPanic exercises the SIGNED offset bounds-check on
// the one input that a magnitude-then-compare check mishandles: a zone offset of
// exactly math.MinInt, whose negation overflows back to math.MinInt (still
// negative) and so slips past a "|offset| >= 24h" test, then panics indexing
// append2's digit-pair table. Reaching that check requires the LOCAL year to be
// in [0,9999] (the year guard runs first); an absolute instant near 2^63 seconds
// past year 1 shifted by the ~-2^63s offset lands the local year back in range.
// Pinned against time.Time.MarshalJSON for error parity and exact error text.
func TestAppendTimeMinIntOffsetNoPanic(t *testing.T) {
	if strconv.IntSize != 64 {
		// On 32-bit systems the ordinary offset-range table above reaches the
		// math.MinInt offset check while keeping the local year in range. Only
		// a 64-bit offset shifts the ordinary fixture so far that this specially
		// constructed absolute year is needed.
		t.Skip("64-bit-only construction; 32-bit math.MinInt is covered by TestAppendTimeZoneOffsetRange")
	}
	// abs year chosen so (abs + math.MinInt seconds) has a local year in range.
	year := int64(292277025000)
	tm := time.Date(int(year), 1, 1, 0, 0, 0, 0, time.UTC).In(time.FixedZone("x", math.MinInt))
	if ly := tm.Year(); ly < 0 || ly > 9999 {
		t.Fatalf("test precondition broken: local year %d not in [0,9999], offset check unreachable", ly)
	}
	want, wantErr := tm.MarshalJSON()
	if wantErr == nil {
		t.Fatalf("oracle assumption broken: MarshalJSON accepted a math.MinInt offset: %s", want)
	}
	dst := []byte("keep")
	got, gotErr := AppendTime(dst, tm) // must not panic
	if gotErr == nil {
		t.Fatalf("want error for math.MinInt offset, got output %q", got)
	}
	if gotErr.Error() != wantErr.Error() {
		t.Errorf("error text mismatch: zerojson %q, MarshalJSON %q", gotErr, wantErr)
	}
	if string(got) != "keep" {
		t.Errorf("dst modified on error path: %q", got)
	}
}

// jU is a shorthand for a JSON \u00XX escape sequence: backslash, 'u',
// then the 4-digit lowercase hex code point. Building the "want" vectors
// below out of these instead of raw backtick literals keeps them
// unambiguous to read (a literal backslash character is easy to
// miscount by eye).
func jU(hex string) string { return "\\u" + hex }

// TestAppendStringMatchesJwriterVectors validates AppendString's escaping
// (including the HTML set '<' '>' '&' and all control bytes) against
// precomputed vectors — captured once from easyjson's jwriter.Writer.String,
// which is the byte-identity target — so the expectations stay pinned
// without the root module depending on easyjson. The live differential
// version (run against jwriter directly) lives in the conformance module.
func TestAppendStringMatchesJwriterVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", `""`},
		{"plain", `"plain"`},
		{"with space", `"with space"`},
		{"AAPL", `"AAPL"`},
		{"FIX.4.2:APCA->NASDAQ", `"FIX.4.2:APCA-` + jU("003e") + `NASDAQ"`},
		{"a<b>c&d", `"a` + jU("003c") + `b` + jU("003e") + `c` + jU("0026") + `d"`},
		{"quote\"backslash\\slash/", `"quote\"backslash\\slash/"`},
		{"tab\tnewline\ncr\r", `"tab\tnewline\ncr\r"`},
		{"\x00\x01\x1f", `"` + jU("0000") + jU("0001") + jU("001f") + `"`},
		{"control\x0bvertical", `"control` + jU("000b") + `vertical"`},
		{"unicode: 日本語 émoji 🎉", `"unicode: 日本語 émoji 🎉"`},
		{"exactly8", `"exactly8"`},
		{"exactly8x", `"exactly8x"`},
		{"seven77", `"seven77"`},
		{"long string that spans multiple SWAR words without any escapes at all",
			`"long string that spans multiple SWAR words without any escapes at all"`},
		{"escape at end >", `"escape at end ` + jU("003e") + `"`},
		{"< escape at start", `"` + jU("003c") + ` escape at start"`},
		{"pre\x00post", `"pre` + jU("0000") + `post"`},
		{"pre\x01post", `"pre` + jU("0001") + `post"`},
		{"pre\x02post", `"pre` + jU("0002") + `post"`},
		{"pre\x03post", `"pre` + jU("0003") + `post"`},
		{"pre\x04post", `"pre` + jU("0004") + `post"`},
		{"pre\x05post", `"pre` + jU("0005") + `post"`},
		{"pre\x06post", `"pre` + jU("0006") + `post"`},
		{"pre\x07post", `"pre` + jU("0007") + `post"`},
		{"pre\x08post", `"pre` + jU("0008") + `post"`},
		{"pre\tpost", `"pre\tpost"`},
		{"pre\npost", `"pre\npost"`},
		{"pre\x0bpost", `"pre` + jU("000b") + `post"`},
		{"pre\x0cpost", `"pre` + jU("000c") + `post"`},
		{"pre\rpost", `"pre\rpost"`},
		{"pre\x0epost", `"pre` + jU("000e") + `post"`},
		{"pre\x0fpost", `"pre` + jU("000f") + `post"`},
		{"pre\x10post", `"pre` + jU("0010") + `post"`},
		{"pre\x11post", `"pre` + jU("0011") + `post"`},
		{"pre\x12post", `"pre` + jU("0012") + `post"`},
		{"pre\x13post", `"pre` + jU("0013") + `post"`},
		{"pre\x14post", `"pre` + jU("0014") + `post"`},
		{"pre\x15post", `"pre` + jU("0015") + `post"`},
		{"pre\x16post", `"pre` + jU("0016") + `post"`},
		{"pre\x17post", `"pre` + jU("0017") + `post"`},
		{"pre\x18post", `"pre` + jU("0018") + `post"`},
		{"pre\x19post", `"pre` + jU("0019") + `post"`},
		{"pre\x1apost", `"pre` + jU("001a") + `post"`},
		{"pre\x1bpost", `"pre` + jU("001b") + `post"`},
		{"pre\x1cpost", `"pre` + jU("001c") + `post"`},
		{"pre\x1dpost", `"pre` + jU("001d") + `post"`},
		{"pre\x1epost", `"pre` + jU("001e") + `post"`},
		{"pre\x1fpost", `"pre` + jU("001f") + `post"`},

		// Invalid UTF-8: both easyjson (jwriter.Writer.String) and
		// encoding/json (its string encoder) replace each invalid byte
		// with the literal 6-byte � escape (NOT U+FFFD's own raw
		// 3-byte UTF-8 encoding, and NOT a raw passthrough of the
		// invalid byte). See conformance/string_test.go's
		// TestAppendStringUTF8MatchesBothOracles.
		{"invalid: \xff\xfe", `"invalid: ` + jU("fffd") + jU("fffd") + `"`},
		{"lone continuation: \x80\x80", `"lone continuation: ` + jU("fffd") + jU("fffd") + `"`},
		{"mixed: abc\xffdef", `"mixed: abc` + jU("fffd") + `def"`},
		{"overlong: \xc0\x80", `"overlong: ` + jU("fffd") + jU("fffd") + `"`},
		{"truncated 3-byte: \xe2\x82", `"truncated 3-byte: ` + jU("fffd") + jU("fffd") + `"`},

		// U+2028/U+2029 (line/paragraph separator): both easyjson
		// ("jsonp stuff" in jwriter.Writer.String) and encoding/json
		// escape these two — and only these two — otherwise-valid
		// runes, for JavaScript-source compatibility. Nearby code points
		// (U+2027, U+202A) stay verbatim. Written as explicit \u escapes
		// in the Go source so no invisible character can silently corrupt
		// the vector.
		{"a\u2028b\u2029c", `"a` + jU("2028") + `b` + jU("2029") + `c"`},
		{"\u2028", `"` + jU("2028") + `"`},
		{"ends with sep\u2029", `"ends with sep` + jU("2029") + `"`},
		{"\u2027\u202a untouched neighbors", "\"\u2027\u202a untouched neighbors\""},
	}
	for _, c := range cases {
		got := AppendString(nil, c.in)
		if !bytes.Equal([]byte(c.want), got) {
			t.Errorf("string %q: want %s got %s", c.in, c.want, got)
		}
	}
}
