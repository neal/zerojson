package zerojson

import (
	"encoding"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const hexDigits = "0123456789abcdef"

const smalls = "00010203040506070809" +
	"10111213141516171819" +
	"20212223242526272829" +
	"30313233343536373839" +
	"40414243444546474849" +
	"50515253545556575859" +
	"60616263646566676869" +
	"70717273747576777879" +
	"80818283848586878889" +
	"90919293949596979899"

func append2(dst []byte, v int) []byte {
	return append(dst, smalls[v*2], smalls[v*2+1])
}

func append4(dst []byte, v int) []byte {
	return append(dst, smalls[(v/100)*2], smalls[(v/100)*2+1], smalls[(v%100)*2], smalls[(v%100)*2+1])
}

// safeSet marks ASCII bytes (< 0x80) that can be copied into a JSON
// string verbatim, mirroring easyjson's jwriter default escaping: '"',
// '\\', control chars, and the HTML set '<' '>' '&' are escaped; every
// other ASCII byte passes through. Bytes >= 0x80 are never "safe" here;
// they route through needEscape's UTF-8-validating slow path.
var safeSet = func() (s [256]bool) {
	for i := range 256 {
		s[i] = i >= 0x20 && i < 0x80 && i != '"' && i != '\\' && i != '<' && i != '>' && i != '&'
	}
	return
}()

// zeroBytes reports (as a SWAR mask) which bytes of x are zero. Borrow
// propagation can set false positives above the first true match, so
// only the lowest set bit is meaningful.
func zeroBytes(x uint64) uint64 {
	return (x - 0x0101010101010101) &^ x & 0x8080808080808080
}

// needEscape returns a SWAR mask of bytes requiring escaping: control
// chars (< 0x20), '"', '\\', '<', '>', '&', or any byte with the high
// bit set (>= 0x80). High-bit bytes are flagged so AppendString can
// inspect them: a valid multi-byte rune passes through verbatim, an
// invalid UTF-8 sequence becomes replacementEscape. A byte's own bit 7
// already lands on this mask's position, so v&maskHighBit folds in with
// no shifting. Lowest set bit is the first byte needing inspection.
func needEscape(v uint64) uint64 {
	const maskHighBit = 0x8080808080808080
	return zeroBytes(v&0xE0E0E0E0E0E0E0E0) | // < 0x20 (top 3 bits clear)
		hasByte(v, '"') | hasByte(v, '\\') |
		hasByte(v, '<') | hasByte(v, '>') | hasByte(v, '&') |
		v&maskHighBit
}

func appendEscaped(dst []byte, c byte) []byte {
	switch c {
	case '"':
		return append(dst, '\\', '"')
	case '\\':
		return append(dst, '\\', '\\')
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	default:
		return append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
	}
}

// replacementEscape is the literal 6-byte escape sequence
// backslash-u-f-f-f-d (the JSON \uXXXX escape for U+FFFD, the Unicode
// replacement character) — NOT U+FFFD's own 3-byte UTF-8 encoding. Both
// encoding/json and easyjson emit exactly these 6 ASCII bytes for an
// invalid UTF-8 byte in an encoded string.
var replacementEscape = []byte{'\\', 'u', 'f', 'f', 'f', 'd'}

// AppendString appends s as a quoted, escaped JSON string. The scan for
// escape-needing bytes runs 8 bytes per step; a clean ASCII string
// reduces to one scan plus one bulk copy.
//
// A byte with the high bit set (>= 0x80) routes to the slow path
// (appendNonASCII): a valid multi-byte UTF-8 sequence passes through
// verbatim, an invalid sequence is replaced with replacementEscape. Both
// match easyjson and encoding/json.
func AppendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	if len(s) == 0 {
		return append(dst, '"')
	}
	b := stringToBytes(s)
	start := 0
	i := 0
	for i+8 <= len(b) {
		v := binary.LittleEndian.Uint64(b[i:])
		m := needEscape(v)
		if m == 0 {
			i += 8
			continue
		}
		j := i + bits.TrailingZeros64(m)>>3
		if b[j] < 0x80 {
			dst = append(dst, s[start:j]...)
			dst = appendEscaped(dst, b[j])
			start = j + 1
			i = j + 1
			continue
		}
		dst, start, i = appendNonASCII(dst, s, b, start, j)
	}
	for ; i < len(b); i++ {
		if b[i] < 0x80 {
			if safeSet[b[i]] {
				continue
			}
			dst = append(dst, s[start:i]...)
			dst = appendEscaped(dst, b[i])
			start = i + 1
			continue
		}
		var newI int
		dst, start, newI = appendNonASCII(dst, s, b, start, i)
		i = newI - 1 // the loop's own i++ accounts for the last 1
	}
	return append(append(dst, s[start:]...), '"')
}

