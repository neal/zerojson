package codec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mailru/easyjson"

	"github.com/neal/zerojson"
)

// benchmark_test.go is the in-repo benchmark suite. Every performance claim in the
// README comes from running these (go test -bench . -benchmem in this
// directory), so the repo can demonstrate and guard its own numbers
// instead of relying on a private external harness. easyjson is imported
// deliberately, as the comparison baseline (it was the fastest library
// tested before zerojson). Event is the message shape; EventEJ is its
// genuinely easyjson-generated twin (easyjson_oracle.go).
//
// All benchmarks call b.ReportAllocs via the -benchmem flag; the encode
// and pooled-decode budgets are additionally pinned as hard assertions in
// allocation_test.go so a regression fails the build, not just a benchmark trend.

func benchEvent(b *testing.B) (*Event, []byte) {
	b.Helper()
	ev := variants()["full"]
	data, err := ev.AppendZJSON(nil)
	if err != nil {
		b.Fatal(err)
	}
	return ev, data
}

// --- Encode: fresh buffer, reused buffer, vs easyjson ---

func BenchmarkEncodeEvent_ZJSON_FreshBuffer(b *testing.B) {
	ev, _ := benchEvent(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf, err := ev.AppendZJSON(nil)
		if err != nil {
			b.Fatal(err)
		}
		sink = buf
	}
}

func BenchmarkEncodeEvent_ZJSON_ReusedBuffer(b *testing.B) {
	ev, data := benchEvent(b)
	buf := make([]byte, 0, len(data))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		buf, err = ev.AppendZJSON(buf[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
	sink = buf
}

func BenchmarkEncodeEvent_Easyjson(b *testing.B) {
	ev, _ := benchEvent(b)
	ej := EventEJ(*ev)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf, err := easyjson.Marshal(ej)
		if err != nil {
			b.Fatal(err)
		}
		sink = buf
	}
}

// Event has no MarshalJSON (it is decoded/encoded by AppendZJSON, not a
// json.Marshaler), so encoding/json reflects it structurally — the baseline
// a general user starting from the standard library actually pays.
func BenchmarkEncodeEvent_Stdlib(b *testing.B) {
	ev, _ := benchEvent(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf, err := json.Marshal(ev)
		if err != nil {
			b.Fatal(err)
		}
		sink = buf
	}
}

// --- All-fast-path shape (EventLeg: decimal, string, enum) ---
//
// Event exercises every field kind for coverage, including several
// deliberately non-fast-path ones (Marshaler-delegated Tags/Parents,
// interface{}-fallback Bag/Extra/AnyList, MarshalText YMD) that each cost
// allocations of their own. EventLeg is all direct leaves, so these show
// the fast-path floor — the shape the zero-alloc claims are about — with no
// easyjson baseline here (EventLeg has no easyjson twin; the vs-easyjson
// comparison is on Event above/below).

func benchLeg(b *testing.B) (EventLeg, []byte) {
	b.Helper()
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		b.Fatal(err)
	}
	return leg, data
}

func BenchmarkEncodeEventLeg_ZJSON_ReusedBuffer(b *testing.B) {
	leg, data := benchLeg(b)
	buf := make([]byte, 0, len(data))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		buf, err = leg.AppendZJSON(buf[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
	sink = buf
}

func BenchmarkDecodeEventLeg_ZJSON_Borrow(b *testing.B) {
	_, data := benchLeg(b)
	var e EventLeg
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := e.UnmarshalZJSONBorrow(data); err != nil {
			b.Fatal(err)
		}
	}
	legSink = &e
}

func BenchmarkDecodeEventLeg_ZJSON_PooledBorrow(b *testing.B) {
	_, data := benchLeg(b)
	var d EventLegDecoder
	var e EventLeg
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := d.DecodeBorrow(data, &e); err != nil {
			b.Fatal(err)
		}
	}
	legSink = &e
}

// --- Decode: copy, borrow, pooled copy, pooled borrow, vs easyjson ---

// The decode benchmarks reuse one target across iterations — the way a real
// decode loop (and allocation_test.go's budgets) does — so the reported
// allocs are the decoder's own, not a per-iteration escape of the result
// struct. The error check keeps each call live against dead-code
// elimination; the post-loop sink pins the final value.

// Keep syntax preflight and scalar-slice reuse separately measurable from
// the mixed Event benchmark, which also contains custom codecs and maps.
func BenchmarkValidateEvent(b *testing.B) {
	_, data := benchEvent(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := zerojson.Valid(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeScalarSliceReused(b *testing.B) {
	data := []byte(`{"names":["first",null,"third"]}`)
	var v PointerCollections
	if err := v.UnmarshalZJSONBorrow(data); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := v.UnmarshalZJSONBorrow(data); err != nil {
			b.Fatal(err)
		}
	}
	scalarSliceSink = v.Names
}

var scalarSliceSink *[]string

func BenchmarkDecodeEvent_ZJSON_Copy(b *testing.B) {
	_, data := benchEvent(b)
	var e Event
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := e.UnmarshalZJSON(data); err != nil {
			b.Fatal(err)
		}
	}
	eventSink = &e
}

func BenchmarkDecodeEvent_ZJSON_Borrow(b *testing.B) {
	_, data := benchEvent(b)
	var e Event
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := e.UnmarshalZJSONBorrow(data); err != nil {
			b.Fatal(err)
		}
	}
	eventSink = &e
}

func BenchmarkDecodeEvent_ZJSON_PooledCopy(b *testing.B) {
	_, data := benchEvent(b)
	var d EventDecoder
	var e Event
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := d.Decode(data, &e); err != nil {
			b.Fatal(err)
		}
	}
	eventSink = &e
}

