package zerojson

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzReadTextBytes(f *testing.F) {
	for _, s := range []string{`""`, `"plain"`, `"\u0061\n"`, `"\q"`, "\"raw\ncontrol\"", `"\u12x4"`, `"\ud800"`, "\"\xff\"", `"unfinished`, `null`, `"ok" trailing`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var want string
		se := json.Unmarshal(data, &want)
		l := NewLexer(data, false)
		got := l.ReadTextBytes()
		l.Finish()
		// The value reader requires a string; generated callers handle null.
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			if l.Err() == nil {
				t.Fatal("text reader accepted null as a string")
			}
			return
		}
		if (se == nil) != (l.Err() == nil) {
			t.Fatalf("text grammar: %q stdlib=%v zerojson=%v", data, se, l.Err())
		}
		if se == nil && string(got) != want {
			t.Fatalf("text content: %q stdlib=%q zerojson=%q", data, want, got)
		}
	})
}