// appendNonASCII handles the byte at b[j] (already known to have its
// high bit set): flushes s[start:j] into dst, then either passes a valid
// UTF-8 rune through verbatim, replaces an invalid one with
// replacementEscape, or escapes U+2028/U+2029. All three match easyjson
// and encoding/json; the U+2028/U+2029 line/paragraph-separator escape is
// their shared JavaScript-compatibility measure. Returns the updated dst,
// a new start (position right after what's been flushed/replaced), and
// the position to resume scanning from (j + the rune's width, or j+1 for
// an invalid byte).
func appendNonASCII(dst []byte, s string, b []byte, start, j int) (newDst []byte, newStart, resumeAt int) {
	r, size := utf8.DecodeRuneInString(s[j:])
	if r == utf8.RuneError && size == 1 {
		dst = append(dst, s[start:j]...)
		dst = append(dst, replacementEscape...)
		return dst, j + 1, j + 1
	}
	if r == '\u2028' || r == '\u2029' {
		dst = append(dst, s[start:j]...)
		dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
		return dst, j + size, j + size
	}
	// Valid multi-byte rune: leave it in the pending verbatim run (it's
	// already part of s[start:...]); just skip past it.
	_ = b
	return dst, start, j + size
}

// AppendTime appends t as a quoted RFC3339 timestamp with nanoseconds,
// byte-identical to time.Time.MarshalJSON, without the generic layout
// machinery of time.AppendFormat.
//
// A year outside [0,9999] is not representable in strict RFC 3339;
// time.Time.MarshalJSON errors and easyjson (which delegates to it)
// errors identically, so this returns that error rather than emitting a
// non-RFC3339 string.
func AppendTime(dst []byte, t time.Time) ([]byte, error) {
	year, month, day := t.Date()
	if year < 0 || year > 9999 {
		return dst, fmt.Errorf("Time.MarshalJSON: year outside of range [0,9999]")
	}
	// RFC 3339's numeric offset is ("+"/"-") HH ":" MM with HH in [0,23], so
	// a zone offset of a whole day or more (|offset| >= 24h) is not
	// representable — time.Time.MarshalJSON returns exactly this error, and
	// easyjson delegates to it. The seconds component is truncated to whole
	// minutes (offset/60), matching time.AppendFormat's RFC3339 output, so a
	// non-minute-aligned zone is not an error — only an out-of-range hour is.
	// Bounds-check the SIGNED offset before any negation: negating math.MinInt
	// stays negative, so a magnitude-then-compare check would let it slip past
	// the range test and then panic indexing append2's digit-pair table.
	_, offset := t.Zone()
	if offset <= -24*3600 || offset >= 24*3600 {
		return dst, fmt.Errorf("Time.MarshalJSON: timezone hour outside of range [0,23]")
	}
	hour, min, sec := t.Clock()

	dst = append(dst, '"')
	dst = append4(dst, year)
	dst = append(dst, '-')
	dst = append2(dst, int(month))
	dst = append(dst, '-')
	dst = append2(dst, day)
	dst = append(dst, 'T')
	dst = append2(dst, hour)
	dst = append(dst, ':')
	dst = append2(dst, min)
	dst = append(dst, ':')
	dst = append2(dst, sec)

	if ns := t.Nanosecond(); ns > 0 {
		digits := 9
		for ns%1000 == 0 {
			ns /= 1000
			digits -= 3
		}
		for ns%10 == 0 {
			ns /= 10
			digits--
		}
		var frac [10]byte
		frac[0] = '.'
		for i := digits; i >= 1; i-- {
			frac[i] = byte(ns%10) + '0'
			ns /= 10
		}
		dst = append(dst, frac[:digits+1]...)
	}

	if offset == 0 {
		dst = append(dst, 'Z')
	} else {
		// Sign and magnitude come from the offset truncated to whole minutes
		// (offset/60, toward zero), matching time.AppendFormat's RFC3339
		// output: a sub-minute offset truncates to zero minutes and is
		// emitted as "+00:00", so the SIGN must follow the truncated value,
		// not the raw seconds — a small negative offset (e.g. -30s) is
		// "+00:00" to stdlib, not "-00:00".
		zoneMin := offset / 60
		if zoneMin < 0 {
			dst = append(dst, '-')
			zoneMin = -zoneMin
		} else {
			dst = append(dst, '+')
		}
		dst = append2(dst, zoneMin/60)
		dst = append(dst, ':')
		dst = append2(dst, zoneMin%60)
	}
	return append(dst, '"'), nil
}

