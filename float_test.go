package zerojson

import (
	"bytes"
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// float_test.go is a float-formatting property test. AppendFloat64/
// AppendFloat32 are documented (writer.go) to match easyjson's
// Float64/Float32 exactly, which is nothing more than
// strconv.AppendFloat(dst, f, 'g', -1, bits) with no additional
// notation-threshold logic on top. That means strconv.AppendFloat itself
// IS the byte-identity oracle for AppendFloat64/32's implementation, and
// this test pins it directly so the root (zero-dependency) module guards
// against reintroducing a hand-rolled formatting path that quietly
// diverges — the live easyjson comparison (the real compatibility target)
// is covered continuously by conformance/codec's value-space fuzzer
// (FuzzEventValueSpaceEncode), which exercises Event's Weight/Ratio32
// fields against a genuine easyjson-generated oracle on every run.

// TestFloatFormatRandomBitPatterns sweeps >2M random uint64 bit
// patterns reinterpreted as float64 (skipping NaN/Inf, which
// AppendFloat64 refuses by contract — see TestFloatFormatNaNInfError)
// and checks AppendFloat64 is byte-exact with strconv.AppendFloat.
func TestFloatFormatRandomBitPatterns(t *testing.T) {
	const n = 2_100_000
	rng := rand.New(rand.NewSource(1))
	for range n {
		bits := rng.Uint64()
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		want := strconv.AppendFloat(nil, f, 'g', -1, 64)
		got, err := AppendFloat64(nil, f)
		if err != nil {
			t.Fatalf("bits %#x (%v): unexpected error: %v", bits, f, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#x: want %s got %s", bits, want, got)
		}
	}
}

// TestFloat32FormatRandomBitPatterns is the float32 sibling sweep,
// reinterpreting random uint32 bit patterns.
func TestFloat32FormatRandomBitPatterns(t *testing.T) {
	const n = 2_100_000
	rng := rand.New(rand.NewSource(2))
	for range n {
		bits := uint32(rng.Uint64())
		f := math.Float32frombits(bits)
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			continue
		}
		want := strconv.AppendFloat(nil, float64(f), 'g', -1, 32)
		got, err := AppendFloat32(nil, f)
		if err != nil {
			t.Fatalf("bits %#x (%v): unexpected error: %v", bits, f, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#x: want %s got %s", bits, want, got)
		}
	}
}

// float64BoundaryCases curates the specific values a random bit-pattern
// sweep can miss by construction (astronomically low hit probability):
// denormals and their edges, +/-0, the 1e20/1e21 notation-threshold
// neighborhood, the smallest denormal (5e-324), and 9007199254740993
// (2^53+1, the first integer float64 can't represent exactly — a classic
// float-formatting edge case).
var float64BoundaryCases = []float64{
	0,
	math.Copysign(0, -1),
	math.SmallestNonzeroFloat64, // 5e-324, smallest denormal
	-math.SmallestNonzeroFloat64,
	4.9406564584124654e-324, // same value written as a literal, not the named const
	2.2250738585072014e-308, // smallest positive normal float64
	math.MaxFloat64,
	-math.MaxFloat64,
	1,
	-1,
	math.Nextafter(1, 2), // 1 ulp above 1
	math.Nextafter(1, 0), // 1 ulp below 1
	1e20,
	-1e20,
	9.999999999999998e19, // just under 1e20
	1e21,
	-1e21,
	1.0000000000000002e21, // just over 1e21
	9999999999999998000,   // just under 1e19 magnitude but near-round
	9007199254740992,      // 2^53, exactly representable
	9007199254740993,      // 2^53 + 1, NOT exactly representable (rounds to 9007199254740992)
	1e-300,
	1e300,
	123.456,
	4.962676536460209e+09, // exact value that exposed the notation-threshold bug
}

func TestFloatFormatBoundaryTable(t *testing.T) {
	for _, f := range float64BoundaryCases {
		want := strconv.AppendFloat(nil, f, 'g', -1, 64)
		got, err := AppendFloat64(nil, f)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", f, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%v: want %s got %s", f, want, got)
		}
	}
}