func BenchmarkDecodeEvent_ZJSON_PooledBorrow(b *testing.B) {
	_, data := benchEvent(b)
	var d EventDecoder
	var e Event
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := d.DecodeBorrow(data, &e); err != nil {
			b.Fatal(err)
		}
	}
	eventSink = &e
}

// --- Intern backend comparison ---
//
// These intentionally-small generated shapes isolate the two places where
// interning remains explicit: a tagged string field and a tagged map key.
// The parallel variants give every worker its own pooled decoder and target,
// so the shared interner is the only contended state.

func BenchmarkDecodeInternedString_ZJSON_PooledBorrow(b *testing.B) {
	data := []byte(`{"op":"INSERT"}`)
	var d MapFieldsLikeDecoder
	var v MapFieldsLike
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		if err := d.DecodeBorrow(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeInternedString_ZJSON_PooledBorrowParallel(b *testing.B) {
	data := []byte(`{"op":"INSERT"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var d MapFieldsLikeDecoder
		var v MapFieldsLike
		for pb.Next() {
			if err := d.DecodeBorrow(data, &v); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkDecodeInternedMapKey_ZJSON_PooledBorrow(b *testing.B) {
	data := []byte(`{"amounts":{"ALPHA":"12.5"}}`)
	var d EventDecoder
	var v Event
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		if err := d.DecodeBorrow(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeInternedMapKey_ZJSON_PooledBorrowParallel(b *testing.B) {
	data := []byte(`{"amounts":{"ALPHA":"12.5"}}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var d EventDecoder
		var v Event
		for pb.Next() {
			if err := d.DecodeBorrow(data, &v); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkDecodeEvent_Easyjson(b *testing.B) {
	_, data := benchEvent(b)
	var e EventEJ
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := easyjson.Unmarshal(data, &e); err != nil {
			b.Fatal(err)
		}
	}
	ejSink = &e
}

// encoding/json reflects Event structurally on decode too — the stdlib
// decode baseline.
func BenchmarkDecodeEvent_Stdlib(b *testing.B) {
	_, data := benchEvent(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var e Event
		if err := json.Unmarshal(data, &e); err != nil {
			b.Fatal(err)
		}
		eventSink = &e
	}
}

// --- Ordered fast path vs keyed fallback, on identical bytes ---
//
// EventLeg is callback-free and therefore eligible for speculation. Both
// benchmarks decode the same compact, declaration-ordered bytes into a fresh
// arena; the only difference is that the ordered path (the public
// dispatcher zjDecode_EventLeg) speculatively tries the ordered decoder first,
// while the fallback path calls zjDecodeFallback_EventLeg directly. Both are
// in-package (the generated fallback decoder and arena type are callable
// here), so this isolates the fast-path win without a -no-ordered build.
// The two produce reflect.DeepEqual results by construction (pinned by
// FuzzOrderedVsFallback and the generated fuzzers).

func BenchmarkDecodeEventLeg_OrderedPath(b *testing.B) {
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		b.Fatal(err)
	}
	var e EventLeg
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var a zjArenaEventLeg
		l := zerojson.NewLexer(data, false)
		zjDecode_EventLeg(&l, &e, &a)
		l.Finish()
		if err := l.Err(); err != nil {
			b.Fatal(err)
		}
	}
	legSink = &e
}

func BenchmarkDecodeEventLeg_FallbackPath(b *testing.B) {
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		b.Fatal(err)
	}
	var e EventLeg
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var a zjArenaEventLeg
		l := zerojson.NewLexer(data, false)
		zjDecodeFallback_EventLeg(&l, &e, &a)
		l.Finish()
		if err := l.Err(); err != nil {
			b.Fatal(err)
		}
	}
	legSink = &e
}

// --- Projection: decode only a few fields of a full Event's bytes ---
//
// EventProjection declares 2 of Event's ~30 wire fields; the rest are
// skipped inline. Compared against a full Event decode over the same bytes,
// this shows what the projection/passthrough pattern costs (or saves).

func BenchmarkDecodeEventProjection_ZJSON(b *testing.B) {
	_, data := benchEvent(b)
	var p EventProjection
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := p.UnmarshalZJSON(data); err != nil {
			b.Fatal(err)
		}
	}
	projSink = &p
}

// --- Valid over the golden corpus ---

func BenchmarkValidGoldenCorpus(b *testing.B) {
	for _, fx := range goldenFixtures {
		data := readGolden(b, fx.name)
		b.Run(fx.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for range b.N {
				if err := zerojson.Valid(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- Batch replay over the golden corpus ---
//
// Models a batch of records, each decoded (pooled, borrow) and re-encoded into
// a reused buffer. Uses the golden corpus files as the batch.

func BenchmarkGoldenBatchReplay(b *testing.B) {
	var batch [][]byte
	for _, fx := range goldenFixtures {
		batch = append(batch, readGolden(b, fx.name))
	}
	var d MapFieldsLikeDecoder
	buf := make([]byte, 0, 4096)
	var total int
	for _, msg := range batch {
		total += len(msg)
	}
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for range b.N {
		for _, msg := range batch {
			var rec MapFieldsLike
			if err := d.DecodeBorrow(msg, &rec); err != nil {
				b.Fatal(err)
			}
			var err error
			buf, err = rec.AppendZJSON(buf[:0])
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	sink = buf
}

func readGolden(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden", name+".json"))
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// Sinks defeat dead-store elimination without escaping into the timed loop.
var (
	sink      []byte
	eventSink *Event
	legSink   *EventLeg
	ejSink    *EventEJ
	projSink  *EventProjection
)
