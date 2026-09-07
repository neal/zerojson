package codec

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"
)

// value_fuzz_test.go is a value-space encode fuzzer. Every other fuzzer in this package (FuzzDecodeAgainstStdlib,
// FuzzOrderedVsFallback, the generated FuzzEventZJSON, ...) starts from
// JSON *bytes* and decodes them into a Event — which can only ever
// reach encoder states that are also reachable by round-tripping valid
// JSON (a decoded float64 field, for instance, can never end up NaN or
// +Inf, since neither is a valid JSON number token encoding/json will
// decode). fillEvent instead builds a Event directly from fuzz bytes,
// reaching producer-side-only states: NaN/Inf floats, invalid UTF-8 in a
// string field, decimals with extreme (but in-range) exponents, and
// every enum value including unknown ones — then compares zerojson's
// encoder against a genuine independent easyjson oracle (EventEJ, see
// easyjson_oracle.go — real easyjson-generated code for a type with
// Event's exact field layout, not a hand-simulated comparison).

// filler deterministically maps fuzz bytes to field values: each method
// consumes a small, fixed number of bytes from data, wrapping around
// (not reading past the end and erroring) once exhausted, so every
// non-empty fuzz input produces a fully populated Event deterministically
// (same input bytes always produce the same Event).
type filler struct {
	data []byte
	pos  int
}

func newFiller(data []byte) *filler {
	if len(data) == 0 {
		data = []byte{0}
	}
	return &filler{data: data}
}

func (f *filler) byte() byte {
	b := f.data[f.pos%len(f.data)]
	f.pos++
	return b
}

func (f *filler) uint64() uint64 {
	var v uint64
	for range 8 {
		v = v<<8 | uint64(f.byte())
	}
	return v
}

func (f *filler) int64() int64 { return int64(f.uint64()) }

func (f *filler) bool() bool { return f.byte()&1 == 1 }

// float64Value mostly returns a raw reinterpreted uint64 bit pattern
// (covering the full float64 space, including subnormals and huge
// magnitudes — see also float_test.go's dedicated property test for
// formatting specifically), but a fraction of the time returns NaN,
// +/-Inf, or +/-0 outright: value-only states no amount of JSON-text
// fuzzing can produce, since none of encoding/json, easyjson, or
// zerojson will ever *decode* a NaN/Inf float64 from valid input.
func (f *filler) float64Value() float64 {
	switch f.byte() % 10 {
	case 0:
		return math.NaN()
	case 1:
		return math.Inf(1)
	case 2:
		return math.Inf(-1)
	case 3:
		return 0
	case 4:
		return math.Copysign(0, -1)
	default:
		return math.Float64frombits(f.uint64())
	}
}

func (f *filler) float32Value() float32 {
	switch f.byte() % 10 {
	case 0:
		return float32(math.NaN())
	case 1:
		return float32(math.Inf(1))
	case 2:
		return float32(math.Inf(-1))
	case 3:
		return 0
	default:
		return math.Float32frombits(uint32(f.uint64()))
	}
}

// stringValue returns a string of up to 15 bytes built directly from
// fuzz bytes (not from valid UTF-8 runes), so it frequently contains
// invalid UTF-8 sequences — lone continuation bytes, truncated
// multi-byte sequences, overlong encodings — exercising AppendString's
// UTF-8-validating slow path (see writer.go's appendNonASCII) the same
// way TestAppendStringUTF8MatchesBothOracles does with a curated table,
// but continuously and in combination with every other field.
func (f *filler) stringValue() string {
	n := int(f.byte() % 16)
	if n == 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = f.byte()
	}
	return string(b)
}

// decimalValue covers extreme values supported by the conformance-only leaf
// coefficients and exponents, including negative values. Exponents are
// bounded to +/-18 — matching event_codec_test.go's existing hugeExponent
// fuzz guard's rationale: shopspring-style decimals expand very large
// exponents to millions of digits in String(), a property shared by
// every codec here, not something this fuzzer needs to explore.
func (f *filler) decimalValue() decimal.Decimal {
	coeff := f.int64()
	exp := int32(f.byte()%37) - 18
	return decimal.New(coeff, exp)
}

func (f *filler) uuidValue() uuid.UUID {
	var u uuid.UUID
	for i := range u {
		u[i] = f.byte()
	}
	return u
}

