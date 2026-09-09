package codec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestCallbacksNotInvokedOnMalformedInput pins that a delegated
// UnmarshalJSON is never handed an empty or partial span: when the value
// it would receive fails the grammar walk (RawValue/Skip), the decode
// error stands and the user callback — with whatever side effects it has
// — does not run on the attacker-controlled malformed input.
func TestCallbacksNotInvokedOnMalformedInput(t *testing.T) {
	for _, doc := range []string{
		`{"first":tru}`, `{"first":}`, `{"first":[1,}`, `{"first":"unterminated`,
		`{"first":{"a":1`, `{"first":1.}`, `{"first":-}`,
	} {
		before := sideEffectCalls
		var v OrderedSideEffects
		if err := v.UnmarshalZJSON([]byte(doc)); err == nil {
			t.Errorf("%s: decoded without error", doc)
		}
		if sideEffectCalls != before {
			t.Errorf("%s: UnmarshalJSON callback ran %d time(s) on malformed input", doc, sideEffectCalls-before)
		}
	}
	// A valid prefix must not invoke a callback in a malformed document.
	{
		before := sideEffectCalls
		var v OrderedSideEffects
		if err := v.UnmarshalZJSON([]byte(`{"first":01}`)); err == nil {
			t.Error(`{"first":01}: decoded without error`)
		}
		if sideEffectCalls != before {
			t.Errorf(`{"first":01}: callback ran %d time(s), want 0`, sideEffectCalls-before)
		}
	}
	// And it still runs exactly once on a well-formed value.
	before := sideEffectCalls
	var v OrderedSideEffects
	if err := v.UnmarshalZJSON([]byte(`{"first":7,"last":1}`)); err != nil {
		t.Fatal(err)
	}
	if sideEffectCalls != before+1 {
		t.Fatalf("callback ran %d time(s) on well-formed input, want 1", sideEffectCalls-before)
	}
}

// TestNullDeliveredToValueUnmarshaler pins that a JSON null reaches a
// value-typed json.Unmarshaler (field, slice element, map value) exactly
// as in encoding/json, so a nullable custom type observes it.
func TestNullDeliveredToValueUnmarshaler(t *testing.T) {
	docs := []string{
		`{"one":null}`, `{"one":5}`, `{"many":[null,3]}`, `{"index":{"a":null,"b":7}}`, `{"ptr":null}`,
	}
	for _, doc := range docs {
		zj := NullCases{One: NullAware{Valid: true, N: 1}}
		std := NullCases{One: NullAware{Valid: true, N: 1}}
		zjErr := zj.UnmarshalZJSON([]byte(doc))
		stdErr := json.Unmarshal([]byte(doc), &std)
		if (zjErr == nil) != (stdErr == nil) || !reflect.DeepEqual(zj, std) {
			t.Fatalf("%s:\n zj: %+v err=%v\nstd: %+v err=%v", doc, zj, zjErr, std, stdErr)
		}
	}
}
