package zerojson

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAppendInterfaceMarshalError hits AppendInterface's json.Marshal
// error path with a value encoding/json cannot marshal.
func TestAppendInterfaceMarshalError(t *testing.T) {
	dst := []byte("PFX")
	got, err := AppendInterface(dst, make(chan int))
	if err == nil {
		t.Fatal("AppendInterface(chan) returned nil error")
	}
	// On error the returned buffer is unchanged (the prefix, no appended value).
	if string(got) != "PFX" {
		t.Fatalf("AppendInterface on error mutated dst: %q", got)
	}
	// Sanity: the same value fails json.Marshal directly (the analogue).
	if _, e := json.Marshal(make(chan int)); e == nil { //nolint:staticcheck // SA1026: deliberately exercise unsupported-type rejection.
		t.Fatal("json.Marshal(chan) unexpectedly succeeded")
	}
}

type ifaceJSON struct {
	out   string
	err   error
	calls *int
}

func (m ifaceJSON) MarshalJSON() ([]byte, error) {
	if m.calls != nil {
		*m.calls++
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.out == "<nil>" {
		return nil, nil
	}
	return []byte(m.out), nil
}

type ifaceJSONPtr struct{ out string }

func (m *ifaceJSONPtr) MarshalJSON() ([]byte, error) { return []byte(m.out), nil }

type ifaceText struct {
	out   string
	err   error
	calls *int
}

func (m ifaceText) MarshalText() ([]byte, error) {
	if m.calls != nil {
		*m.calls++
	}
	if m.err != nil {
		return nil, m.err
	}
	return []byte(m.out), nil
}

type ifaceTextPtr struct{ out string }

func (m *ifaceTextPtr) MarshalText() ([]byte, error) { return []byte(m.out), nil }

type ifaceBoth struct{}

func (ifaceBoth) MarshalJSON() ([]byte, error) { return []byte(`"json"`), nil }
func (ifaceBoth) MarshalText() ([]byte, error) { return []byte("text"), nil }

type ifaceNamedString string

// Named kinds without encoding methods take AppendInterface's kind path;
// the pointer-method and AppendText types must not.
type (
	ifaceNamedBool    bool
	ifaceNamedInt     int16
	ifaceNamedUint    uint32
	ifaceNamedFloat   float32
	ifaceNamedNumber  json.Number
	ifacePtrJSONInt   int
	ifacePtrTextFloat float64
	ifaceAppendText   int
)

func (*ifacePtrJSONInt) MarshalJSON() ([]byte, error)   { return []byte(`"ptr json"`), nil }
func (*ifacePtrTextFloat) MarshalText() ([]byte, error) { return []byte("ptr text"), nil }
func (ifaceAppendText) AppendText(b []byte) ([]byte, error) {
	return append(b, "append text"...), nil
}

// AppendText outranks MarshalText once encoding/json is built on
// encoding/json/v2, which calls a pointer method only through a pointer.
type (
	ifaceTextAppend    int
	ifaceTextAppendPtr int
)

func (ifaceTextAppend) MarshalText() ([]byte, error) { return []byte("marshal text"), nil }
func (ifaceTextAppend) AppendText(b []byte) ([]byte, error) {
	return append(b, "append text"...), nil
}
func (ifaceTextAppendPtr) MarshalText() ([]byte, error) { return []byte("marshal text"), nil }
func (*ifaceTextAppendPtr) AppendText(b []byte) ([]byte, error) {
	return append(b, "pointer append text"...), nil
}

func appendInterfaceCorpus() []any {
	const bs = `\`
	lineSep, eAcute, bad := "\xe2\x80\xa8", "\xc3\xa9", "\xff"
	jsonOuts := []string{
		`"187.25"`, `123`, `1.50`, `1e2`, `1E+2`, `-0`, `true`, `null`, `[]`, `{}`,
		`{"a":1,"b":[1,2,{"c":null}]}`, `{"a":1,"a":2}`, ` "x" `, `{"a": 1}`, "[1,\n2]",
		`"<b>&"`, `"` + eAcute + `"`, `"` + lineSep + `"`, `"` + bad + `"`,
		`"` + bs + `u00e9"`, `"` + bs + `/"`, `"` + bs + `n"`, `"a` + bs + `"b"`,
		`[1,2`, ``, `nope`, `"open`, `{"a":1}x`, `01`, `1.`, `<nil>`,
		strings.Repeat("[", 20000) + strings.Repeat("]", 20000),
	}
	texts := []string{
		"", "abc", "2026-10-28", "a<b", "a&b", eAcute, lineSep, bad, "\b\f\n\t\r", "\x00\x1f\x7f",
		`say "hi"`, `back` + bs + `slash`, "\xed\xa0\x80",
	}
	var vs []any
	for _, o := range jsonOuts {
		vs = append(vs, ifaceJSON{out: o}, json.RawMessage(o))
	}
	for _, s := range texts {
		vs = append(vs, s, ifaceNamedString(s), ifaceText{out: s}, &ifaceTextPtr{out: s})
	}
	boom := errors.New("boom")
	return append(vs,
		nil, true, false, 0, -7, uint8(9), 1.5, 1e21, 1e-7, float32(0.1), math.NaN(), math.Inf(1),
		json.Number("12"), json.Number("x"), []byte("bytes"), [2]int{1, 2}, struct{ A int }{1},
		map[string]any{"b": 1, "a": ifaceText{out: "<t>"}, "c": ifaceJSON{out: `{"z": 1}`}},
		[]any{ifaceJSON{out: "1"}, "x", nil, ifaceBoth{}},
		map[string]any{"bad": ifaceJSON{err: boom}},
		ifaceBoth{}, &ifaceBoth{}, ifaceJSONPtr{out: `"value"`}, &ifaceJSONPtr{out: `"ptr"`},
		(*ifaceJSONPtr)(nil), (*ifaceJSON)(nil), (*ifaceTextPtr)(nil), (*ifaceText)(nil), (*ifaceBoth)(nil),
		ifaceJSON{err: boom}, ifaceText{err: boom}, &ifaceText{err: boom},
		time.Date(2026, 10, 28, 9, 30, 0, 123456789, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 8, 2, 30, 0, 0, time.FixedZone("EST", -5*3600)), time.Date(1, 1, 1, 0, 0, 0, 1, time.FixedZone("odd", 3600+30)),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("day", 24*3600)), time.Now(), (*time.Time)(nil),
		func() *time.Time { t := time.Date(2026, 10, 28, 0, 0, 0, 5, time.UTC); return &t }(),
		func() *time.Time { t := time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC); return &t }(),
		make(chan int), func() {},
		int8(-128), int16(-32768), int32(-1), int64(math.MinInt64), uint(7), uint16(65535), uint32(1),
		uint64(math.MaxUint64), uintptr(42), float32(1e21), float32(1e-7), float32(-0.0), math.Copysign(0, -1),
		time.Duration(1500), ifaceNamedBool(true), ifaceNamedInt(-3), ifaceNamedUint(3), ifaceNamedFloat(0.1),
		ifaceNamedFloat(float32(math.Inf(-1))), ifaceNamedNumber("12"), ifacePtrJSONInt(1), ifacePtrTextFloat(2.5),
		ifaceAppendText(3), map[string]any{"n": ifaceNamedInt(1), "f": 2.5, "i": int64(-9)},
		ifaceTextAppend(0), new(ifaceTextAppend), ifaceTextAppendPtr(0), new(ifaceTextAppendPtr), (*ifaceTextAppend)(nil),
	)
}

