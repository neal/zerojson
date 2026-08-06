package codec

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neal/zerojson"
)

// linearity_test.go asserts decode work scales ~linearly, not
// quadratically, on inputs constructed to provoke superlinear behavior.
// It is a TEST, not a benchmark, so a quadratic regression fails the build
// loudly rather than merely showing up as a slow benchmark nobody reads.
//
// The adversarial shapes, and which decode path each stresses:
//
//	A. bail-restart amplification — a long array of objects each of which
//	   drives the ordered fast path and then bails to the keyed fallback
//	   (an out-of-order known key plus an unknown trailing key at every
//	   element). If a single bail re-processed the whole element super-
//	   linearly, an array of them would blow up. NOTE: the generated types
//	   have a fixed ~40-field schema and do not self-nest, so an object's
//	   own ordered scan cannot be widened to provoke an intra-object
//	   quadratic; depth amplification is therefore probed separately by D.
//	B. skip-heavy projection — one object of N unknown keys decoded into a
//	   struct that declares none of them, so every member goes through the
//	   grammar-strict Skip.
//	C. long string with a trailing escape — an N-byte string whose only
//	   escape is at the very end, so any scan that restarted on the escape
//	   would be quadratic.
//	D. deep nesting at (and near) the depth cap — nested arrays and nested
//	   objects decoded via ReadInterface, the recursive-descent path that
//	   CAN be scaled in depth (unlike the fixed-schema ordered path).
//
// Each shape is measured at size N and 4N; a linear decoder gives
// time(4N) ~= 4*time(N), so the generous threshold time(4N) < 8*time(N)
// passes for anything linear (or even mildly super-linear from cache
// effects) while a genuine quadratic (~16x) fails. Each reported ratio uses
// min-of-reps timing after a warmup to shed allocation/GC and first-call
// noise, and the assertion uses the median of three independent ratios so a
// single loaded-runner outlier cannot fail a dedicated local run. Wall-clock
// assertions are opt-in via ZEROJSON_LINEARITY=1 because scheduler and VM noise
// make them unsuitable as an ordinary go-test or hosted-CI gate. The shapes
// still decode for correctness when the assertion is disabled.

func linearityAssertionsEnabled() bool {
	return os.Getenv("ZEROJSON_LINEARITY") == "1" && !testing.Short()
}

