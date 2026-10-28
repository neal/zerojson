package codec

import (
	"os"
	"testing"

	"github.com/neal/zerojson"
	decimal "github.com/neal/zerojson/conformance/internal/testleaf"
)

func requireStableAllocationInstrumentation(t *testing.T) {
	t.Helper()
	if os.Getenv("ZEROJSON_CHECKPTR") != "" {
		t.Skip("exact allocation counts are not stable under checkptr instrumentation")
	}
}

// allocation_test.go converts the README's benchmark-only allocation claims
// into plain Go tests, following a sibling codec project's perf_test.go
// pattern (TestMessageParseReuseAllocationBudget): testing.AllocsPerRun
// assertions that fail the build the moment a change regresses the
// zero-alloc contract, rather than relying on someone noticing a
// benchmark number moved.
//
// EventLeg (not Event) is used for the strict zero-allocation claims:
// every one of its fields (a decimal, a plain string, an enum) is a
// direct leaf fast path, mirroring the all-leaf-kind benchmark shape used
// for the README's headline numbers. Event itself deliberately
// exercises every field kind the generator supports for coverage —
// including several genuinely non-fast-path ones (Marshaler delegation
// for Tags/Parents/StrInt64, encoding/json fallback for Extra/Bag/AnyList,
// fmt.Appendf-based MarshalText for YMD) — so it does NOT hit zero
// allocations, on purpose; its budget is asserted below too, pinned at
// its current honest (non-zero) value so a regression that adds
// allocations beyond what those exotic fields already cost is still
// caught.

func fullEventLeg() EventLeg {
	return EventLeg{
		Ratio: decimal.RequireFromString("2.5"),
		Label: "ALPHA",
		Mode:  ModeUp,
	}
}

// TestEventLegEncodeReuseAllocationBudget: encoding an all-leaf-kind
// struct into a reused buffer must not allocate.
func TestEventLegEncodeReuseAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	leg := fullEventLeg()
	buf, err := leg.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		buf = buf[:0]
		var err error
		buf, err = leg.AppendZJSON(buf)
		if err != nil {
			t.Fatalf("AppendZJSON: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("EventLeg.AppendZJSON (reused buffer) allocations=%v, want 0", allocs)
	}
}

// TestEventLegPooledDecodeBorrowAllocationBudget: a pooled Decoder's
// DecodeBorrow on an all-leaf-kind struct must not allocate at all —
// no escaping arena, no string copies (borrowed), no interning miss
// (Mode is a known enum constant, Label isn't tagged intern so it's
// just a borrowed string header).
func TestEventLegPooledDecodeBorrowAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	var d EventLegDecoder
	var v EventLeg
	if err := d.DecodeBorrow(data, &v); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := d.DecodeBorrow(data, &v); err != nil {
			t.Fatalf("DecodeBorrow: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("EventLegDecoder.DecodeBorrow (fresh per-call storage) allocations=%v, want 0", allocs)
	}
}