// AppendUUIDBytes appends u (a raw 16-byte UUID) in canonical quoted form,
// byte-identical to uuid.UUID.MarshalText, without allocating. Generated
// code casts a domain UUID type to [16]byte at the call site (e.g.
// zerojson.AppendUUIDBytes(dst, [16]byte(v.Field))), keeping the runtime
// itself free of any UUID dependency.
func AppendUUIDBytes(dst []byte, u [16]byte) []byte {
	var s [38]byte
	s[0] = '"'
	j := 1
	for i, b := range u {
		switch i {
		case 4, 6, 8, 10:
			s[j] = '-'
			j++
		}
		s[j] = hexDigits[b>>4]
		s[j+1] = hexDigits[b&0xF]
		j += 2
	}
	s[37] = '"'
	return append(dst, s[:]...)
}

// AppendQuotedInt64 appends v as a quoted integer, matching the common
// string-encoded Int64 convention used to survive float64-based decoders.
func AppendQuotedInt64(dst []byte, v int64) []byte {
	dst = append(dst, '"')
	dst = strconv.AppendInt(dst, v, 10)
	return append(dst, '"')
}

// AppendQuotedUint64 appends v as a quoted unsigned integer, for
// json:",string" fields (stdlib's "quoted" convention).
func AppendQuotedUint64(dst []byte, v uint64) []byte {
	dst = append(dst, '"')
	dst = strconv.AppendUint(dst, v, 10)
	return append(dst, '"')
}

// AppendBool appends the JSON boolean literal.
func AppendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, "true"...)
	}
	return append(dst, "false"...)
}

// AppendFloat64 appends f formatted exactly as easyjson's
// jwriter.Writer.Float64 does: strconv.AppendFloat(dst, f, 'g', -1, 64),
// with no post-processing. Byte-identity with easyjson is the encode
// contract; this is a deliberate divergence from encoding/json's float
// format selection (its manual abs<1e-6||abs>=1e21 'e'-vs-'f' threshold),
// which disagrees with strconv's raw 'g' format on many values.
//
// NaN and Infinity are not valid JSON number tokens: encoding/json and
// easyjson both refuse to encode them. AppendFloat64 matches both — dst
// is returned unchanged with a non-nil error, which the generated
// AppendZJSON propagates like any other encode error.
func AppendFloat64(dst []byte, f float64) ([]byte, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return dst, fmt.Errorf("json: unsupported value: %s", strconv.FormatFloat(f, 'g', -1, 64))
	}
	return strconv.AppendFloat(dst, f, 'g', -1, 64), nil
}

