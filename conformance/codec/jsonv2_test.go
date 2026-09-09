package codec

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
)

// jsonv2_test.go covers encoding/json/v2 as a third decode oracle,
// alongside easyjson (encode's byte-identity target) and
// encoding/json v1 (decode's primary oracle per this project's
// contract — see README's "Compatibility contract"). It uses the
// github.com/go-json-experiment/json reference module rather than the
// real stdlib encoding/json/v2 package: the stdlib package only exists
// when built with GOEXPERIMENT=jsonv2, which would tie every `go test`/`go
// build` invocation across this module to a non-default experimental flag;
// go-json-experiment/json is the actual reference implementation the
// stdlib package is staged from (same semantics, same jsontext/json
// split), importable as an ordinary module dependency instead. See
// conformance/README.md for the summarized v1-vs-v2 divergence table
// this file's tests pin empirically.
//
// Three genuine v1-vs-v2 behavioral divergences this file pins:
//
//  1. Key matching case-sensitivity: v1 falls back to a case-insensitive
//     match when no exact key match exists; v2 matches case-sensitively
//     only, with no case-insensitive fallback (an opt-in
//     MatchCaseInsensitiveNames option restores v1's behavior).
//     zerojson follows v1's exact-first, case-insensitive fallback.
//  2. Duplicate object member names: v1 resolves them last-occurrence-
//     wins; v2 rejects the whole decode with a "duplicate object member
//     name" error by default. zerojson's documented choice (see
//     ordered_test.go's orderedTrapSeeds: "duplicate key, last wins")
//     sides with v1 here.
//  3. time.Duration representation: v1 has no special handling for
//     time.Duration at all — it's just a named int64, so a bare JSON
//     number decodes into it like any other integer. v2 special-cases
//     time.Duration and, by design, HAS NO DEFAULT REPRESENTATION for
//     it at all (still an open question upstream — see
//     https://go.dev/issue/71631, cited verbatim in the "no default
//     representation" error text this test pins) unless the caller
//     opts into one explicitly (a `format` struct tag, or — as used
//     below — a WithUnmarshalers override). zerojson matches v1 (a bare
//     number, no special-casing): Event's Timeout field, being a
//     time.Duration, needs this override to compare against v2 at all,
//     which is why durationAsNanoV2Option exists below.
//
// Generic number handling agrees between the two beyond error-path
// cosmetics: both reject the same malformed numeric literals, and both
// decode a JSON number into an `any` field as float64 with the same
// precision loss for values beyond 2^53 (integer overflow, negative-into-
// uint64, leading zero/plus/hex digits, bare NaN/Infinity literals, and
// large exponents all produce the same accept/reject outcome). The one
// cosmetic difference (v2 leaves a float64 field holding +Inf on an
// out-of-range overflow error where v1 leaves it at the zero value) is an
// error-path detail neither oracle's success contract depends on and isn't
// pinned here.

// durationAsNanoV2 gives v2's Unmarshal a v1-matching (and
// zerojson-matching) representation for time.Duration — a bare JSON
// number of nanoseconds — so Event (which has a Timeout time.Duration
// field) can be decoded via v2 at all; v2 has no default representation
// for the type otherwise (divergence 3 above).
var durationAsNanoV2 = jsonv2.WithUnmarshalers(jsonv2.UnmarshalFunc(func(b []byte, d *time.Duration) error {
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return err
	}
	*d = time.Duration(n)
	return nil
}))

// FuzzDecodeAgainstJSONV2 extends FuzzDecodeAgainstStdlib's
// canonicalization (round-trip fuzzer input through v1 first, so raw
// bytes with case variation or duplicate keys — ambiguous by
// construction, per divergences 1 and 2 above — never reach the
// comparison) with a third decode via v2 (given divergence 3's
// override, so Timeout doesn't trip every single input), requiring all
// three decoders agree on the canonical form.
func FuzzDecodeAgainstJSONV2(f *testing.F) {
	for _, tr := range variants() {
		b, _ := json.Marshal(tr)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if hugeExponent(data) {
			return
		}
		var seed Event
		if err := json.Unmarshal(data, &seed); err != nil {
			return
		}
		canonical, err := json.Marshal(&seed)
		if err != nil {
			return
		}

		var wantV1 Event
		if err := json.Unmarshal(canonical, &wantV1); err != nil {
			return
		}
		var wantV2 Event
		if err := jsonv2.Unmarshal(canonical, &wantV2, durationAsNanoV2); err != nil {
			t.Fatalf("v2 rejected v1-canonical input %q: %v", canonical, err)
		}
		if !reflect.DeepEqual(&wantV1, &wantV2) {
			t.Fatalf("v1/v2 disagree on a v1-canonical input (unexpected: canonicalizing through v1 first removes the known ambiguities)\ninput: %q\nv1: %+v\nv2: %+v", canonical, &wantV1, &wantV2)
		}

		var got Event
		if err := got.UnmarshalZJSON(canonical); err != nil {
			t.Fatalf("zerojson rejected canonical input %q: %v", canonical, err)
		}
		if !reflect.DeepEqual(&wantV1, &got) {
			t.Fatalf("zerojson diverged from v1/v2 on canonical input\ninput: %q\nwant: %+v\ngot:  %+v", canonical, &wantV1, &got)
		}
	})
}

