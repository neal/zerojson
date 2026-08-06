package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// TestEmbedOrderDivergenceFromEasyjson pins a genuine oracle-vs-oracle
// disagreement: for an untagged embedded struct, encoding/json promotes
// the embed's fields at the embed's declaration position, but easyjson
// emits them AFTER all of the struct's direct fields, regardless of where
// the embed is declared.
//
// zerojson deliberately sides with encoding/json here (the generator's
// flattenEmbedded promotes fields in declaration order, exactly like
// stdlib) — a documented divergence from easyjson, the one place where
// the encode contract's byte-identity-with-easyjson precedence is
// knowingly not honored: easyjson's ordering is the outlier behavior
// (json/v2 also uses declaration position), no real production easyjson
// type uses an untagged mid-struct embed, and
// a migrating type that DOES need easyjson-identical bytes has a
// trivial workaround — declare the embed last, where the two
// conventions coincide (exactly what codec's own Event does; see
// types.go).
func TestEmbedOrderDivergenceFromEasyjson(t *testing.T) {
	v := EmbedMid{
		First: "f",
		Audit: Audit{CreatedBy: "cb", Version: 3},
		Last:  "l",
	}
	ej := EmbedMidEJ(v)

	wantStd, err := json.Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	wantEJ, err := ej.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	gotZJ, err := v.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Pin the exact orders, not just inequality, so a change in either
	// oracle's behavior (or zerojson's) is loud and specific.
	const stdOrder = `{"first":"f","created_by":"cb","version":3,"last":"l"}`
	const ejOrder = `{"first":"f","last":"l","created_by":"cb","version":3}`
	if string(wantStd) != stdOrder {
		t.Fatalf("stdlib order changed: %s (want %s)", wantStd, stdOrder)
	}
	if string(wantEJ) != ejOrder {
		t.Fatalf("easyjson order changed: %s (want %s) — if easyjson now matches stdlib, this divergence is obsolete: update the README and this test", wantEJ, ejOrder)
	}
	if !bytes.Equal(wantStd, gotZJ) {
		t.Fatalf("zerojson diverged from stdlib on embed position\nstdlib:   %s\nzerojson: %s", wantStd, gotZJ)
	}

	// Ordering aside, all three decode each other's output to the same
	// value — the divergence is byte-level only, never semantic.
	for name, data := range map[string][]byte{"stdlib": wantStd, "easyjson": wantEJ, "zerojson": gotZJ} {
		var viaStd EmbedMid
		if err := json.Unmarshal(data, &viaStd); err != nil {
			t.Fatalf("stdlib rejected %s output: %v", name, err)
		}
		var viaZJ EmbedMid
		if err := viaZJ.UnmarshalZJSON(data); err != nil {
			t.Fatalf("zerojson rejected %s output: %v", name, err)
		}
		if !reflect.DeepEqual(&v, &viaStd) || !reflect.DeepEqual(&v, &viaZJ) {
			t.Fatalf("%s output did not round-trip: stdlib=%+v zerojson=%+v want=%+v", name, &viaStd, &viaZJ, &v)
		}
	}
}