// timeValue returns the zero time.Time a meaningful fraction of the
// time (a value-space-only edge case worth covering deliberately: the
// zero time.Time's AppendTime path, and its interaction with omitempty,
// which gates on the ordinary zero Go value, not IsZero()). It also
// reaches producer-only edges no JSON-text fuzzing can: extreme FixedZone
// offsets (including |offset| >= 24h, which AppendTime and
// time.Time.MarshalJSON both refuse as non-RFC3339) and year extremes
// outside [0,9999] (likewise refused) — so the encode error-agreement
// path (FuzzEventValueSpaceEncode) exercises these unencodable states,
// not just the happy path.
func (f *filler) timeValue() time.Time {
	switch f.byte() % 8 {
	case 0:
		return time.Time{}
	case 1:
		// Extreme zone offset, occasionally past the ±24h RFC3339 limit
		// (offset can reach ±~35h here), plus second-granularity offsets.
		off := int(f.int64()%(36*3600*2+1)) - 36*3600
		return time.Unix(f.int64()%1e11, f.int64()%1e9).In(time.FixedZone("x", off))
	case 2:
		// Year extremes on both sides of the [0,9999] bound.
		y := int(f.int64()%20000) - 5000
		return time.Date(y, 1, 1, 0, 0, 0, int(f.int64()%1e9), time.UTC)
	default:
		sec := f.int64() % 1e11 // keep within a sane calendar range
		nsec := f.int64() % 1e9
		return time.Unix(sec, nsec).UTC()
	}
}

func (f *filler) modeValue() Mode {
	switch f.byte() % 4 {
	case 0:
		return ModeUp
	case 1:
		return ModeDown
	case 2:
		return Mode(f.stringValue()) // unknown enum value
	default:
		return ""
	}
}

func (f *filler) phaseValue() Phase {
	switch f.byte() % 4 {
	case 0:
		return PhaseNew
	case 1:
		return PhaseClosed
	case 2:
		return Phase(f.stringValue())
	default:
		return ""
	}
}

// sliceState picks nil, non-nil-empty, or populated — the three states
// TestDecodeEmptyArrayVsNullVsAbsent pins on the decode side; this
// exercises all three from the encode side instead.
type sliceState int

const (
	sliceNil sliceState = iota
	sliceEmpty
	slicePopulated
)

func (f *filler) sliceState() sliceState {
	return sliceState(f.byte() % 3)
}

func fillEventLeg(f *filler) EventLeg {
	return EventLeg{
		Ratio: f.decimalValue(),
		Label: f.stringValue(),
		Mode:  f.modeValue(),
	}
}

