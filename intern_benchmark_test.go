package zerojson_test

import (
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/neal/zerojson"
)

var (
	internBenchmarkSink    string
	internBenchmarkCounter atomic.Uint64
	internBenchmarkHot     = [][]byte{
		[]byte("acme"), []byte("globex"), []byte("initech"),
		[]byte("umbrella"), []byte("wonka"), []byte("soylent"),
	}
)

func primeInternBenchmarkHotSet() {
	for _, token := range internBenchmarkHot {
		_ = zerojson.Intern(token)
	}
}

func BenchmarkInternHotHit(b *testing.B) {
	token := []byte("acme")
	_ = zerojson.Intern(token)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		internBenchmarkSink = zerojson.Intern(token)
	}
}

func BenchmarkInternHotHitParallel(b *testing.B) {
	primeInternBenchmarkHotSet()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = zerojson.Intern(internBenchmarkHot[i%len(internBenchmarkHot)])
			i++
		}
	})
}

// BenchmarkInternMixedParallel models a mostly-hot token stream with one new
// short value every ten operations. The global counter never resets during a
// benchmark process, so calibration and repeated samples cannot accidentally
// turn the miss stream into hits.
func BenchmarkInternMixedParallel(b *testing.B) {
	primeInternBenchmarkHotSet()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var storage [24]byte
		i := 0
		for pb.Next() {
			if i%10 == 0 {
				n := internBenchmarkCounter.Add(1)
				token := strconv.AppendUint(storage[:0], n, 36)
				_ = zerojson.Intern(token)
			} else {
				_ = zerojson.Intern(internBenchmarkHot[i%len(internBenchmarkHot)])
			}
			i++
		}
	})
}

// BenchmarkInternCardinality feeds a never-repeated stream of short values.
// It exposes insertion, saturation, allocation, and GC behavior rather than
// measuring a pre-warmed cache.
func BenchmarkInternCardinality(b *testing.B) {
	var storage [24]byte
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n := internBenchmarkCounter.Add(1)
		token := strconv.AppendUint(storage[:0], n, 36)
		internBenchmarkSink = zerojson.Intern(token)
	}
}

// BenchmarkInternHotSetGCEpoch forces a collection before every 4,096 hot
// lookups. A strong bounded table should retain its entries; a weak interner
// may need to recreate canonical entries after each collection. Metrics are
// per GC epoch, not per individual Intern call.
func BenchmarkInternHotSetGCEpoch(b *testing.B) {
	primeInternBenchmarkHotSet()
	const lookups = 4096
	b.ReportAllocs()
	b.ReportMetric(lookups, "interns/epoch")
	b.ResetTimer()
	for range b.N {
		runtime.GC()
		for i := range lookups {
			internBenchmarkSink = zerojson.Intern(internBenchmarkHot[i%len(internBenchmarkHot)])
		}
	}
}

// BenchmarkInternPostCardinalityGCEpoch measures the lasting GC cost after a
// burst of 200,000 distinct short values. The setup is outside the benchmark
// timer but remains visible in the process-level peak-memory measurements.
func BenchmarkInternPostCardinalityGCEpoch(b *testing.B) {
	var storage [24]byte
	b.StopTimer()
	for range 200_000 {
		n := internBenchmarkCounter.Add(1)
		token := strconv.AppendUint(storage[:0], n, 36)
		_ = zerojson.Intern(token)
	}
	runtime.GC()

	const lookups = 4096
	b.ReportAllocs()
	b.ReportMetric(lookups, "interns/epoch")
	b.StartTimer()
	for range b.N {
		runtime.GC()
		for i := range lookups {
			internBenchmarkSink = zerojson.Intern(internBenchmarkHot[i%len(internBenchmarkHot)])
		}
	}
}
