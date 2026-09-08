package codec

import (
	"os"
	"testing"

	"github.com/neal/zerojson/conformance/internal/oracle"
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
// no arena (reused), no string copies (borrowed), no interning miss
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
	if err := d.DecodeBorrow(data, &v); err != nil { // warm the arena
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := d.DecodeBorrow(data, &v); err != nil {
			t.Fatalf("DecodeBorrow: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("EventLegDecoder.DecodeBorrow (reused arena) allocations=%v, want 0", allocs)
	}
}

// TestEventLegPooledDecodeCopyAllocationBudget: the pooled Decoder's copy
// mode still allocates exactly one string (Label) per decode — Go's
// string(bytes) conversion always allocates for a non-empty result. This
// pins that floor exactly, so any additional allocation (e.g. the arena
// itself no longer being reused) is caught.
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
		t.Fatalf("EventLegDecoder.Decode (reused arena, copy mode) allocations=%v, want %v", allocs, wantAllocs)
	}
}

// TestEventLegNonPooledDecodeBorrowAllocationBudget: a one-off
// (non-pooled) UnmarshalZJSONBorrow. The README describes "one
// allocation per decode, zero with -pool" for the general case (the
// per-decode arena backing every pointer field) — but EventLeg has no
// pointer, slice, or map fields at all, so its generated arena type is a
// zero-size struct, and `&zjArenaEventLeg{}` costs nothing (Go shares one
// zero-size backing address for it, the same mechanism that makes an
// empty slice/map literal free — see decode.go's emitNonNilEmptySlice).
// Combined with borrow mode skipping the one string copy, this hits true
// zero, better than the general one-alloc floor.
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
	// Event's Extra/Bag/AnyList fields delegate to encoding/json, so part
	// of this budget is stdlib's, not zerojson's: the v2-backed
	// encoding/json that Go 1.27 ships allocates more on that path than
	// classic v1 did. Both figures are pinned; which one applies is
	// detected by probing the oracle (see internal/oracle), so the budget
	// stays exact under either implementation.
	wantAllocs := 22.0
	if oracle.StdlibJSONV2 {
		wantAllocs = 29.0
	}
	if allocs != wantAllocs {
		t.Fatalf("Event.AppendZJSON (reused buffer) allocations=%v, want %v (update deliberately if a field's encoding legitimately changed; investigate if not)", allocs, wantAllocs)
	}
}

// TestEventPooledDecodeBorrowAllocationBudget pins Event's pooled
// DecodeBorrow floor: even in borrow mode, its Marshaler-delegated and
// interface{}-fallback fields (UnmarshalJSON/json.Unmarshal calls) each
// allocate independent of zerojson's own arena/slice-cell reuse.
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
	t.Logf("EventDecoder.DecodeBorrow (reused arena): %v allocations/decode", allocs)
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
	// Callback-bearing schemas now bypass speculative ordered decoding so a
	// later fallback cannot invoke user UnmarshalJSON/ReadZJSON methods twice.
	// Avoiding that abandoned attempt also removes two allocations here.
	// A pooled Decode zeroes *v before decoding (so the arena never adopts
	// caller-owned memory that a later decode would clear); the two
	// allocations that adds here are IDList.UnmarshalJSON regrowing its own
	// slice, which the reused non-zero target used to keep — user-type
	// storage, not the arena's, which is still reused.
	const maxAllocs = 40.0
	if allocs > maxAllocs {
		t.Fatalf("EventDecoder.DecodeBorrow (reused arena) allocations=%v, budget %v", allocs, maxAllocs)
	}
}

// TestPointerCollectionsPooledFallbackAllocationBudget pins that a pooled
// Decoder keeps recycling its slice and map cells when a document takes the
// KEYED fallback path: keys out of declaration order, whitespace the ordered
// matcher does not consume, and a duplicated member. The ordered attempt
// publishes the arena's own cells into the target and rewinds; the fallback
// then finds them there and must recognize them as ours, not as
// caller-supplied storage to leave alone. A regression here costs one
// allocation per container field per decode for every producer that is
// not zerojson's own compact encoder (pretty-printed JSON, other
// serializers, a reordered wire format).
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
		for i := 0; i < 3; i++ { // warm the arena, including growth
			if err := d.DecodeBorrow(data, &v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		allocs := testing.AllocsPerRun(200, func() {
			if err := d.DecodeBorrow(data, &v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
		if allocs != 0 {
			t.Errorf("%s: PointerCollectionsDecoder.DecodeBorrow (reused arena) allocations=%v, want 0", name, allocs)
		}
	}
}