// AppendFloat32 appends f formatted exactly as easyjson's
// jwriter.Writer.Float32 does (strconv.AppendFloat with the value widened
// to float64 but the 32-bit shortest-round-trip precision request). See
// AppendFloat64's doc for the format-matching rationale and the
// NaN/Infinity error contract.
func AppendFloat32(dst []byte, f float32) ([]byte, error) {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return dst, fmt.Errorf("json: unsupported value: %s", strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	return strconv.AppendFloat(dst, float64(f), 'g', -1, 32), nil
}

// AppendUint64 appends v as a bare unsigned integer.
func AppendUint64(dst []byte, v uint64) []byte {
	return strconv.AppendUint(dst, v, 10)
}

// AppendInt64 appends v as a bare signed integer.
func AppendInt64(dst []byte, v int64) []byte {
	return strconv.AppendInt(dst, v, 10)
}

// AppendStringBytes appends b as a quoted, escaped JSON string (for
// TextMarshaler output).
func AppendStringBytes(dst, b []byte) []byte {
	return AppendString(dst, bytesToString(b))
}

// SizeHint sizes the buffer an encoding starts with from the encodings
// before it, so a steady stream of similar values encodes in one allocation
// instead of growing a buffer from empty. Generated MarshalJSON and
// MarshalEasyJSON wrappers keep one per type. The zero value is ready to
// use, and a SizeHint is safe for concurrent use.
type SizeHint struct {
	n atomic.Int64
}

// maxSizeHint caps the room a SizeHint asks for, so one huge encoding does
// not make later ones allocate as much.
const maxSizeHint = 64 << 10

// Buffer returns an empty buffer with room for the encoding the hint
// expects, or nil before the first Observe.
func (h *SizeHint) Buffer() []byte {
	if n := h.n.Load(); n > 0 {
		return make([]byte, 0, n)
	}
	return nil
}

// Observe records the length of an encoding. The hint moves to that length
// plus a quarter, to absorb small variation, when the encoding outgrew the
// hint or used less than half of it, and is otherwise left alone so steady
// traffic does not write to it.
func (h *SizeHint) Observe(n int) {
	cur := h.n.Load()
	want := min(int64(n)+int64(n)/4, maxSizeHint)
	if int64(n) > cur && want > cur || want < cur/2 {
		h.n.Store(want)
	}
}

// AppendInterface appends an arbitrary value exactly as json.Marshal encodes
// it, matching easyjson's handling of interface{} fields (which fall back to
// encoding/json). Nested maps therefore have sorted keys, unlike the
// range-order maps zerojson emits for statically-typed map fields.
//
// Common values skip json.Marshal's reflection: nil, bools, numbers,
// strings that need no escaping, the same kinds under a named type without
// encoding methods, and json.Marshaler/encoding.TextMarshaler values whose
// output encoding/json would copy unchanged. A value with a method that
// encoding/json ranks above those (MarshalJSONTo or AppendText, once it is
// built on encoding/json/v2) is left to json.Marshal. Each method is called
// once.
// Output that encoding/json would compact, escape, or reject is handed back
// to json.Marshal through a stand-in that replays the method's result, so
// the bytes and the error are json.Marshal's for v.
func AppendInterface(dst []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		return AppendBool(dst, x), nil
	case string:
		if verbatimString(x) {
			dst = append(dst, '"')
			dst = append(dst, x...)
			return append(dst, '"'), nil
		}
	case float64:
		return appendJSONFloat(dst, v, x, 64)
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case time.Time:
		// encoding/json/v2 formats times itself rather than calling
		// MarshalJSON; AppendTime matches both, and the rare unencodable
		// time takes encoding/json's own error.
		if b, err := AppendTime(dst, x); err == nil {
			return b, nil
		}
		return appendStdlib(dst, v)
	case *time.Time:
		if x == nil {
			return append(dst, "null"...), nil
		}
		if b, err := AppendTime(dst, *x); err == nil {
			return b, nil
		}
		return appendStdlib(dst, v)
	case json.Marshaler:
		if hasMarshalJSONTo(v) {
			return appendStdlib(dst, v)
		}
		if isNilPointer(v) {
			return append(dst, "null"...), nil
		}
		b, err := x.MarshalJSON()
		if err == nil && verbatimJSON(b) {
			return append(dst, b...), nil
		}
		return appendReplayed(dst, v, replayMarshaler{b, err})
	case encoding.TextMarshaler:
		if hasMarshalJSONTo(v) || hasAppendText(v) {
			return appendStdlib(dst, v)
		}
		if isNilPointer(v) {
			return append(dst, "null"...), nil
		}
		b, err := x.MarshalText()
		if err == nil && verbatimString(b) {
			dst = append(dst, '"')
			dst = append(dst, b...)
			return append(dst, '"'), nil
		}
		return appendReplayed(dst, v, replayTextMarshaler{b, err})
	}
	return appendKind(dst, v)
}

// appendKind encodes a bool, number, or string of any other type as
// encoding/json does, when encoding/json would format it by kind alone:
// the cases above have ruled out the value's own encoding methods, and
// plainKind rules out those on *T. Everything else is left to json.Marshal.
func appendKind(dst []byte, v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	switch k := rv.Kind(); {
	case k == reflect.Invalid, k > reflect.Float64 && k != reflect.String, !plainKind(rv.Type()):
		return appendStdlib(dst, v)
	case k == reflect.Bool:
		return AppendBool(dst, rv.Bool()), nil
	case k <= reflect.Int64:
		return strconv.AppendInt(dst, rv.Int(), 10), nil
	case k <= reflect.Uintptr:
		return strconv.AppendUint(dst, rv.Uint(), 10), nil
	case k == reflect.Float32:
		return appendJSONFloat(dst, v, rv.Float(), 32)
	case k == reflect.Float64:
		return appendJSONFloat(dst, v, rv.Float(), 64)
	}
	if s := rv.String(); verbatimString(s) {
		dst = append(dst, '"')
		dst = append(dst, s...)
		return append(dst, '"'), nil
	}
	return appendStdlib(dst, v)
}