// TestKeyMatchingCaseSensitivityDivergesFromV1MatchesV2 pins divergence
// 1 directly against both live packages: a key that only case-
// insensitively matches a declared field name is bound by v1 and left
// unbound by v2 — zerojson matches v2.
func TestKeyMatchingCaseSensitivityMatchesV1(t *testing.T) {
	data := []byte(`{"ORIGIN":"acme","ACTIVE":true}`)

	var v1 Event
	if err := json.Unmarshal(data, &v1); err != nil {
		t.Fatal(err)
	}
	if v1.Origin != "acme" || !v1.Active {
		t.Fatalf("oracle assumption broken: v1 did not case-insensitively match: %+v", v1)
	}

	var v2 Event
	if err := jsonv2.Unmarshal(data, &v2, durationAsNanoV2); err != nil {
		t.Fatalf("oracle assumption broken: v2 errored on an unrecognized (case-mismatched) key: %v", err)
	}
	if v2.Origin != "" || v2.Active {
		t.Fatalf("oracle assumption broken: v2 unexpectedly case-insensitively matched: %+v", v2)
	}

	var got Event
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if got.Origin != v1.Origin || got.Active != v1.Active {
		t.Fatalf("zerojson case folding differs from stdlib: %+v", got)
	}
}

// TestDuplicateKeyLastWinsDivergesFromV2MatchesV1 pins divergence 2
// directly: v1 resolves a duplicate object key last-occurrence-wins; v2
// rejects the decode outright. zerojson matches v1.
func TestDuplicateKeyLastWinsDivergesFromV2MatchesV1(t *testing.T) {
	data := []byte(`{"origin":"first","origin":"second"}`)

	var v1 Event
	if err := json.Unmarshal(data, &v1); err != nil {
		t.Fatal(err)
	}
	if v1.Origin != "second" {
		t.Fatalf("oracle assumption broken: v1 did not take the last duplicate: %+v", v1)
	}

	var v2 Event
	if err := jsonv2.Unmarshal(data, &v2, durationAsNanoV2); err == nil {
		t.Fatalf("oracle assumption broken: v2 accepted a duplicate key without error: %+v", v2)
	}

	var got Event
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if got.Origin != "second" {
		t.Fatalf("zerojson diverged from its documented last-wins (v1-matching) duplicate-key choice: %+v", got)
	}
}

// TestDurationRepresentationDivergesFromV1RequiresExplicitFormatInV2
// pins divergence 3 directly: v2 refuses to decode a bare JSON number
// into a time.Duration field at all (by design — see the doc comment
// above), where v1 (and zerojson) treat it as a plain integer.
func TestDurationRepresentationDivergesFromV1RequiresExplicitFormatInV2(t *testing.T) {
	data := []byte(`{"timeout":1500000000}`)

	var v1 Event
	if err := json.Unmarshal(data, &v1); err != nil {
		t.Fatal(err)
	}
	if v1.Timeout != 1500*time.Millisecond {
		t.Fatalf("oracle assumption broken: v1 did not decode Timeout as a bare number: %v", v1.Timeout)
	}

	var v2Default Event
	if err := jsonv2.Unmarshal(data, &v2Default); err == nil {
		t.Fatalf("oracle assumption broken: v2 decoded a time.Duration field without an explicit format option: %+v", v2Default)
	}

	var v2WithOption Event
	if err := jsonv2.Unmarshal(data, &v2WithOption, durationAsNanoV2); err != nil {
		t.Fatalf("v2 with the nanoseconds override still rejected the input: %v", err)
	}
	if v2WithOption.Timeout != v1.Timeout {
		t.Fatalf("v2 with the nanoseconds override diverged from v1: v1=%v v2=%v", v1.Timeout, v2WithOption.Timeout)
	}

	var got Event
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if got.Timeout != v1.Timeout {
		t.Fatalf("zerojson diverged from v1's bare-number Timeout decode: v1=%v zerojson=%v", v1.Timeout, got.Timeout)
	}
}
