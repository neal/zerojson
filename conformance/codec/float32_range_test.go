package codec

import (
	"encoding/json"
	"testing"
)

// TestFloat32RangeMatchesStdlib pins that a bare number outside float32's
// range on a float32 field is a decode error, as encoding/json reports it,
// rather than a silent ±Inf that AppendZJSON would then refuse to
// re-encode (found by the final review of the fix branch).
func TestFloat32RangeMatchesStdlib(t *testing.T) {
	for _, doc := range []string{
		`{"ratio32":1e39}`, `{"ratio32":-1e39}`, `{"ratio32":3.4028236e38}`,
		`{"ratio32":3.4e38}`, `{"ratio32":1e-50}`, `{"ratio32":-0.5}`,
	} {
		var ev Event
		zjErr := ev.UnmarshalZJSON([]byte(doc))
		var std Event
		stdErr := json.Unmarshal([]byte(doc), &std)
		if (zjErr == nil) != (stdErr == nil) {
			t.Fatalf("%s: zerojson err=%v stdlib err=%v", doc, zjErr, stdErr)
		}
		if zjErr == nil && ev.Ratio32 != std.Ratio32 {
			t.Fatalf("%s: zerojson=%v stdlib=%v", doc, ev.Ratio32, std.Ratio32)
		}
		if zjErr == nil {
			if _, err := ev.AppendZJSON(nil); err != nil {
				t.Fatalf("%s: decoded value does not re-encode: %v", doc, err)
			}
		}
	}
}
