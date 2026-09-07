package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"
)

// TestOmitZeroByteIdenticalToStdlib pins omitzero's Go 1.24 stdlib
// semantics against encoding/json (which implements the identical
// option) — deliberately NOT easyjson, which ignores omitzero outright
// (see the README's compatibility-contract caveat). It also pins the one
// place omitzero diverges from omitempty: a non-nil, zero-length slice
// is "empty" (omitempty would drop it) but not "zero" (its zero value is
// nil, so omitzero keeps it).
func TestOmitZeroByteIdenticalToStdlib(t *testing.T) {
	cases := map[string]OmitZeroCases{
		"all zero": {},
		"all set": {
			When:     time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
			Amount:   decimal.RequireFromString("12.5"),
			ID:       uuid.New(),
			Count:    7,
			Name:     "x",
			Ptr:      ptrTo(3),
			NilSlice: nil,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(&c)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.AppendZJSON(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
			}

			var wantRT, gotRT OmitZeroCases
			if err := json.Unmarshal(want, &wantRT); err != nil {
				t.Fatal(err)
			}
			if err := gotRT.UnmarshalZJSON(got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(&wantRT, &gotRT) {
				t.Fatalf("round-trip mismatch\nstdlib:   %+v\nzerojson: %+v", &wantRT, &gotRT)
			}
		})
	}

	// Encode-only: the non-nil-empty-slice divergence between omitzero
	// and omitempty (see TestOmitZeroEmptyVsZeroSlice for the assertion).
	withEmptySlice := OmitZeroCases{EmptySlice: []string{}}
	want, err := json.Marshal(&withEmptySlice)
	if err != nil {
		t.Fatal(err)
	}
	got, err := withEmptySlice.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
	}
}

// TestOmitZeroEmptyVsZeroSlice pins the divergence directly: omitempty
// drops a non-nil empty slice, omitzero does not (its zero value is nil,
// not len==0) — the one behavior an omitempty-only implementation could
// not represent, which is exactly what motivated adding omitzero.
func TestOmitZeroEmptyVsZeroSlice(t *testing.T) {
	c := OmitZeroCases{EmptySlice: []string{}}
	got, err := c.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"empty_slice":[]`)) {
		t.Fatalf("expected empty_slice to be emitted as [] (omitzero keeps non-nil-but-empty), got %s", got)
	}
}

// TestStringOptByteIdenticalToStdlib pins the ",string" tag option
// (implemented for int/uint fields) against encoding/json, which
// implements the identical quoted-number convention.
func TestStringOptByteIdenticalToStdlib(t *testing.T) {
	cases := []StringOptCases{
		{Count: 0, Amount: 0},
		{Count: 4294967295, Amount: -123456789},
	}
	for _, c := range cases {
		want, err := json.Marshal(&c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("mismatch\nstdlib:   %s\nzerojson: %s", want, got)
		}

		var wantRT, gotRT StringOptCases
		if err := json.Unmarshal(want, &wantRT); err != nil {
			t.Fatal(err)
		}
		if err := gotRT.UnmarshalZJSON(got); err != nil {
			t.Fatal(err)
		}
		if wantRT != gotRT {
			t.Fatalf("round-trip mismatch: stdlib %+v zerojson %+v", wantRT, gotRT)
		}

		// Bare numbers are invalid when json:",string" is present, matching
		// encoding/json rather than the ordinary bare-only numeric readers.
		bare := []byte(`{"count":0,"amount":0}`)
		var stdBare, viaBare StringOptCases
		stdErr := json.Unmarshal(bare, &stdBare)
		zjErr := viaBare.UnmarshalZJSON(bare)
		if stdErr == nil || zjErr == nil {
			t.Fatalf("bare ,string values must fail: std=%v zerojson=%v", stdErr, zjErr)
		}
	}
}

func ptrTo[T any](v T) *T { return &v }