// TestAppendInterfaceFloatsMatchMarshal pins appendJSONFloat to
// json.Marshal across magnitudes, both bit sizes, and the exponent-form
// thresholds, for plain and named float types.
func TestAppendInterfaceFloatsMatchMarshal(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	fs := []float64{0, math.Copysign(0, -1), 1e-6, 1e21, math.SmallestNonzeroFloat64, math.MaxFloat64,
		math.Nextafter(1e-6, 0), math.Nextafter(1e21, 0), 1e-7, 123456789e-15, 0.1, 100, 1e20, 5e-324}
	for range 100000 {
		fs = append(fs, math.Float64frombits(r.Uint64()), r.NormFloat64()*math.Pow(10, float64(r.IntN(60)-30)))
	}
	check := func(v any) {
		t.Helper()
		want, wantErr := json.Marshal(v)
		got, gotErr := AppendInterface(nil, v)
		if (wantErr == nil) != (gotErr == nil) || string(got) != string(want) {
			t.Fatalf("%T(%v): json.Marshal %q, %v; AppendInterface %q, %v", v, v, want, wantErr, got, gotErr)
		}
	}
	for _, f := range fs {
		check(f)
		check(float32(f))
		check(ifaceNamedFloat(f))
	}
}

// TestAppendInterfaceMatchesMarshal pins AppendInterface to json.Marshal on
// the running toolchain: identical bytes, identical errors, and the prefix
// left untouched on failure. The fast paths must be invisible.
func TestAppendInterfaceMatchesMarshal(t *testing.T) {
	for i, v := range appendInterfaceCorpus() {
		want, wantErr := json.Marshal(v)
		got, gotErr := AppendInterface([]byte("PFX"), v)
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("%d %#v: json.Marshal err=%v, AppendInterface err=%v", i, v, wantErr, gotErr)
			continue
		}
		if wantErr != nil {
			if wantErr.Error() != gotErr.Error() || reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) {
				t.Errorf("%d %#v: errors differ\njson.Marshal:    %T %v\nAppendInterface: %T %v", i, v, wantErr, wantErr, gotErr, gotErr)
			}
			if string(got) != "PFX" {
				t.Errorf("%d %#v: failed append changed dst to %q", i, v, got)
			}
			continue
		}
		if string(got) != "PFX"+string(want) {
			t.Errorf("%d %#v:\njson.Marshal:    %q\nAppendInterface: %q", i, v, want, got[3:])
		}
	}
}

