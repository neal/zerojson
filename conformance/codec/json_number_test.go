package codec

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neal/zerojson/conformance/internal/oracle"
)

// TestJSONNumberMatchesStdlib pins encoding/json.Number fields against
// classic encoding/json: the literal is written bare and preserved
// verbatim on decode (no float round trip), a quoted numeric string is
// accepted, non-numbers are rejected on both sides, and an invalid Number
// value refuses to encode.
func TestJSONNumberMatchesStdlib(t *testing.T) {
	n := json.Number("2.50")
	for _, v := range []NumberCases{
		{},
		{N: "7", NO: "0", NP: &n, NS: []json.Number{"1", "-2e3", "123456789012345678901234567890"}, NM: map[string]json.Number{"k": "0.1"}, CntS: 4},
	} {
		zj, err := v.AppendZJSON(nil)
		if err != nil {
			t.Fatal(err)
		}
		std, _ := json.Marshal(v)
		var a, b map[string]any
		if json.Unmarshal(zj, &a) != nil || json.Unmarshal(std, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("encode mismatch\n zj: %s\nstd: %s", zj, std)
		}
	}
	if _, err := (&NumberCases{N: "abc"}).AppendZJSON(nil); err == nil {
		t.Fatal("invalid Number encoded without error")
	}
	for _, doc := range []string{
		`{"n":12}`, `{"n":"12"}`, `{"n":-1.5e10}`, `{"n":123456789012345678901234567890}`,
		`{"n":"abc"}`, `{"n":true}`, `{"n":01}`, `{"n":null}`,
		`{"np":null}`, `{"np":3}`, `{"ns":[1,"2",null]}`, `{"nm":{"a":1,"b":"2"}}`,
		`{"cnt_s":"null"}`, `{"cnt_s":"5"}`, `{"cnt_s":"+5"}`, `{"cnt_s":null}`,
		`{"cnt_s":"n\u0075ll"}`, `{"cnt_s":"\u006eull"}`, `{"cnt_s":"n\u0075lx"}`,
		`{"ptr_s":"null"}`, `{"ptr_s":"7"}`, `{"ptr_s":"\u006eull"}`,
	} {
		seven := int64(7)
		zjT := NumberCases{CntS: 9, PtrS: &seven}
		stdT := NumberCases{CntS: 9, PtrS: &seven}
		zjErr := zjT.UnmarshalZJSON([]byte(doc))
		stdErr := json.Unmarshal([]byte(doc), &stdT)
		if doc == `{"ptr_s":"\u006eull"}` && oracle.StdlibJSONV2 {
			// Go 1.27's v1-on-v2 layer leaves a ,string POINTER field
			// untouched for the ESCAPED spelling of "null" (it clears it
			// for the plain spelling); classic v1 clears it for both, and
			// so does zerojson. Documented under "Which encoding/json?".
			continue
		}
		if (zjErr == nil) != (stdErr == nil) {
			// The one documented direction: a leading '+' inside a
			// ,string value is accepted by Go 1.27's v2-backed
			// encoding/json only.
			if doc == `{"cnt_s":"+5"}` && zjErr != nil && stdErr == nil {
				continue
			}
			t.Fatalf("%s: zerojson err=%v stdlib err=%v", doc, zjErr, stdErr)
		}
		if zjErr == nil && !reflect.DeepEqual(zjT, stdT) {
			t.Fatalf("%s: decode mismatch\n zj: %+v\nstd: %+v", doc, zjT, stdT)
		}
	}
}
