package codec

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

var (
	resourceBytesSink    BytesCases
	resourceEventSink    Event
	resourceStdBytesSink BytesCases
	resourceStdEventSink Event
)

func buildNumericByteArray(n int) []byte {
	var b strings.Builder
	b.Grow(10 + n*4)
	b.WriteString(`{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(itoa(i & 255))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func buildBase64Bytes(n int) []byte {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	return []byte(`{"data":"` + encoded + `"}`)
}

func buildInvalidBase64Bytes(n int) []byte {
	if n < 1 {
		n = 1
	}
	return []byte(`{"data":"` + strings.Repeat("A", n-1) + `!"}`)
}

func buildByteMap(n int) []byte {
	var b strings.Builder
	b.Grow(12 + n*18)
	b.WriteString(`{"index":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k`)
		b.WriteString(itoa(i))
		b.WriteString(`":[0,1,255]`)
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

func buildInterfaceArray(n int) []byte {
	var b strings.Builder
	b.Grow(12 + n*3)
	b.WriteString(`{"extra":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(itoa(i & 127))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func decodeResourceBytes(data []byte) error {
	var value BytesCases
	err := value.UnmarshalZJSON(data)
	resourceBytesSink = value
	return err
}

func decodeResourceEvent(data []byte) error {
	var value Event
	err := value.UnmarshalZJSON(data)
	resourceEventSink = value
	return err
}

func decodeStdResourceBytes(data []byte) error {
	var value BytesCases
	err := json.Unmarshal(data, &value)
	resourceStdBytesSink = value
	return err
}

func decodeStdResourceEvent(data []byte) error {
	var value Event
	err := json.Unmarshal(data, &value)
	resourceStdEventSink = value
	return err
}

// allocatedBytesPerCall measures total heap bytes rather than allocation
// count. A linear decoder may legitimately grow a slice geometrically and
// therefore perform O(log N) allocations, while the total bytes must remain
// O(N). Inputs are built before the measurement and each result is retained in
// a package sink so the compiler cannot discard the decode.
func allocatedBytesPerCall(t *testing.T, data []byte, decode func([]byte) error, wantErr bool) uint64 {
	t.Helper()
	if err := decode(data); (err != nil) != wantErr {
		t.Fatalf("oracle input error mismatch: err=%v wantErr=%v", err, wantErr)
	}
	const reps = 7
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range reps {
		_ = decode(data)
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / reps
}

func assertAllocationLinear(t *testing.T, name string, build func(int) []byte, decode, oracle func([]byte) error, wantErr bool, n int, maxBytesPerInput float64) {
	t.Helper()
	dataN := build(n)
	data4N := build(4 * n)
	allocN := allocatedBytesPerCall(t, dataN, decode, wantErr)
	alloc4N := allocatedBytesPerCall(t, data4N, decode, wantErr)
	oracle4N := allocatedBytesPerCall(t, data4N, oracle, wantErr)
	ratio := float64(alloc4N) / float64(allocN)
	perInput := float64(alloc4N) / float64(len(data4N))
	vsOracle := float64(alloc4N) / float64(oracle4N)
	t.Logf("%s: input=%d->%d allocated=%d->%d ratio=%.2fx bytes/input=%.2fx stdlib=%d zerojson/stdlib=%.2fx", name, len(dataN), len(data4N), allocN, alloc4N, ratio, perInput, oracle4N, vsOracle)
	if allocN >= 4096 && ratio >= 6.0 {
		t.Fatalf("%s: superlinear allocated-byte growth: 4N/N=%.2fx", name, ratio)
	}
	if perInput > maxBytesPerInput {
		t.Fatalf("%s: excessive allocation amplification: %.2fx input bytes > %.2fx budget", name, perInput, maxBytesPerInput)
	}
	if vsOracle > 1.5 {
		t.Fatalf("%s: zerojson allocates %.2fx encoding/json; budget is 1.50x", name, vsOracle)
	}
}

// TestDecodeResourceScaling complements the time-only linearity suite with
// allocated-byte scaling. It includes successful materialization paths and a
// large failure path: rejecting attacker-controlled input must not hide a
// superlinear allocation bomb.
func TestDecodeResourceScaling(t *testing.T) {
	cases := []struct {
		name             string
		build            func(int) []byte
		decode           func([]byte) error
		oracle           func([]byte) error
		wantErr          bool
		n                int
		maxBytesPerInput float64
	}{
		{"numeric byte array", buildNumericByteArray, decodeResourceBytes, decodeStdResourceBytes, false, sizeFor(2_000, 20_000), 4},
		{"base64 bytes", buildBase64Bytes, decodeResourceBytes, decodeStdResourceBytes, false, sizeFor(5_000, 50_000), 2},
		{"invalid base64", buildInvalidBase64Bytes, decodeResourceBytes, decodeStdResourceBytes, true, sizeFor(5_000, 50_000), 2},
		// Map keys are copied per decode (interning is opt-in via
		// zerojson:"intern"; this map is untagged), so decoding the same data
		// repeatedly does not amortize key allocation. Growth stays linear
		// (the ratio check) and below encoding/json (the vsOracle check); the
		// absolute constant is ~12.8x under -race.
		{"byte-valued map", buildByteMap, decodeResourceBytes, decodeStdResourceBytes, false, sizeFor(200, 2_000), 15},
		{"interface array", buildInterfaceArray, decodeResourceEvent, decodeStdResourceEvent, false, sizeFor(1_000, 10_000), 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertAllocationLinear(t, tc.name, tc.build, tc.decode, tc.oracle, tc.wantErr, tc.n, tc.maxBytesPerInput)
			if testing.Short() {
				return
			}
			decodeForTime := func(data []byte) {
				if err := tc.decode(data); (err != nil) != tc.wantErr {
					t.Fatalf("decode error mismatch: %v", err)
				}
			}
			assertLinear(t, tc.name+" resource path", tc.build, decodeForTime, tc.n)
		})
	}
}
