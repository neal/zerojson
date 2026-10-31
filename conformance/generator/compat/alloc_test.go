package compat

import (
	"os"
	"strings"
	"testing"

	"github.com/mailru/easyjson/jwriter"
)

// TestMarshalJSONAllocatesOnce pins the wrappers' buffer sizing: once a size
// has been seen, encoding a similar value takes one allocation instead of
// growing a buffer from empty.
func TestMarshalJSONAllocatesOnce(t *testing.T) {
	if os.Getenv("ZEROJSON_CHECKPTR") != "" {
		t.Skip("exact allocation counts are not stable under checkptr instrumentation")
	}
	p := Point{X: 3, Y: -4, Name: strings.Repeat("n", 200)}
	if n := testing.AllocsPerRun(100, func() {
		if _, err := p.MarshalJSON(); err != nil {
			t.Fatal(err)
		}
	}); n != 1 {
		t.Errorf("MarshalJSON allocations = %v, want 1", n)
	}
	var w jwriter.Writer
	if n := testing.AllocsPerRun(100, func() {
		w.Buffer.Buf = w.Buffer.Buf[:0]
		p.MarshalEasyJSON(&w)
	}); n != 1 {
		t.Errorf("MarshalEasyJSON allocations = %v, want 1", n)
	}
}
