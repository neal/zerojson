package zerojson_test

import (
	"os"
	"testing"

	"github.com/neal/zerojson"
)

var internContractSink string

func TestInternValueContract(t *testing.T) {
	if got := zerojson.Intern([]byte("filled")); got != "filled" {
		t.Fatalf("Intern([]byte(\"filled\")) = %q", got)
	}
	if zerojson.Intern([]byte("acme")) != zerojson.Intern([]byte("acme")) {
		t.Fatal("equal inputs produced unequal interned values")
	}
	if zerojson.Intern(nil) != "" || zerojson.Intern([]byte{}) != "" {
		t.Fatal("empty input must intern to \"\"")
	}
	long := []byte("0123456789012345678901234") // 25 bytes; over the 24-byte cap.
	if got := zerojson.Intern(long); got != string(long) {
		t.Fatalf("over-cap Intern returned %q", got)
	}
}

func TestInternHitPathZeroAlloc(t *testing.T) {
	if os.Getenv("ZEROJSON_CHECKPTR") != "" {
		t.Skip("exact allocation counts are not stable under checkptr instrumentation")
	}
	tok := []byte("AAPL")
	zerojson.Intern(tok)
	if n := testing.AllocsPerRun(100, func() { internContractSink = zerojson.Intern(tok) }); n != 0 {
		t.Fatalf("intern hit path allocated %v times/op, want 0", n)
	}
}