// plainKinds caches plainKind by type.
var plainKinds sync.Map // reflect.Type -> bool

// plainKind reports whether encoding/json formats a bool, number, or
// string of type t by its kind alone: t is not json.Number (which has no
// encoding method before encoding/json is built on encoding/json/v2), and
// neither t nor *t has a method either encoding/json would call instead.
func plainKind(t reflect.Type) bool {
	if plain, ok := plainKinds.Load(t); ok {
		return plain.(bool)
	}
	plain := t != reflect.TypeFor[json.Number]()
	pt := reflect.PointerTo(t)
	for _, name := range [...]string{"MarshalJSON", "MarshalJSONTo", "MarshalText", "AppendText"} {
		if _, ok := pt.MethodByName(name); ok {
			plain = false
		}
	}
	plainKinds.Store(t, plain)
	return plain
}

// appendJSONFloat appends f as encoding/json formats a float of the given
// bit size: the shortest digits that round-trip, in exponent form only
// below 1e-6 or from 1e21, with a one-digit negative exponent unpadded.
// NaN and the infinities are left to json.Marshal(v) for its error.
func appendJSONFloat(dst []byte, v any, f float64, bits int) ([]byte, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return appendStdlib(dst, v)
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (bits == 64 && (abs < 1e-6 || abs >= 1e21) ||
		bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21)) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, bits)
	if n := len(dst); format == 'e' && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
		// e-07 becomes e-7.
		dst[n-2] = dst[n-1]
		dst = dst[:n-1]
	}
	return dst, nil
}

func appendStdlib(dst []byte, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return dst, err
	}
	return append(dst, b...), nil
}

// appendReplayed appends json.Marshal(standIn), where standIn replays the
// result of v's own method. A Marshaler error names standIn's type, so it is
// renamed to the type json.Marshal(v) would name: v's type, or with an
// encoding/json built on encoding/json/v2, a pointer to v's value type.
func appendReplayed(dst []byte, v, standIn any) ([]byte, error) {
	b, err := json.Marshal(standIn)
	if err != nil {
		if me, ok := err.(*json.MarshalerError); ok {
			vt, st := reflect.TypeOf(v), reflect.TypeOf(standIn)
			switch me.Type {
			case st:
			case reflect.PointerTo(st):
				if vt.Kind() != reflect.Pointer {
					vt = reflect.PointerTo(vt)
				}
			default:
				return dst, err
			}
			retyped := *me
			retyped.Type = vt
			err = &retyped
		}
		return dst, err
	}
	return append(dst, b...), nil
}

// replayMarshaler and replayTextMarshaler return a result the user's method
// already produced, so encoding/json post-processes it without calling the
// method a second time.
type replayMarshaler struct {
	b   []byte
	err error
}

func (r replayMarshaler) MarshalJSON() ([]byte, error) { return r.b, r.err }

type replayTextMarshaler struct {
	b   []byte
	err error
}

func (r replayTextMarshaler) MarshalText() ([]byte, error) { return r.b, r.err }

// isNilPointer reports whether v holds a nil pointer, which encoding/json
// encodes as null without calling its Marshaler or TextMarshaler.
func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// verbatimString reports whether encoding/json would quote s without
// escaping anything: printable ASCII other than '"', '\\', and the HTML set.
func verbatimString[T string | []byte](s T) bool {
	for i := 0; i < len(s); i++ {
		if !safeSet[s[i]] {
			return false
		}
	}
	return true
}

// verbatimJSON reports whether encoding/json would copy Marshaler output b
// unchanged: valid, compact, ASCII, with no HTML-sensitive bytes and no
// string escapes (left to encoding/json so its spelling stays authoritative).
func verbatimJSON(b []byte) bool {
	inString := false
	for _, c := range b {
		if c >= utf8.RuneSelf || c == '<' || c == '>' || c == '&' || c == '\\' {
			return false
		}
		switch {
		case c == '"':
			inString = !inString
		case !inString && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			return false
		}
	}
	return Valid(b) == nil
}
