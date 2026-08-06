// Package runtimeconformance holds differential runtime tests that need an
// external oracle (google/uuid, easyjson) and so can't live in the
// zero-dependency root module. See zerojson's lexer_test.go/writer_test.go
// for the dependency-free vector versions of these same checks, captured
// once from these same oracles.
package runtimeconformance

import (
	"testing"

	"github.com/google/uuid"

	"github.com/neal/zerojson"
)

// TestReadUUIDBytesMatchesParseBytes validates zerojson's fixed-offset UUID
// fast path and its fallback against google/uuid live, across formats and
// malformed input.
func TestReadUUIDBytesMatchesParseBytes(t *testing.T) {
	cases := []string{
		`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // canonical lowercase
		`"B070C9C9-EDAA-4ADC-ADBE-CF1D2D66C1E1"`, // uppercase
		`"B070c9C9-eDaa-4adC-adBe-cf1d2d66c1E1"`, // mixed
		`"00000000-0000-0000-0000-000000000000"`,
		`"ffffffff-ffff-ffff-ffff-ffffffffffff"`,
		`"{b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1}"`,        // braced (fallback)
		`"urn:uuid:b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // urn (fallback)
		`"URN:UUID:b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1"`, // urn, uppercase prefix
		`"b070c9c9edaa4adcadbecf1d2d66c1e1"`,              // no dashes (fallback)
		`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1eg"`,          // bad hex
		`"b070c9c9+edaa-4adc-adbe-cf1d2d66c1e1"`,          // bad dash
		`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1"`,            // short
		`"b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1ff"`,        // long
		// 38-byte values whose wrapper bytes are NOT braces. google/uuid's
		// ParseBytes strips only the leading byte and ignores the trailing
		// one (it never validates the braces), so it ACCEPTS these — and so
		// must zerojson, reading the same 36 middle bytes. The expected
		// verdict comes from the oracle below, so these pin agreement with
		// google/uuid's actual (lenient) behavior, not an invented stricter
		// one.
		`"xb070c9c9-edaa-4adc-adbe-cf1d2d66c1e1y"`, // non-brace wrapper: accepted by the oracle
		`"{b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1]"`, // open brace, wrong close: still accepted
		`"[b070c9c9-edaa-4adc-adbe-cf1d2d66c1e1}"`, // wrong open, close brace: still accepted
	}
	for _, c := range cases {
		var wantU [16]byte
		wantErr := false
		u, err := uuid.ParseBytes([]byte(c[1 : len(c)-1]))
		if err != nil {
			wantErr = true
		} else {
			wantU = [16]byte(u)
		}

		l := zerojson.NewLexer([]byte(c), false)
		gotU := l.ReadUUIDBytes()
		gotErr := l.Err() != nil

		if wantErr != gotErr {
			t.Errorf("%s: error mismatch: uuid pkg err=%v zerojson err=%v", c, wantErr, l.Err())
			continue
		}
		if !wantErr && wantU != gotU {
			t.Errorf("%s: value mismatch: want %v got %v", c, wantU, gotU)
		}
	}
}
