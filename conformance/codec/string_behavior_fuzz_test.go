package codec

import (
	"encoding/json"
	"testing"
)

// These fuzzers keep the schema fixed and mutate one meaningful token. This
// complements the whole-document fuzzers: every execution reaches the reader
// under test, and successful stdlib decodes are compared as Go values before a
// re-encode can normalize away differences such as invalid UTF-8.
func FuzzStringValueBehavior(f *testing.F) {
	f.Add([]byte("x"))
	f.Add([]byte(`\u0078`))
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, content []byte) {
		in := append([]byte(`{"origin":"`), content...)
		in = append(in, []byte(`"}`)...)
		var std, zj Event
		if err := json.Unmarshal(in, &std); err != nil {
			if zj.UnmarshalZJSON(in) == nil {
				t.Fatalf("zerojson accepted stdlib-rejected string input %q", in)
			}
			return
		}
		if err := zj.UnmarshalZJSON(in); err != nil {
			t.Fatalf("zerojson rejected stdlib input %q: %v", in, err)
		}
		if zj.Origin != std.Origin {
			t.Fatalf("value mismatch for %q: std=%q (% x) zerojson=%q (% x)", in, std.Origin, []byte(std.Origin), zj.Origin, []byte(zj.Origin))
		}
	})
}

func FuzzStringOptionBehavior(f *testing.F) {
	f.Add([]byte(`"1"`))
	f.Add([]byte(`"\u0031"`))
	f.Add([]byte(`1`))
	f.Fuzz(func(t *testing.T, token []byte) {
		in := append([]byte(`{"count":`), token...)
		in = append(in, '}')
		var std, zj StringOptCases
		if err := json.Unmarshal(in, &std); err != nil {
			return
		}
		if err := zj.UnmarshalZJSON(in); err != nil {
			t.Fatalf("zerojson rejected stdlib input %q: %v", in, err)
		}
		if zj.Count != std.Count {
			t.Fatalf("value mismatch for %q: std=%d zerojson=%d", in, std.Count, zj.Count)
		}
	})
}