// fillEvent deterministically builds a Event from fuzz bytes, touching
// every field kind Event declares (see types.go's Event doc), including
// value-space-only states normal JSON-text fuzzing can't reach.
func fillEvent(data []byte) Event {
	f := newFiller(data)
	var tr Event

	tr.CreatedAt = f.timeValue()
	if f.bool() {
		v := f.timeValue()
		tr.ClosedAt = &v
	}

	tr.Amount = f.decimalValue()
	if f.bool() {
		v := f.decimalValue()
		tr.Size = &v
	}
	if f.bool() {
		v := f.decimalValue()
		tr.Total = &v
	}

	tr.Origin = f.stringValue()
	if f.bool() {
		v := f.stringValue()
		tr.Location = &v
	}
	tr.Comment = f.stringValue()

	tr.Mode = f.modeValue()
	if f.bool() {
		v := f.phaseValue()
		tr.Phase = &v
	}

	tr.Seq = StrInt64(f.int64())
	if f.bool() {
		v := StrInt64(f.int64())
		tr.RecvNs = &v
	}

	switch f.sliceState() {
	case sliceEmpty:
		tr.Tags = Tags{}
	case slicePopulated:
		n := int(f.byte() % 4)
		for range n {
			tr.Tags = append(tr.Tags, f.stringValue())
		}
	}

	if f.bool() {
		n := int(f.byte() % 4)
		ids := make([]uuid.UUID, n)
		for i := range ids {
			ids[i] = f.uuidValue()
		}
		tr.Parents = NewIDList(ids...)
	}

	tr.Meta = EventMeta{Internal: f.bool()}
	if f.bool() {
		v := f.stringValue()
		tr.Meta.Note = &v
	}
	if f.bool() {
		v := f.decimalValue()
		tr.Meta.Cost = &v
	}
	if f.bool() {
		tr.MetaPtr = &EventMeta{Internal: f.bool()}
		if f.bool() {
			v := f.decimalValue()
			tr.MetaPtr.Cost = &v
		}
	}

	switch f.sliceState() {
	case sliceEmpty:
		tr.Items = []EventLeg{}
	case slicePopulated:
		n := int(f.byte() % 4)
		for range n {
			tr.Items = append(tr.Items, fillEventLeg(f))
		}
	}
	switch f.sliceState() {
	case sliceEmpty:
		tr.PtrItems = []*EventLeg{}
	case slicePopulated:
		n := int(f.byte() % 4)
		for range n {
			if f.bool() {
				tr.PtrItems = append(tr.PtrItems, nil)
				continue
			}
			leg := fillEventLeg(f)
			tr.PtrItems = append(tr.PtrItems, &leg)
		}
	}
	switch f.sliceState() {
	case sliceEmpty:
		tr.Names = []string{}
	case slicePopulated:
		n := int(f.byte() % 4)
		for range n {
			tr.Names = append(tr.Names, f.stringValue())
		}
	}

	tr.Count = int(f.int64())
	tr.Shard = int32(f.int64())
	tr.Flags = uint16(f.uint64())
	tr.Weight = f.float64Value()
	tr.Ratio32 = f.float32Value()
	tr.Timeout = time.Duration(f.int64())

	tr.PrimaryDate = YMD{int(f.byte()), int(f.byte() % 13), int(f.byte() % 32)}
	if f.bool() {
		v := YMD{int(f.byte()), int(f.byte() % 13), int(f.byte() % 32)}
		tr.SecondDate = &v
	}

	// Typed map fields (Amounts, Bag) are filled with at most ONE
	// entry: both easyjson and zerojson encode typed maps in Go range
	// order, which is nondeterministic between two separate iterations
	// of the same map, so a multi-entry map can never be byte-compared
	// across two independent encoders (the same reason event_codec_test.go's
	// Event fixture keeps Amounts single-entry — found the hard way by
	// this very fuzzer). Multi-entry map correctness is covered by the
	// DeepEqual round-trip tests, where order doesn't matter. (Extra —
	// an `any` field — is exempt: interface values fall back to
	// encoding/json in both codecs, which sorts map keys.)
	switch f.sliceState() {
	case sliceEmpty:
		tr.Amounts = map[string]decimal.Decimal{}
	case slicePopulated:
		tr.Amounts = map[string]decimal.Decimal{f.stringValue(): f.decimalValue()}
	}

	if f.bool() {
		tr.Extra = f.stringValue()
	}
	switch f.sliceState() {
	case sliceEmpty:
		tr.Bag = map[string]any{}
	case slicePopulated:
		tr.Bag = map[string]any{f.stringValue(): f.stringValue()}
	}
	switch f.sliceState() {
	case sliceEmpty:
		tr.AnyList = []any{}
	case slicePopulated:
		n := int(f.byte() % 4)
		for range n {
			tr.AnyList = append(tr.AnyList, f.stringValue())
		}
	}

	tr.CreatedBy = f.stringValue()
	tr.Version = int(f.int64())

	tr.ID = f.uuidValue()
	tr.Active = f.bool()

	return tr
}

// FuzzEventValueSpaceEncode is the value-space encode fuzzer proper:
// zerojson's AppendZJSON is compared against EventEJ's real
// easyjson-generated MarshalJSON, byte-for-byte on success, and for
// error-agreement otherwise (both must agree exactly when a NaN/Inf
// float makes the value unencodable — see writer.go's AppendFloat64).
// On success, zerojson's own output is decoded back and DeepEqual-ed
// against a stdlib decode of the same bytes (stdlib being decode's
// primary oracle per this project's contract).
func FuzzEventValueSpaceEncode(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	for i := range byte(200) {
		f.Add([]byte{i, i, i, i})
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tr := fillEvent(data)
		ej := EventEJ(tr)

		wantEJ, errEJ := ej.MarshalJSON()
		gotZJ, errZJ := tr.AppendZJSON(nil)

		if (errEJ != nil) != (errZJ != nil) {
			t.Fatalf("error-agreement mismatch: easyjson err=%v zerojson err=%v\nvalue: %+v", errEJ, errZJ, tr)
		}
		if errEJ != nil {
			return // both agree the value is unencodable (NaN/Inf float) — nothing further to check
		}
		if !bytes.Equal(wantEJ, gotZJ) {
			t.Fatalf("encode mismatch\neasyjson: %s\nzerojson: %s\nvalue: %+v", wantEJ, gotZJ, tr)
		}

		var viaStd Event
		if err := json.Unmarshal(gotZJ, &viaStd); err != nil {
			t.Fatalf("stdlib rejected zerojson's own output: %v\noutput: %s", err, gotZJ)
		}
		var viaZJ Event
		if err := viaZJ.UnmarshalZJSON(gotZJ); err != nil {
			t.Fatalf("zerojson could not re-decode its own output: %v\noutput: %s", err, gotZJ)
		}
		if !reflect.DeepEqual(&viaStd, &viaZJ) {
			t.Fatalf("round-trip mismatch\nstdlib:   %+v\nzerojson: %+v\noutput: %s", &viaStd, &viaZJ, gotZJ)
		}
	})
}