// TestEventLegPooledDecodeCopyAllocationBudget: the pooled Decoder's copy
// mode still allocates exactly one string (Label) per decode — Go's
// string(bytes) conversion always allocates for a non-empty result. This
// pins that floor exactly, so any additional allocation is caught.
func TestEventLegPooledDecodeCopyAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	var d EventLegDecoder
	var v EventLeg
	if err := d.Decode(data, &v); err != nil {
		t.Fatal(err)
	}

	const wantAllocs = 1 // Label's string copy
	allocs := testing.AllocsPerRun(1000, func() {
		if err := d.Decode(data, &v); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs != wantAllocs {
		t.Fatalf("EventLegDecoder.Decode (fresh per-call storage, copy mode) allocations=%v, want %v", allocs, wantAllocs)
	}
}

// TestEventLegNonPooledDecodeBorrowAllocationBudget: a one-off
// (non-pooled) UnmarshalZJSONBorrow. EventLeg has no pointer or container
// fields, so its zero-sized arena does not allocate. Borrowing avoids the
// remaining string copy.
func TestEventLegNonPooledDecodeBorrowAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	leg := fullEventLeg()
	data, err := leg.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	var v EventLeg
	if err := v.UnmarshalZJSONBorrow(data); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		var v EventLeg
		if err := v.UnmarshalZJSONBorrow(data); err != nil {
			t.Fatalf("UnmarshalZJSONBorrow: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("non-pooled EventLeg.UnmarshalZJSONBorrow allocations=%v, want 0", allocs)
	}
}

// TestEventEncodeAllocationBudget pins Event's own (non-zero, by design)
// encode allocation floor: its exotic coverage fields (Tags/Parents/
// StrInt64's MarshalJSON delegation, Extra/Bag/AnyList's encoding/json
// fallback, YMD's fmt.Appendf-based MarshalText) each cost at least one
// allocation on their own, independent of zerojson's own code. Pinned
// exactly so a change to the fast-path fields' encoding doesn't silently
// add to this floor.
//
// What the encoder delegates is measured at run time and subtracted
// (delegatedEncodeAllocs): the interface{} fallback's encoding/json calls
// and the fixture's own MarshalJSON/MarshalText methods (Tags.MarshalJSON
// itself calls json.Marshal). That share moves with the Go release — the
// v2-backed encoding/json that Go 1.27 ships allocates more on it than
// classic v1 did — while what this test pins is zerojson's own count, so
// it stays exact on any toolchain without per-implementation constants.
func TestEventEncodeAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	tr := variants()["full"]
	buf, err := tr.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		buf = buf[:0]
		var err error
		buf, err = tr.AppendZJSON(buf)
		if err != nil {
			t.Fatalf("AppendZJSON: %v", err)
		}
	})
	t.Logf("Event.AppendZJSON (reused buffer): %v allocations/encode (non-zero by design — see file comment)", allocs)
	if raceDetectorEnabled {
		// The race detector's shadow-memory bookkeeping for map/interface
		// accesses (Event's Extra/Bag/AnyList `any` fields fall back to
		// encoding/json, which is map/interface-heavy) perturbs
		// testing.AllocsPerRun's count in a way that isn't even stable run
		// to run under -race (observed 32, 33, and other nearby values
		// across repeated runs of just this test) — not a real
		// regression, just measurement noise from instrumentation this
		// budget isn't trying to characterize. The exact, deterministic
		// budget enforcement below only runs in a non-race build; a race
		// build still exercises the same code path (so a panic or data
		// race is still caught) but doesn't assert a specific count.
		return
	}
	delegated := delegatedEncodeAllocs(tr)
	t.Logf("of which delegated (encoding/json fallback and user marshal methods): %v", delegated)
	const wantOwnAllocs = 0.0 // every remaining allocation is in delegated code
	if own := allocs - delegated; own != wantOwnAllocs {
		t.Fatalf("Event.AppendZJSON (reused buffer) allocations=%v, of which delegated %v: own=%v, want %v (update deliberately if a field's encoding legitimately changed; investigate if not)", allocs, delegated, own, wantOwnAllocs)
	}
}

// delegatedEncodeAllocs measures what Event's generated encoder spends in
// code it delegates to, call for call: one zerojson.AppendInterface
// (json.Marshal) per Extra, per Bag value, and per AnyList element, plus the
// Tags/Parents MarshalJSON and PrimaryDate/SecondDate MarshalText calls. The
// fallback buffer is pre-sized, so only json.Marshal's own allocations count.
func delegatedEncodeAllocs(e *Event) float64 {
	var calls []func()
	buf := make([]byte, 0, 4096)
	fallback := func(v any) func() {
		return func() { buf, _ = zerojson.AppendInterface(buf[:0], v) }
	}
	if e.Extra != nil {
		calls = append(calls, fallback(e.Extra))
	}
	for _, v := range e.Bag {
		calls = append(calls, fallback(v))
	}
	for _, v := range e.AnyList {
		calls = append(calls, fallback(v))
	}
	if len(e.Tags) != 0 {
		calls = append(calls, func() { _, _ = e.Tags.MarshalJSON() })
	}
	calls = append(calls, func() { _, _ = e.Parents.MarshalJSON() })
	calls = append(calls, func() { _, _ = e.PrimaryDate.MarshalText() })
	if e.SecondDate != nil {
		calls = append(calls, func() { _, _ = e.SecondDate.MarshalText() })
	}
	var total float64
	for _, call := range calls {
		total += testing.AllocsPerRun(1000, call)
	}
	return total
}