// measureDecode returns the minimum wall time over reps calls of decode on
// a freshly-built input of the given size, after warmup calls. Min (not
// mean) is the noise-robust estimator here: it is the run least perturbed
// by GC pauses and scheduler preemption on a possibly-loaded machine.
func measureDecode(t *testing.T, build func(n int) []byte, decode func([]byte), n, warmup, reps int) time.Duration {
	t.Helper()
	data := build(n)
	for range warmup {
		decode(data)
	}
	best := time.Duration(1<<62 - 1)
	for range reps {
		start := time.Now()
		decode(data)
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

// assertLinear measures a shape at N and 4N and fails if the median 4N/N time
// ratio across three trials exceeds the quadratic-catching threshold. It logs
// every trial so a failure retains the raw evidence needed for diagnosis.
func assertLinear(t *testing.T, name string, build func(n int) []byte, decode func([]byte), n int) {
	t.Helper()
	const warmup, reps = 3, 9
	attempts := 1
	if linearityAssertionsEnabled() {
		attempts = 3
	}

	ratios := make([]float64, 0, attempts)
	for attempt := range attempts {
		tN := measureDecode(t, build, decode, n, warmup, reps)
		t4N := measureDecode(t, build, decode, 4*n, warmup, reps)
		ratio := float64(t4N) / float64(tN)
		t.Logf("%s trial %d/%d: N=%d %v  4N=%d %v  ratio=%.2fx (linear~4, quadratic~16)", name, attempt+1, attempts, n, tN, 4*n, t4N, ratio)
		// Guard against a divide-by-noise when tN is implausibly small.
		if tN < time.Microsecond {
			t.Logf("%s trial %d/%d: tN below 1us (%v); ratio ignored (too noisy)", name, attempt+1, attempts, tN)
			continue
		}
		ratios = append(ratios, ratio)
	}
	if !linearityAssertionsEnabled() || len(ratios) == 0 {
		return
	}

	sort.Float64s(ratios)
	median := ratios[len(ratios)/2]
	if median >= 8.0 {
		t.Fatalf("%s: repeatable superlinear scaling — median time(4N)/time(N) = %.2fx >= 8x (ratios=%v). Likely quadratic; report with these numbers, do not restructure the decoder.", name, median, ratios)
	}
}

func sizeFor(short, full int) int {
	if testing.Short() {
		return short
	}
	return full
}

// TestDecodeLinearityBailRestart — A: an EventList of N elements, each
// element built to force an ordered-path bail followed by a keyed-fallback
// pass (an out-of-order known key and an unknown trailing key).
func TestDecodeLinearityBailRestart(t *testing.T) {
	// active/origin are late/mid declared fields; presenting active before
	// origin is out of order, and "zz" is unknown — together they exercise
	// the ordered matcher's bail and the fallback's skip on every element.
	const elem = `{"active":true,"origin":"x","seq":1,"zz":0}`
	build := func(n int) []byte {
		var b strings.Builder
		b.Grow(n * (len(elem) + 1))
		b.WriteByte('[')
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(elem)
		}
		b.WriteByte(']')
		return []byte(b.String())
	}
	decode := func(data []byte) {
		var el EventList
		if err := el.UnmarshalZJSON(data); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
	}
	assertLinear(t, "bail-restart array", build, decode, sizeFor(2000, 20000))
}

// TestDecodeLinearitySkipHeavy — B: one object of N unknown keys decoded
// into Event (which declares none of them), so every member is Skip'd.
func TestDecodeLinearitySkipHeavy(t *testing.T) {
	build := func(n int) []byte {
		var b strings.Builder
		b.Grow(n * 16)
		b.WriteByte('{')
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"k`)
			b.WriteString(itoa(i))
			b.WriteString(`":`)
			b.WriteString(itoa(i))
		}
		b.WriteByte('}')
		return []byte(b.String())
	}
	decode := func(data []byte) {
		var ev Event
		if err := ev.UnmarshalZJSON(data); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
	}
	assertLinear(t, "skip-heavy object", build, decode, sizeFor(2000, 20000))
}

// TestDecodeLinearityTrailingEscape — C: an N-byte string value whose only
// escape sits at the very end, decoded as a struct string field.
func TestDecodeLinearityTrailingEscape(t *testing.T) {
	build := func(n int) []byte {
		// {"comment":"aaaa...\n"} — the \n escape is the last content byte.
		var b strings.Builder
		b.Grow(n + 24)
		b.WriteString(`{"comment":"`)
		for range n {
			b.WriteByte('a')
		}
		b.WriteString("\\n\"}")
		return []byte(b.String())
	}
	decode := func(data []byte) {
		var ev Event
		if err := ev.UnmarshalZJSON(data); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
	}
	assertLinear(t, "trailing-escape string", build, decode, sizeFor(100000, 2000000))
}

// TestDecodeLinearityDeepNesting — D: nested arrays and nested objects at
// depth near the cap, decoded via ReadInterface (the recursive-descent
// path, the only decode surface that scales in depth). 4N must stay under
// maxDepth (10000).
func TestDecodeLinearityDeepNesting(t *testing.T) {
	depth := sizeFor(500, 2000) // 4N = 2000 or 8000, both < 10000

	arr := func(n int) []byte {
		b := make([]byte, 0, 2*n)
		for range n {
			b = append(b, '[')
		}
		for range n {
			b = append(b, ']')
		}
		return b
	}
	obj := func(n int) []byte {
		b := make([]byte, 0, 6*n)
		for range n {
			b = append(b, '{', '"', 'a', '"', ':')
		}
		b = append(b, '1')
		for range n {
			b = append(b, '}')
		}
		return b
	}
	decodeIface := func(data []byte) {
		l := zerojson.NewLexer(data, false)
		_ = l.ReadInterface()
		l.Finish()
		if l.Err() != nil {
			t.Fatalf("ReadInterface failed: %v", l.Err())
		}
	}
	assertLinear(t, "deep nested arrays", arr, decodeIface, depth)
	assertLinear(t, "deep nested objects", obj, decodeIface, depth)
}

// itoa is a tiny dependency-free int-to-decimal for the builders above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
