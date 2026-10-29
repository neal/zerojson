package codec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neal/zerojson"
)

// decodeFallbackOnly decodes data via zjDecodeFallback_Event directly,
// bypassing the public UnmarshalZJSON entry point (and therefore the
// speculative ordered fast path entirely). Comparing this against the
// public path is the differential check for the ordered decoder: by
// construction, both must produce identical results for every input.
//
// It calls Lexer.Finish itself, mirroring exactly what the public
// entry point's wrapper does (see decode.go's unmarshalZJSON) — Finish
// lives in the wrapper, deliberately not in zjDecodeFallback_<T>/
// zjDecodeOrdered_<T> themselves (a nested/recursive decode must not
// trailing-check), so a bare zjDecodeFallback_Event call has no opinion
// on trailing data at all. Without this, "{}0" would (correctly)
// diverge: the public path rejects the trailing "0", a bare
// zjDecodeFallback_Event call ignores it — not a decode bug, just this
// helper not applying the same entry-point contract the comparison
// needs to be meaningful.
func decodeFallbackOnly(data []byte, borrow bool) (Event, error) {
	l := zerojson.NewLexer(data, borrow)
	var v Event
	// Mirror the entry-point wrapper's top-level null handling too: null is
	// a stdlib-style no-op success, checked before the object decode, so the
	// raw fallback function never sees a top-level null. IsNull consumes the
	// token when it matches (the generated zjDecode_<T> relies on the same),
	// so Finish alone follows it — no extra Skip.
	if l.IsNull() {
		l.Finish()
		return v, l.Err()
	}
	a := &zjArenaEvent{}
	zjDecodeFallback_Event(&l, &v, a)
	l.Finish()
	return v, l.Err()
}

// FuzzOrderedVsFallback is the differential fuzzer required for the
// ordered decode feature: it decodes fuzzer input via the public path
// (ordered decode, falling back on any deviation) and via the fallback
// decoder directly, and checks the results agree exactly — both on
// success (reflect.DeepEqual) and on failure (both err or neither).
func FuzzOrderedVsFallback(f *testing.F) {
	for _, tr := range variants() {
		b, _ := json.Marshal(tr)
		f.Add(b)
	}
	// Seeds that exercise the ordered matcher's trap conditions directly
	// (out-of-order, duplicate, escaped, unknown-interleaved, whitespace,
	// null mid-sequence, nested bail) in addition to whatever the fuzzer
	// discovers on its own.
	for _, seed := range orderedTrapSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if hugeExponent(data) {
			return
		}
		wantV, wantErr := decodeFallbackOnly(data, false)
		var gotV Event
		gotErr := gotV.UnmarshalZJSON(data)
		if (wantErr != nil) != (gotErr != nil) {
			t.Fatalf("error-agreement mismatch\ninput: %q\nfallback-only err: %v\npublic (ordered+fallback) err: %v", data, wantErr, gotErr)
		}
		if wantErr == nil && !reflect.DeepEqual(&wantV, &gotV) {
			t.Fatalf("decode divergence between fallback-only and public path\ninput: %q\nfallback-only: %+v\npublic:        %+v", data, &wantV, &gotV)
		}
	})
}

// orderedTrapSeeds are hand-picked documents that exercise each of the
// ordered matcher's documented trap conditions. TestOrderedTraps checks
// each against encoding/json directly (proving end-to-end correctness,
// not just internal agreement); they're also used to seed the fuzzer
// above so mutations continue to explore near them.
var orderedTrapSeeds = []string{
	`{"active":true,"origin":"acme"}`,                                       // out-of-order
	`{"origin":"first","origin":"second"}`,                                  // duplicate key, last wins
	`{"i\u0064":"11111111-1111-1111-1111-111111111111"}`,                    // escaped key that decodes to "id"
	`{"unknown1":123,"origin":"acme","unknown2":[1,2,3],"active":true}`,     // unknown keys interleaved (projection)
	`{"origin": "acme", "active":true}`,                                     // whitespace after colon
	`{ "origin":"acme","active":true}`,                                      // whitespace after brace
	`{"origin":"acme","active":true}`,                                       // omitempty subsequence (skips 1..33)
	`{"origin":"acme","location":null,"active":true}`,                       // null mid-sequence
	`{"origin":"acme","meta":{"internal":true,"cost":"1.5"},"active":true}`, // nested out-of-order -> bail propagates
	`{ }`,  // whitespace-padded empty object: must bail (IsDelim guard), not ReadKey the brace
	`{  }`, // same, wider padding — with ordered errors now final, a missing guard would reject valid input
}