// TestEventPooledDecodeBorrowAllocationBudget pins Event's pooled
// DecodeBorrow floor: even in borrow mode, its Marshaler-delegated and
// interface{}-fallback fields (UnmarshalJSON/json.Unmarshal calls) each
// allocate in addition to zerojson's fresh per-call storage.
func TestEventPooledDecodeBorrowAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	tr := variants()["full"]
	data, err := tr.AppendZJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	var d EventDecoder
	var v Event
	if err := d.DecodeBorrow(data, &v); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := d.DecodeBorrow(data, &v); err != nil {
			t.Fatalf("DecodeBorrow: %v", err)
		}
	})
	t.Logf("EventDecoder.DecodeBorrow (fresh per-call storage): %v allocations/decode", allocs)
	if raceDetectorEnabled {
		// This path's race-detector overhead is itself nondeterministic
		// run to run (observed both 40 and 41, and occasionally other
		// nearby values, across dozens of repeated runs of just this
		// test — see TestEventEncodeAllocationBudget's sibling comment
		// for the likely cause: GC-timing-dependent map bucket growth in
		// the Bag/Extra/AnyList encoding/json fallback, perturbed by the
		// race detector's shadow-memory bookkeeping). A pinned or even
		// banded race-build count proved flaky in practice, so — same as
		// the encode budget above — this only exercises the code path
		// under -race (still catching a panic or data race) without
		// asserting a specific count; the exact, deterministic budget
		// enforcement is the non-race assertion below.
		return
	}
	// Fresh per-call storage deliberately replaces the old 39-allocation
	// persistent arena. The architecture comparison measured 55; allow one
	// additional allocation for toolchain variation, not unbounded growth.
	const maxAllocs = 56.0
	if allocs > maxAllocs {
		t.Fatalf("EventDecoder.DecodeBorrow (fresh per-call storage) allocations=%v, budget %v", allocs, maxAllocs)
	}
}

func TestReusedScalarSliceAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	data := []byte(`{"names":["first",null,"third"]}`)
	var v PointerCollections
	if err := v.UnmarshalZJSONBorrow(data); err != nil {
		t.Fatal(err)
	}
	n := testing.AllocsPerRun(200, func() {
		if err := v.UnmarshalZJSONBorrow(data); err != nil {
			t.Fatal(err)
		}
	})
	// The containing non-pooled arena escapes; the scalar traversal itself
	// must not add stdlib state or string-copy allocations in borrow mode.
	if n > 1 {
		t.Fatalf("reused scalar slice allocations=%g, budget 1", n)
	}
}

// Pin fallback costs while preserving element reuse across rewinds and
// duplicate members, including stdlib delegation where still required.
func TestPointerCollectionsPooledFallbackAllocationBudget(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	for name, doc := range map[string]string{
		"declaration order": `{"names":["a","b"],"index":{"k":1}}`,
		"out of order":      `{"index":{"k":1},"names":["a","b"]}`,
		"spaced":            `{"names": ["a", "b"], "index": {"k": 1}}`,
		"duplicate member":  `{"names":["a"],"names":["a","b"],"index":{"k":1},"index":{"j":2}}`,
		"bail on first key": `{"zzz":0,"names":["a","b"],"index":{"k":1}}`,
	} {
		var d PointerCollectionsDecoder
		var v PointerCollections
		data := []byte(doc)
		for i := 0; i < 3; i++ {
			if err := d.DecodeBorrow(data, &v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		allocs := testing.AllocsPerRun(200, func() {
			if err := d.DecodeBorrow(data, &v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
		// Revisited slices preserve stdlib element reuse, with generated
		// scalar traversal where eligible.
		// This is a ceiling: newer stdlib implementations can allocate less.
		want := 5.0 // fresh arena, slice growth, and map storage
		if allocs > want {
			t.Errorf("%s: PointerCollectionsDecoder.DecodeBorrow allocations=%v, budget %v", name, allocs, want)
		}
	}
}
