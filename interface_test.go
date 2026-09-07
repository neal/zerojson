package zerojson

import (
	"encoding/json"
	"testing"
)

// TestAppendInterfaceMarshalError hits AppendInterface's json.Marshal
// error path with a value encoding/json cannot marshal.
func TestAppendInterfaceMarshalError(t *testing.T) {
	dst := []byte("PFX")
	got, err := AppendInterface(dst, make(chan int))
	if err == nil {
		t.Fatal("AppendInterface(chan) returned nil error")
	}
	// On error the returned buffer is unchanged (the prefix, no appended value).
	if string(got) != "PFX" {
		t.Fatalf("AppendInterface on error mutated dst: %q", got)
	}
	// Sanity: the same value fails json.Marshal directly (the analogue).
	if _, e := json.Marshal(make(chan int)); e == nil {
		t.Fatal("json.Marshal(chan) unexpectedly succeeded")
	}
}