// TestOrderedTraps checks each documented ordered-matcher trap against
// encoding/json directly, so correctness is verified end-to-end
// regardless of which internal path (ordered fast path or fallback)
// zerojson actually took.
func TestOrderedTraps(t *testing.T) {
	for _, data := range orderedTrapSeeds {
		t.Run(data, func(t *testing.T) {
			var want Event
			if err := json.Unmarshal([]byte(data), &want); err != nil {
				t.Fatalf("stdlib rejected seed: %v", err)
			}
			var got Event
			if err := got.UnmarshalZJSON([]byte(data)); err != nil {
				t.Fatalf("zerojson rejected seed: %v", err)
			}
			if !reflect.DeepEqual(&want, &got) {
				t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
			}
		})
	}
}

// TestOrderedNullMidSequenceClearsPointer specifically checks that a null
// appearing mid-sequence on the ordered fast path clears an
// already-populated pointer field, exactly as encoding/json does on a
// reused target — the null-mid-sequence trap, but starting from non-zero
// state (the zero-value case can't distinguish "cleared" from "never
// set").
func TestOrderedNullMidSequenceClearsPointer(t *testing.T) {
	base := []byte(`{"origin":"acme1","location":"ZONE1","active":true}`)
	patch := []byte(`{"origin":"acme2","location":null,"active":false}`)

	var want, got Event
	if err := json.Unmarshal(base, &want); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalZJSON(base); err != nil {
		t.Fatal(err)
	}
	if want.Location == nil || got.Location == nil {
		t.Fatalf("setup failed: want.Location=%v got.Location=%v", want.Location, got.Location)
	}

	if err := json.Unmarshal(patch, &want); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalZJSON(patch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &got) {
		t.Fatalf("mismatch after null patch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
	}
	if got.Location != nil {
		t.Fatalf("Location not cleared by mid-sequence null: %q", *got.Location)
	}
}

// TestOrderedNestedBailPropagates directly checks the nested out-of-order
// trap: EventMeta's own ordered decode meets an out-of-order key and hands
// the rest of its object to its keyed member loop, and Event must still end
// up exactly as stdlib and the fallback-only path decode it (no half-written
// fields or arena state from the ordered prefix).
func TestOrderedNestedBailPropagates(t *testing.T) {
	data := []byte(`{"origin":"acme","meta":{"internal":true,"cost":"1.5","note":"n"},"active":true}`)
	var want Event
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := got.UnmarshalZJSON(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, &got) {
		t.Fatalf("mismatch\nstdlib:   %+v\nzerojson: %+v", &want, &got)
	}
	// Cross-check against the fallback-only path too: the whole point of
	// the ordered decoder is that both must agree, always.
	viaFallback, err := decodeFallbackOnly(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&viaFallback, &got) {
		t.Fatalf("public path diverged from fallback-only\nfallback-only: %+v\npublic:        %+v", &viaFallback, &got)
	}
}

// orderedHandoffSeeds deviate only after the ordered decoder has matched
// some members, so the keyed member loop takes over mid-object.
var orderedHandoffSeeds = []string{
	`{"origin":"acme","active":true,"origin":"beta"}`, // duplicate after a full ordered prefix
	`{"origin":"acme","unknown":[1,{"x":2}],"active":true,"origin":"beta"}`,
	`{"active":true,"origin":"acme"}`,            // out of order at the second member
	`{"origin":"acme" ,"active":true}`,           // whitespace before a separator
	`{"origin":"acme","active":true }`,           // whitespace before the closing brace
	`{"origin":"acme","ACTIVE":true}`,            // case-folded declared key
	`{"origin":"acme","active":true,"origin":7}`, // error in the keyed remainder
	`{"origin":"acme","active":true,"origin":"b","active":false}`,
	`{"active":true,"ORIGIN":"x"}`,         // case-folded declared key behind the cursor
	`{"origin":"acme", "origin":"beta"}`,   // whitespace before a declared key: ReadKey path
	`{"origin":"acme","\u0061ctive":true}`, // escaped declared key: ReadKey path
}

// TestOrderedHandoffMatchesStdlib checks every handoff seed against
// encoding/json and the fallback-only path, in both copy and borrow mode:
// the ordered prefix plus the keyed remainder must equal a keyed decode of
// the whole object, including which inputs fail.
func TestOrderedHandoffMatchesStdlib(t *testing.T) {
	for _, data := range orderedHandoffSeeds {
		var want EventProjection
		wantErr := json.Unmarshal([]byte(data), &want)
		for _, borrow := range []bool{false, true} {
			var got EventProjection
			var gotErr error
			if borrow {
				gotErr = got.UnmarshalZJSONBorrow([]byte(data))
			} else {
				gotErr = got.UnmarshalZJSON([]byte(data))
			}
			l := zerojson.NewLexer([]byte(data), borrow)
			var fallback EventProjection
			zjDecodeFallback_EventProjection(&l, &fallback, &zjArenaEventProjection{})
			l.Finish()
			fallbackErr := l.Err()
			if (wantErr != nil) != (gotErr != nil) || (gotErr != nil) != (fallbackErr != nil) {
				t.Fatalf("%s borrow=%v: stdlib err=%v, zerojson err=%v, fallback-only err=%v", data, borrow, wantErr, gotErr, fallbackErr)
			}
			if gotErr != nil {
				if gotErr.Error() != fallbackErr.Error() {
					t.Errorf("%s borrow=%v: error %q, fallback-only %q", data, borrow, gotErr, fallbackErr)
				}
				continue
			}
			if got != want || got != fallback {
				t.Errorf("%s borrow=%v: zerojson %+v, stdlib %+v, fallback-only %+v", data, borrow, got, want, fallback)
			}
		}
	}
	// Nested: EventMeta hands off inside Event's keyed decode, then Event
	// carries on with the members after it.
	for _, data := range []string{
		`{"meta":{"note":"n","internal":true,"cost":"1.5"},"origin":"acme"}`,
		`{"meta":{"note":"n","note":"m"},"meta_ptr":{"cost":"2","note":"p"},"active":true}`,
		`{"meta":{"note":"n" ,"internal":true},"origin":"acme"}`,
	} {
		var want Event
		if err := json.Unmarshal([]byte(data), &want); err != nil {
			t.Fatal(err)
		}
		var got Event
		if err := got.UnmarshalZJSON([]byte(data)); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		viaFallback, err := decodeFallbackOnly([]byte(data), false)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(&want, &got) || !reflect.DeepEqual(&viaFallback, &got) {
			t.Errorf("%s:\nstdlib:        %+v\nzerojson:      %+v\nfallback-only: %+v", data, &want, &got, &viaFallback)
		}
	}
}

// TestOrderedHandoffDecodesPrefixOnce pins that a deviation after the
// ordered prefix does not restart the object: in copy mode each decoded
// string costs one allocation, so a restart would copy "acme" twice.
func TestOrderedHandoffDecodesPrefixOnce(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	for _, doc := range []string{
		`{"origin":"acme","active":true,"origin":"beta"}`,
		`{"origin":"acme","active":true, "origin":"beta"}`, // not a plain key: ReadKey path
	} {
		data := []byte(doc)
		var v EventProjection
		got := testing.AllocsPerRun(500, func() {
			v = EventProjection{}
			if err := v.UnmarshalZJSON(data); err != nil {
				t.Fatal(err)
			}
		})
		if got != 2 {
			t.Fatalf("%s: EventProjection.UnmarshalZJSON allocations=%v, want 2 (one copy per decoded origin)", doc, got)
		}
	}
}

// orderedKeyFuncs are one type's generated ordered-key helpers.
type orderedKeyFuncs struct {
	name     string
	names    []string
	try      func(*zerojson.Lexer, int) bool
	seek     func(*zerojson.Lexer, int) int
	declared func(string) bool
}

func orderedKeyTypes() []orderedKeyFuncs {
	return []orderedKeyFuncs{
		{"OrderedLookup", zjFieldNamesOrderedLookup[:], zjTryOrderedKeyOrderedLookup, zjSeekOrderedKeyOrderedLookup, zjIsDeclaredKeyOrderedLookup},
		{"NumberCases", zjFieldNamesNumberCases[:], zjTryOrderedKeyNumberCases, zjSeekOrderedKeyNumberCases, zjIsDeclaredKeyNumberCases},
		{"OmitZeroCases", zjFieldNamesOmitZeroCases[:], zjTryOrderedKeyOmitZeroCases, zjSeekOrderedKeyOmitZeroCases, zjIsDeclaredKeyOmitZeroCases},
		{"EventProjection", zjFieldNamesEventProjection[:], zjTryOrderedKeyEventProjection, zjSeekOrderedKeyEventProjection, zjIsDeclaredKeyEventProjection},
		{"IgnoredOnly", zjFieldNamesIgnoredOnly[:], zjTryOrderedKeyIgnoredOnly, zjSeekOrderedKeyIgnoredOnly, zjIsDeclaredKeyIgnoredOnly},
	}
}

// seekReference is what zjSeekOrderedKey<T> stands in for: the literal
// tries from from on, then ReadKey and zjIsDeclaredKey<T> for a key that
// reads back as the bytes it is written with (no whitespace, escape, or
// non-ASCII byte).
func (f orderedKeyFuncs) seekReference(data []byte, pos, from int) (int, int) {
	l := zerojson.NewLexer(data, true)
	for k := from; k < len(f.names); k++ {
		l.SetPos(pos)
		if f.try(&l, k) {
			return k, l.Pos()
		}
	}
	l.SetPos(pos)
	key := l.ReadKey()
	n := len(key)
	if l.Err() != nil || l.Pos() != pos+n+3 || data[pos] != '"' || string(data[pos+1:pos+1+n]) != string(key) || data[pos+1+n] != '"' {
		return -1, pos
	}
	for _, c := range key {
		if c >= utf8.RuneSelf {
			return -1, pos
		}
	}
	if f.declared(string(key)) {
		return -2, pos
	}
	return -3, pos + n + 3
}

func (f orderedKeyFuncs) checkSeek(t *testing.T, data []byte, pos, from int) {
	t.Helper()
	l := zerojson.NewLexer(data, true)
	l.SetPos(pos)
	got := f.seek(&l, from)
	want, wantPos := f.seekReference(data, pos, from)
	if got != want || l.Pos() != wantPos || l.Err() != nil {
		t.Fatalf("zjSeekOrderedKey%s(%q at %d, from %d) = %d at %d (err %v); want %d at %d", f.name, data, pos, from, got, l.Pos(), l.Err(), want, wantPos)
	}
}

// orderedKeyDocs builds member prefixes around each declared name: the
// name itself, case and length variants, and the non-plain spellings
// (whitespace, escapes, non-ASCII) that must be left to ReadKey.
func orderedKeyDocs(names []string) []string {
	keys := []string{"", "unknown", strings.Repeat("u", 40)}
	for _, n := range names {
		keys = append(keys, n, strings.ToUpper(n), strings.ToUpper(n[:1])+n[1:], n+"x", n[:len(n)-1])
	}
	var docs []string
	for _, k := range keys {
		docs = append(docs, `"`+k+`":1`, `"`+k+`" :1`, `"`+k+`"`, ` "`+k+`":1`)
		if k != "" {
			docs = append(docs, fmt.Sprintf(`"\u%04x%s":1`, k[0], k[1:])) // first byte escaped
		}
	}
	return append(docs, `"\u006e":1`, "\"n\xc3\xa9\":1", "\"\xe2\x84\xaa\":1", `"n\"":1`, `}`, ``, `"n`, "\"\x01\":1")
}

func TestSeekOrderedKeyMatchesReference(t *testing.T) {
	for _, f := range orderedKeyTypes() {
		for _, doc := range orderedKeyDocs(f.names) {
			for from := range len(f.names) + 2 {
				f.checkSeek(t, []byte(doc), 0, from)
				f.checkSeek(t, []byte(","+doc), 1, from)
			}
		}
	}
}

func FuzzSeekOrderedKey(f *testing.F) {
	funcs := orderedKeyTypes()[0]
	for _, doc := range orderedKeyDocs(funcs.names) {
		f.Add([]byte(doc), uint8(0), uint8(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, pos, from uint8) {
		funcs.checkSeek(t, data, min(int(pos), len(data)), int(from)%(len(funcs.names)+2))
	})
}

func TestOrderedLookupDecodeMatchesStdlib(t *testing.T) {
	var d OrderedLookupDecoder
	checkFoldDecodes[OrderedLookup](t, []string{
		`{"n":1,"no":2,"nos":3,"ID":4,"id":5,"-":6,"x-y":7,"a_declared_name_longer_than_thirty_two":8,"last":"z"}`,
		`{"last":"z","n":1}`, // looked up at the end, then a declared name behind it
		`{"id":5,"ID":4}`,
		`{"Id":9}`, `{"iD":9,"id":1}`, `{"ID":1,"Id":2}`, // "Id" folds onto ID, the first field
		`{"NOS":3,"nos":4}`,
		`{"nosy":1,"no":2,"n":3}`,
		`{"x":1,"n":2,"an_unknown_key_longer_than_thirty_two_bytes":3,"last":"q"}`,
		`{"A_DECLARED_NAME_LONGER_THAN_THIRTY_TWO":8}`,
		`{"n" :1,"no":2}`, `{"n":1, "no":2,"n":3}`, `{"\u006eo":2,"n":1}`,
		"{\"n\xc3\xa9\":1,\"no\":2}",
		`{"-":1,"x-y":2,"-":3}`,
		`{"n":1,"no":"x"}`, `{"last":"z","n":"x"}`,
	}, d.Decode)
	var ignored IgnoredOnlyDecoder
	checkFoldDecodes[IgnoredOnly](t, []string{`{}`, `{"Hidden":1}`, `{"-":1,"x":[1,{"y":2}]}`, `{ "a":1}`, `{"a":1, "b":2}`, `{"a":x}`}, ignored.Decode)
}
