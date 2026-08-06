package runtimeconformance

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/neal/zerojson"
)

// FuzzReadUUIDBytesMatchesParseBytes requires zerojson's ReadUUIDBytes to
// agree with google/uuid's ParseBytes on accept-vs-reject (and on the
// decoded value when both accept) for arbitrary inputs. This is the
// error-agreement oracle the finding asks for: the fallback's brace bug
// (accepting any 38-byte value by stripping its first/last byte) escaped
// precisely because there was no such differential guard on the fallback
// path, only a fixed table.
//
// zerojson claims exactly google/uuid's ParseBytes format set — canonical
// 36-char, braced 38, urn:uuid: 45, un-hyphenated 32 — so the two must
// match with no documented divergence.
func FuzzReadUUIDBytesMatchesParseBytes(f *testing.F) {
	seeds := []string{
		"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1",
		"B070C9C9-EDAA-4ADC-ADBE-CF1D2D66C1E1",
		"{b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1}",
		"urn:uuid:b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1",
		"b070c9c9edaa4adcadbecf1d2d66c1e1",
		"xb070c9c9-edaa-4adc-adbe-cf1d2d66c1e1y",
		"00000000-0000-0000-0000-000000000000",
		"not-a-uuid",
		"",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, s []byte) {
		// A quote or backslash would change JSON string tokenization, so the
		// quoted-token content would differ from s itself; skip those so
		// both sides see the exact same bytes.
		if bytes.ContainsAny(s, "\"\\") {
			return
		}

		wantU, err := uuid.ParseBytes(s)
		wantErr := err != nil

		quoted := make([]byte, 0, len(s)+2)
		quoted = append(quoted, '"')
		quoted = append(quoted, s...)
		quoted = append(quoted, '"')

		l := zerojson.NewLexer(quoted, false)
		gotU := l.ReadUUIDBytes()
		gotErr := l.Err() != nil

		if wantErr != gotErr {
			t.Fatalf("%q: accept/reject mismatch: uuid.ParseBytes err=%v, zerojson err=%v", s, err, l.Err())
		}
		if !wantErr && [16]byte(wantU) != gotU {
			t.Fatalf("%q: value mismatch: uuid.ParseBytes=%x, zerojson=%x", s, [16]byte(wantU), gotU)
		}
	})
}