// float32BoundaryCases is float64BoundaryCases' float32 counterpart:
// denormals, +/-0, the smallest normal float32, and MaxFloat32.
var float32BoundaryCases = []float32{
	0,
	float32(math.Copysign(0, -1)),
	math.SmallestNonzeroFloat32, // ~1.401298e-45
	-math.SmallestNonzeroFloat32,
	1.1754944e-38, // smallest positive normal float32
	math.MaxFloat32,
	-math.MaxFloat32,
	1,
	-1,
	float32(math.Nextafter(1, 2)),
	float32(math.Nextafter(1, 0)),
	1e20,
	-1e20,
	123.456,
}

func TestFloat32FormatBoundaryTable(t *testing.T) {
	for _, f := range float32BoundaryCases {
		want := strconv.AppendFloat(nil, float64(f), 'g', -1, 32)
		got, err := AppendFloat32(nil, f)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", f, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%v: want %s got %s", f, want, got)
		}
	}
}

// TestFloatFormatNaNInfError checks the documented refusal contract (see
// writer.go's AppendFloat64/32 doc): NaN and +/-Inf must error, matching
// encoding/json (UnsupportedValueError) and easyjson (jwriter.Writer.Float64/32,
// which records an error and appends nothing) — never silently emitting
// strconv's raw "NaN"/"+Inf" tokens, which aren't valid JSON.
func TestFloatFormatNaNInfError(t *testing.T) {
	bad64 := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, f := range bad64 {
		dst := []byte("prefix")
		got, err := AppendFloat64(dst, f)
		if err == nil {
			t.Errorf("AppendFloat64(%v): want error, got output %s", f, got)
		}
		if string(got) != "prefix" {
			t.Errorf("AppendFloat64(%v): dst modified on error path: %s", f, got)
		}
	}
	bad32 := []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	for _, f := range bad32 {
		dst := []byte("prefix")
		got, err := AppendFloat32(dst, f)
		if err == nil {
			t.Errorf("AppendFloat32(%v): want error, got output %s", f, got)
		}
		if string(got) != "prefix" {
			t.Errorf("AppendFloat32(%v): dst modified on error path: %s", f, got)
		}
	}
}

// FuzzAppendFloat64 gives the random-bit-pattern property test
// continuous coverage beyond the seeded sweep above.
func FuzzAppendFloat64(f *testing.F) {
	f.Add(uint64(0))
	f.Add(math.Float64bits(1))
	f.Add(math.Float64bits(-1))
	f.Add(math.Float64bits(1e20))
	f.Add(math.Float64bits(1e21))
	f.Add(math.Float64bits(math.SmallestNonzeroFloat64))
	f.Add(math.Float64bits(math.MaxFloat64))
	f.Add(math.Float64bits(9007199254740993))
	for _, v := range float64BoundaryCases {
		f.Add(math.Float64bits(v))
	}
	f.Fuzz(func(t *testing.T, bits uint64) {
		v := math.Float64frombits(bits)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		want := strconv.AppendFloat(nil, v, 'g', -1, 64)
		got, err := AppendFloat64(nil, v)
		if err != nil {
			t.Fatalf("bits %#x: unexpected error %v", bits, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#x: want %s got %s", bits, want, got)
		}
	})
}

// FuzzAppendFloat32 is FuzzAppendFloat64's float32 sibling.
func FuzzAppendFloat32(f *testing.F) {
	f.Add(uint32(0))
	f.Add(math.Float32bits(1))
	f.Add(math.Float32bits(-1))
	f.Add(math.Float32bits(math.SmallestNonzeroFloat32))
	f.Add(math.Float32bits(math.MaxFloat32))
	for _, v := range float32BoundaryCases {
		f.Add(math.Float32bits(v))
	}
	f.Fuzz(func(t *testing.T, bits uint32) {
		v := math.Float32frombits(bits)
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return
		}
		want := strconv.AppendFloat(nil, float64(v), 'g', -1, 32)
		got, err := AppendFloat32(nil, v)
		if err != nil {
			t.Fatalf("bits %#x: unexpected error %v", bits, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("bits %#x: want %s got %s", bits, want, got)
		}
	})
}
