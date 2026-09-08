package zerojson

import (
	"os"
	"testing"
)

func TestSameBacking(t *testing.T) {
	base := make([]int, 2, 8)
	if !SameBacking(base, base[:0]) || !SameBacking(base[:0], base[:8]) {
		t.Fatal("same array, different headers, reported as different")
	}
	if SameBacking(base, make([]int, 2, 8)) {
		t.Fatal("different arrays reported as the same")
	}
	if SameBacking(base, nil) || SameBacking([]int{}, []int{}) || SameBacking[int](nil, nil) {
		t.Fatal("headers without capacity must never be the same backing")
	}
	// Reslicing past the shared prefix keeps the same first element.
	if !SameBacking(base[:1], base[1:]) == false {
		// base[1:] starts one element later: a different first element.
		t.Fatal("offset header reported as the same backing")
	}
}

func TestSameMap(t *testing.T) {
	m := map[string]int{"a": 1}
	alias := m
	if !SameMap(m, alias) {
		t.Fatal("aliases reported as different maps")
	}
	if SameMap(m, map[string]int{"a": 1}) {
		t.Fatal("distinct maps reported as the same")
	}
	if !SameMap[string, int](nil, nil) || SameMap(m, nil) {
		t.Fatal("nil handling")
	}
	if os.Getenv("ZEROJSON_CHECKPTR") != "" {
		return // exact allocation counts are not stable under checkptr instrumentation
	}
	if n := testing.AllocsPerRun(100, func() { _ = SameMap(m, alias) }); n != 0 {
		t.Fatalf("SameMap allocates: %v", n)
	}
}
