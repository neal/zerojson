package zerojson

import (
	"os"
	"testing"
)

// TestReadJSONNumberBorrowDoesNotAllocate pins that a json.Number literal
// follows the same borrow contract as every other string: aliased in
// borrow mode (zero allocations, so a leaf-only pooled DecodeBorrow loop
// stays allocation-free as the guide promises), copied in copy mode.
func TestReadJSONNumberBorrowDoesNotAllocate(t *testing.T) {
	if os.Getenv("ZEROJSON_CHECKPTR") != "" {
		t.Skip("exact allocation counts are not stable under checkptr instrumentation")
	}
	data := []byte(`1234567890.5e-3`)
	for _, c := range []struct {
		name   string
		borrow bool
		want   float64
	}{{"borrow", true, 0}, {"copy", false, 1}} {
		if c.borrow && !borrowAliases {
			c.want = 1 // the nounsafe build copies in borrow mode too
		}
		got := testing.AllocsPerRun(100, func() {
			l := NewLexer(data, c.borrow)
			if n := l.ReadJSONNumber(); n != string(data) || l.Err() != nil {
				t.Fatalf("%s: got %q err %v", c.name, n, l.Err())
			}
		})
		if got != c.want {
			t.Errorf("%s: ReadJSONNumber allocations=%v, want %v", c.name, got, c.want)
		}
	}
}