// TestAppendInterfaceCallsMethodsOnce checks that the stdlib fallback for
// output needing post-processing (or for an error) replays the method's
// result instead of calling it again.
func TestAppendInterfaceCallsMethodsOnce(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		v    func(*int) any
	}{
		{"json_verbatim", func(n *int) any { return ifaceJSON{out: `"1.5"`, calls: n} }},
		{"json_compacted", func(n *int) any { return ifaceJSON{out: `{"a": 1}`, calls: n} }},
		{"json_invalid", func(n *int) any { return ifaceJSON{out: `{`, calls: n} }},
		{"json_error", func(n *int) any { return ifaceJSON{err: boom, calls: n} }},
		{"text_verbatim", func(n *int) any { return ifaceText{out: "abc", calls: n} }},
		{"text_escaped", func(n *int) any { return ifaceText{out: "<a>", calls: n} }},
		{"text_error", func(n *int) any { return ifaceText{err: boom, calls: n} }},
	} {
		var n int
		_, _ = AppendInterface(nil, c.v(&n))
		if n != 1 {
			t.Errorf("%s: method called %d times, want 1", c.name, n)
		}
	}
}

// objectDoc renders an object of n members cycling through every
// ReadInterface shape, with nested (flat) objects of assorted sizes when
// nest is set. A non-negative dup repeats that member's key last with a new
// value, which must win.
func objectDoc(n, dup int, nest bool) []byte {
	var b strings.Builder
	b.WriteByte('{')
	member := func(i, key int) {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(key) + `":`)
		switch i % 5 {
		case 0:
			b.WriteString(`"s` + strconv.Itoa(i) + `"`)
		case 1:
			b.WriteString(strconv.Itoa(i) + ".5")
		case 2:
			b.WriteString("null")
		case 3:
			if nest {
				b.Write(objectDoc(i%(2*objectSpillSlots+3), -1, false))
			} else {
				b.WriteString("false")
			}
		default:
			b.WriteString(`[true,` + strconv.Itoa(i) + `]`)
		}
	}
	for i := range n {
		member(i, i)
	}
	if dup >= 0 && dup < n {
		member(n, dup)
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// TestReadInterfaceObjectSizes decodes objects across readObject's
// boundaries — objectSlots members on the stack, then the spill buffer, then
// direct insertion — and compares each with encoding/json.
func TestReadInterfaceObjectSizes(t *testing.T) {
	direct := 2 * objectSpillSlots
	sizes := []int{1, objectSlots - 1, objectSlots, objectSlots + 1, objectSpillSlots - 1, objectSpillSlots,
		objectSpillSlots + 1, direct - 1, direct, direct + 1, 3 * direct}
	for _, n := range sizes {
		for _, dup := range []int{-1, 0, objectSlots, objectSpillSlots, n - 1} {
			data := objectDoc(n, dup, true)
			var want any
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			l := NewLexer(data, false)
			got := l.ReadInterface()
			l.Finish()
			if err := l.Err(); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("n=%d dup=%d: err=%v\ngot  %v\nwant %v", n, dup, err, got, want)
			}
		}
	}
}

// TestReadInterfaceObjectErrorIsNil puts an out-of-range number at each
// buffering stage of an object: the result must be an untyped nil, not a
// partial map.
func TestReadInterfaceObjectErrorIsNil(t *testing.T) {
	for _, at := range []int{0, objectSlots - 1, objectSlots, objectSpillSlots - 1, objectSpillSlots, 2 * objectSpillSlots} {
		var b strings.Builder
		b.WriteByte('{')
		for i := range at + 3 {
			if i > 0 {
				b.WriteByte(',')
			}
			value := strconv.Itoa(i)
			if i == at {
				value = "1e999"
			}
			b.WriteString(`"k` + strconv.Itoa(i) + `":` + value)
		}
		b.WriteByte('}')
		l := NewLexer([]byte(b.String()), false)
		if got := l.ReadInterface(); got != nil || l.Err() == nil {
			t.Errorf("error at member %d: got %#v, err %v; want nil and an error", at, got, l.Err())
		}
	}
}
