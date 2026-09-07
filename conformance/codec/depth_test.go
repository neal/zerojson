package codec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/neal/zerojson"
)

// TestDecodeDepthLimitMatchesValidEverywhere pins the decode-implies-Valid
// contract at the nesting limit through the GENERATED decoders: an
// unknown-field subtree (skipped inline by the ordered decoder, or by the
// fallback's Skip), an `any` field (ReadInterface), and a Raw field
// (RawValue) must each accept and reject exactly the depths Valid and
// encoding/json do, wherever the deep part sits. Regression for the
// off-by-one where a skip started counting from zero instead of the
// enclosing object's depth.
func TestDecodeDepthLimitMatchesValidEverywhere(t *testing.T) {
	const limit = 10000
	for _, n := range []int{limit - 1, limit, limit + 1} {
		deep := strings.Repeat("[", n) + strings.Repeat("]", n)
		for _, field := range []string{"zzz", "extra", "payload"} {
			doc := []byte(`{"` + field + `":` + deep + `}`)
			want := zerojson.Valid(doc) == nil
			var stdTarget map[string]any
			if stdOK := json.Unmarshal(doc, &stdTarget) == nil; stdOK != want {
				t.Fatalf("oracle disagreement at n=%d: stdlib ok=%v Valid ok=%v", n, stdOK, want)
			}
			var ev Event
			if got := ev.UnmarshalZJSON(doc) == nil; got != want {
				t.Errorf("Event (field %q, %d arrays): decode ok=%v want %v", field, n, got, want)
			}
			var raw RawHolder
			if got := raw.UnmarshalZJSON(doc) == nil; got != want {
				t.Errorf("RawHolder (field %q, %d arrays): decode ok=%v want %v", field, n, got, want)
			}
		}
	}
}

// TestDecodeDepthLimitCountsEmptyContainersAndAnySiblings pins two more
// positions of the limit through the generated decoders: an unknown-field
// subtree whose innermost container is EMPTY (which still occupies a
// level), and a deep subtree that follows an `any`-typed sibling (whose
// decode must leave the depth counter balanced).
func TestDecodeDepthLimitCountsEmptyContainersAndAnySiblings(t *testing.T) {
	const limit = 10000
	for _, n := range []int{limit - 2, limit - 1, limit} {
		deep := strings.Repeat("[", n) + strings.Repeat("]", n)
		for _, doc := range []string{
			`{"zzz":` + deep + `}`,                            // innermost is [] — object + n arrays
			`{"extra":{"a":[[1]],"b":{}},"zzz":` + deep + `}`, // after an any-typed sibling
			`{"extra":[[[]]],"zzz":` + deep + `}`,
		} {
			want := zerojson.Valid([]byte(doc)) == nil
			var ev Event
			if got := ev.UnmarshalZJSON([]byte(doc)) == nil; got != want {
				t.Errorf("Event (%d arrays): decode ok=%v want %v for %.40s...", n, got, want, doc)
			}
			var stdTarget map[string]any
			if stdOK := json.Unmarshal([]byte(doc), &stdTarget) == nil; stdOK != want {
				t.Fatalf("oracle disagreement (%d arrays): stdlib ok=%v Valid ok=%v", n, stdOK, want)
			}
		}
	}
}
