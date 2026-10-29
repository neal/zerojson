// Package codec exercises every field kind the generator supports and
// validates the emitted codec against encoding/json as an oracle.
package codec

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	decimal "github.com/neal/zerojson/conformance/internal/testleaf"

	"github.com/neal/zerojson"
)

// EventLeg and EventMeta are auto-discovered from Event's fields.
//go:generate go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types Event,RawHolder,OmitZeroCases,StringOptCases,AppenderCases,MapFieldsLike,EventLeg,EmbedMid,EventProjection,InteractionOmitZero,MapPointerMethods,ReuseSemantics,OrderedSideEffects,PoolAppenderHolder,BytesCases,EventList,LegPtrList,LegIndex,PointerMethodIndex,PointerQuotedIndex,RawList,RawIndex,RawHolderList,DecimalList,PointerCollections,NumberCases,NullCases,FoldNames,FoldUnicodeNames,OrderedLookup,IgnoredOnly -leaf github.com/neal/zerojson/conformance/internal/testleaf.Decimal=github.com/neal/zerojson/conformance/internal/testleaf.Append,github.com/neal/zerojson/conformance/internal/testleaf.Read -pool -out zerojson_gen.go -tests zerojson_gen_fuzz_test.go

// EventList, LegPtrList, and LegIndex exercise the generator's named
// slice/map ROOT support (root.go) — a top-level shape easyjson handles.
// Each element/value struct keeps its own ordered fast path; the root loop
// is the plain array/object protocol. Encode: nil -> null, non-nil empty ->
// [] or {}; decode: null -> nil, [] / {} -> non-nil empty — the same
// null/empty rules the slice/map FIELD code establishes (see
// root_types_test.go). LegPtrList covers []*T with null elements; LegIndex
// covers a string-keyed map of struct values (Go range order on encode,
// matching easyjson — encoding/json would sort).
type EventList []Event

// LegPtrList is a []*T root: null elements round-trip as nil pointers.
type LegPtrList []*EventLeg

// LegIndex is a map[string]T root with struct values.
type LegIndex map[string]EventLeg

// RawList and DecimalList are named-slice ROOTS whose element is a custom
// LEAF type (zerojson.Raw and the conformance-only Decimal respectively), not a
// struct. The root emit passes each element cell to the leaf's read func,
// which takes *T — so the element expression must be &cell[idx], not
// cell[idx]. Emitting the value form produced uncompilable code (a T where
// *T was required); these roots pin that the leaf-element root path
// compiles and round-trips. RawList exercises the always-registered
// built-in Raw leaf; DecimalList exercises an explicitly registered -leaf.
type RawList []zerojson.Raw

// RawIndex and RawHolderList keep direct and nested Raw roots in every
// dynamically discovered fuzz campaign, alongside the flat RawList.
type RawIndex map[string]zerojson.Raw
type RawHolderList []RawHolder

// DecimalList is a []decimal.Decimal leaf-element root.
type DecimalList []decimal.Decimal

// Mode is an enum: named string type with declared constants.
type Mode string

const (
	ModeUp   Mode = "up"
	ModeDown Mode = "down"
)

// Phase is an enum used through a pointer field.
type Phase string

const (
	PhaseNew    Phase = "new"
	PhaseClosed Phase = "done"
)

// StrInt64 follows the common quoted-int64 convention: an int64 that
// marshals as a quoted string to survive float64-based decoders.
type StrInt64 int64

func (i StrInt64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

func (StrInt64) ZeroJSONQuotedInt64() {}

func (i *StrInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*i = StrInt64(v)
	return nil
}

// Tags is a custom-marshaler type (slice-shaped): comma-joined string.
type Tags []string

func (t Tags) Value() (driver.Value, error) { return strings.Join(t, ","), nil }

func (t Tags) MarshalJSON() ([]byte, error) {
	return json.Marshal(strings.Join(t, ","))
}

func (t *Tags) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*t = nil
		return nil
	}
	*t = strings.Split(s, ",")
	return nil
}

// IDList is a custom-marshaler type (struct-shaped).
type IDList struct {
	items []uuid.UUID
}

func NewIDList(items ...uuid.UUID) IDList { return IDList{items} }

func (l IDList) MarshalJSON() ([]byte, error) {
	if l.items == nil {
		return []byte("null"), nil
	}
	parts := make([]string, len(l.items))
	for i, u := range l.items {
		parts[i] = u.String()
	}
	return json.Marshal("{" + strings.Join(parts, ",") + "}")
}

func (l *IDList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	s = strings.Trim(s, "{}")
	if s == "" {
		l.items = nil
		return nil
	}
	for _, p := range strings.Split(s, ",") {
		u, err := uuid.Parse(p)
		if err != nil {
			return fmt.Errorf("idlist: %w", err)
		}
		l.items = append(l.items, u)
	}
	return nil
}

// YMD is a TextMarshaler (MarshalText/UnmarshalText, no MarshalJSON),
// like civil.Date: serializes as a quoted string.
type YMD struct {
	Y, M, D int
}

func (d YMD) MarshalText() ([]byte, error) {
	return fmt.Appendf(nil, "%04d-%02d-%02d", d.Y, d.M, d.D), nil
}

func (d *YMD) UnmarshalText(b []byte) error {
	_, err := fmt.Sscanf(string(b), "%04d-%02d-%02d", &d.Y, &d.M, &d.D)
	return err
}

// EventMeta is a nested generated struct.
type EventMeta struct {
	Note     *string          `json:"note,omitempty"`
	Cost     *decimal.Decimal `json:"cost,omitempty"`
	Internal bool             `json:"internal,omitempty"`
}

// EventLeg is a slice element type. It's also listed explicitly in
// -types (get its own EventLegDecoder) because, unlike Event itself
// (which deliberately exercises every field kind including several
// non-fast-path ones — Marshaler delegation, interface{} fallback — for
// coverage), every one of EventLeg's fields is a direct leaf fast path.
// allocation_test.go uses it for the true zero-allocation budget assertions
// behind the README's all-leaf benchmark numbers.
type EventLeg struct {
	Ratio decimal.Decimal `json:"ratio,omitempty"`
	Label string          `json:"label,omitempty"`
	Mode  Mode            `json:"mode,omitempty"`
}

// Audit is embedded untagged into Event: its fields are promoted.
type Audit struct {
	CreatedBy string `json:"created_by,omitempty"`
	Version   int    `json:"version,omitempty"`
}

// Event covers every field kind: leaf values and pointers, enums (value
// and pointer), quoted ints, custom marshalers (slice- and struct-shaped),
// nested structs (value and pointer), slices of structs, interning tags,
// omitempty and always-emit variants.
type Event struct {
	CreatedAt time.Time  `json:"created_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`

	Amount decimal.Decimal  `json:"amount,omitempty"`
	Size   *decimal.Decimal `json:"size,omitempty"`
	Total  *decimal.Decimal `json:"total"` // no omitempty: null when nil

	Origin   string  `json:"origin,omitempty" zerojson:"intern"`
	Location *string `json:"location,omitempty"`
	Comment  string  `json:"comment,omitempty"`

	Mode  Mode   `json:"mode,omitempty"`
	Phase *Phase `json:"phase,omitempty"`

	Seq     StrInt64  `json:"seq,omitempty"`
	RecvNs  *StrInt64 `json:"recv_ns,omitempty"`
	Tags    Tags      `json:"tags,omitempty"`
	Parents IDList    `json:"parents,omitempty"`

	Meta    EventMeta  `json:"meta,omitempty"`
	MetaPtr *EventMeta `json:"meta_ptr,omitempty"`

	Items    []EventLeg  `json:"items,omitempty"`
	PtrItems []*EventLeg `json:"ptr_items,omitempty"`
	Names    []string    `json:"names,omitempty"`

	Count   int           `json:"count,omitempty"`
	Shard   int32         `json:"shard,omitempty"`
	Flags   uint16        `json:"flags,omitempty"`
	Weight  float64       `json:"weight,omitempty"`
	Ratio32 float32       `json:"ratio32,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`

	PrimaryDate YMD  `json:"primary_date"`
	SecondDate  *YMD `json:"second_date,omitempty"`

	// Single-entry in byte-identity tests: zerojson uses range order
	// while stdlib sorts, so multi-entry equality is covered by the
	// round-trip test instead.
	Amounts map[string]decimal.Decimal `json:"amounts,omitempty" zerojson:"intern"`

	Extra   any            `json:"extra,omitempty"`
	Bag     map[string]any `json:"bag,omitempty"`
	AnyList []any          `json:"any_list,omitempty"`

	ID     uuid.UUID `json:"id,omitempty"`
	Active bool      `json:"active,omitempty"`

	// Untagged embed: CreatedBy/Version promoted into Event. Declared
	// LAST deliberately: zerojson (like encoding/json) emits promoted
	// fields at the embed's declaration position, but easyjson emits
	// them after all of the struct's direct fields regardless of where
	// the embed is declared — a genuine easyjson-vs-stdlib divergence
	// (zerojson sides with stdlib; see embed_order_test.go for the
	// empirical pin against real easyjson-generated output, and the
	// README's compatibility contract). With the embed declared last,
	// the two conventions coincide, letting the value-space fuzzer
	// (value_fuzz_test.go) byte-compare Event against its easyjson
	// oracle without tripping over the ordering policy on every input.
	Audit
}

// EventProjection declares only two of Event's ~30 wire fields — Origin
// (6th declared) and Active (32nd, near the end) — in the same relative
// order they appear on Event itself (the README's "declaration order
// matters for staying on the fast path" guidance), routing everything
// between them (nested structs, slices, maps, an interface{} fallback
// field, Marshaler-delegated fields) through the skip machinery
// unexamined. This is the projection/passthrough pattern documented in
// the README ("declaring only the wire fields a reader needs"); used by
// decode_implies_valid_test.go, which needs a type that exercises Skip
// far more heavily than a single injected unknown field would, to prove
// Skip's grammar strictness holds under a realistic projection-shaped
// decode too, not just a hand-picked single case.
type EventProjection struct {
	Origin string `json:"origin,omitempty"`
	Active bool   `json:"active,omitempty"`
}

// EmbedMid declares an untagged embed in the MIDDLE of the struct — the
// shape that exposes easyjson's promoted-field ordering divergence from
// encoding/json (easyjson emits promoted fields after all direct fields,
// regardless of declaration position; stdlib and zerojson emit them at
// the embed's position). EmbedMidEJ (easyjson_oracle.go) is its
// easyjson-generated twin; embed_order_test.go pins all three encoders
// against each other: zerojson == stdlib != easyjson, byte-for-byte.
type EmbedMid struct {
	First string `json:"first,omitempty"`
	Audit        // promoted here by stdlib/zerojson; moved to the end by easyjson
	Last  string `json:"last,omitempty"`
}

// RawHolder exercises zerojson.Raw: a passthrough/projection field
// (Set, unconditionally emitted) alongside a pointer variant (Where) to
// exercise null-clears-pointer handling, and an ordinary field (Table)
// so Raw isn't the only thing on the wire.
type RawHolder struct {
	Table string        `json:"table,omitempty"`
	Set   zerojson.Raw  `json:"set"`
	Where *zerojson.Raw `json:"where,omitempty"`
}

// OmitZeroCases exercises the omitzero tag option (Go 1.24 stdlib
// semantics): an IsZero() bool method is honored when the type has one
// (time.Time, decimal.Decimal); pointers/slices/maps/interfaces gate on
// nil rather than emptiness — the one place omitzero diverges from
// omitempty, exercised by EmptySlice below (non-nil but len==0: omitzero
// still emits it, unlike omitempty); everything else gates on the
// ordinary zero value. Diffed against encoding/json (which implements
// the identical option since Go 1.24) as the oracle in tagpolicy_test.go
// — not easyjson, which ignores omitzero entirely (see README).
type OmitZeroCases struct {
	When       time.Time       `json:"when,omitzero"`
	Amount     decimal.Decimal `json:"amount,omitzero"`
	ID         uuid.UUID       `json:"id,omitzero"`
	Count      int             `json:"count,omitzero"`
	Name       string          `json:"name,omitzero"`
	Ptr        *int            `json:"ptr,omitzero"`
	NilSlice   []string        `json:"nil_slice,omitzero"`
	EmptySlice []string        `json:"empty_slice,omitzero"`
}

// StringOptCases exercises the ",string" tag option, implemented for
// int/uint fields: encodes quoted and requires a quoted JSON token on decode.
// Diffed against encoding/json, which implements the identical
// convention, in tagpolicy_test.go.
type StringOptCases struct {
	Count  uint32 `json:"count,string"`
	Amount int64  `json:"amount,string"`
}

// Cents is a hand-written fixed-point money type (whole cents, formatted
// as a quoted decimal string, e.g. Cents(12345) -> "123.45") that
// implements zerojson's extension pair directly: AppendZJSON/ReadZJSON
// (see interface.go), joining the zero-alloc encode path without a -leaf
// generator registration. It also implements the standard
// MarshalJSON/UnmarshalJSON — used here as the byte-identity oracle and,
// via the -tests fuzzer's json.Unmarshal/json.Marshal calls, as the
// generic encoding/json path — and the generator's detection gives
// AppendZJSON/ReadZJSON precedence over them (a type offering both gets
// the fast path). Both pairs share one parse/format implementation below
// so they can never disagree with each other.
type Cents int64

func (c Cents) MarshalJSON() ([]byte, error) {
	return appendCentsQuoted(nil, int64(c)), nil
}

func (c *Cents) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	v, ok := parseCents(b)
	if !ok {
		return fmt.Errorf("cents: invalid %q", b)
	}
	*c = Cents(v)
	return nil
}

// AppendZJSON appends c's quoted decimal form to dst, byte-identical to
// MarshalJSON, without allocating.
func (c Cents) AppendZJSON(dst []byte) ([]byte, error) {
	return appendCentsQuoted(dst, int64(c)), nil
}

// ReadZJSON parses c's quoted decimal form from l, reporting errors via
// l.Fail — the same convention every other leaf Read function uses (see
// README's "Custom leaf types").
func (c *Cents) ReadZJSON(l *zerojson.Lexer) {
	b := l.ReadNumericBytes()
	if l.Err() != nil {
		return
	}
	v, ok := parseCents(b)
	if !ok {
		l.Fail("cents %q: invalid", b)
		return
	}
	*c = Cents(v)
}

func appendCentsQuoted(dst []byte, v int64) []byte {
	dst = append(dst, '"')
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	dst = strconv.AppendInt(dst, v/100, 10)
	dst = append(dst, '.')
	frac := v % 100
	dst = append(dst, byte('0'+frac/10), byte('0'+frac%10))
	return append(dst, '"')
}

// parseCents parses a plain (unquoted) decimal string — optional '-',
// digits, optional '.' plus 1-2 fractional digits — into whole cents.
func parseCents(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	i := 0
	neg := false
	if b[0] == '-' {
		neg = true
		i++
	}
	start := i
	var ip int64
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		ip = ip*10 + int64(b[i]-'0')
		i++
	}
	if i == start {
		return 0, false
	}
	frac := int64(0)
	if i < len(b) && b[i] == '.' {
		i++
		fracStart := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' && i-fracStart < 2 {
			frac = frac*10 + int64(b[i]-'0')
			i++
		}
		if i == fracStart {
			return 0, false
		}
		if i-fracStart == 1 {
			frac *= 10
		}
	}
	if i != len(b) {
		return 0, false
	}
	v := ip*100 + frac
	if neg {
		v = -v
	}
	return v, true
}

// AppenderCases exercises the Appender/Reader extension pair as ordinary
// generated fields: a value field and a pointer field (null clears it,
// matching every other pointer kind).
type AppenderCases struct {
	Amount Cents  `json:"amount"`
	Cost   *Cents `json:"cost,omitempty"`
}

// GenMap is a map-shaped custom Marshaler (its own
// MarshalJSON/UnmarshalJSON over an underlying map[string]interface{}),
// mirroring a common real-world shape: an easyjson-annotated
// map[string]interface{} with its own generated Marshal/Unmarshal
// methods. The generator classifies this as kMarshaler (custom JSON
// methods win over structural map handling), not kMap.
type GenMap map[string]any

func (d GenMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(d))
}

func (d *GenMap) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*d = m
	return nil
}

// MapFieldsLike is a struct with two map-shaped-Marshaler fields tagged
// omitempty — the shape that exercises omitempty gating of map-shaped
// Marshaler fields. Set/Where must be omitted when nil or empty (matching
// easyjson's own generated `len(Where) != 0` gating) rather than emitting
// their key (e.g. `"where":null` for a nil map).
type MapFieldsLike struct {
	Op    string `json:"op,omitempty" zerojson:"intern"`
	Set   GenMap `json:"set,omitempty"`
	Where GenMap `json:"where,omitempty"`
}

// Interaction fixtures cover receiver/addressability, reused-target,
// duplicate-key, ordered-fallback, and pooled-arena combinations that a
// field-kind x tag x zero/full matrix cannot express on its own.
type OddInt int

func (v OddInt) IsZero() bool { return v == 42 }

type OddSlice []int

func (v OddSlice) IsZero() bool { return len(v) == 0 }

type InteractionOmitZero struct {
	When  *time.Time `json:"when,omitzero"`
	Both  *time.Time `json:"both,omitempty,omitzero"`
	Odd   OddInt     `json:"odd,omitzero"`
	Items OddSlice   `json:"items,omitzero"`
}

type PointerOnlyJSON struct {
	Value int `json:"value"`
}

type PointerOnlyQuoted int64

func (v *PointerOnlyQuoted) MarshalJSON() ([]byte, error) {
	return []byte(`"pointer-quoted"`), nil
}

func (v *PointerOnlyQuoted) UnmarshalJSON([]byte) error {
	*v = 99
	return nil
}

func (v *PointerOnlyJSON) MarshalJSON() ([]byte, error) {
	return []byte(`"pointer-method"`), nil
}

func (v *PointerOnlyJSON) UnmarshalJSON([]byte) error {
	v.Value = 99
	return nil
}

type MapPointerMethods struct {
	Values  map[string]PointerOnlyJSON   `json:"values"`
	Numbers map[string]PointerOnlyQuoted `json:"numbers"`
}

type PointerMethodIndex map[string]PointerOnlyJSON
type PointerQuotedIndex map[string]PointerOnlyQuoted

type ReuseInner struct {
	A int `json:"a"`
	B int `json:"b"`
}

type Accumulator []int

func (v Accumulator) MarshalJSON() ([]byte, error) { return json.Marshal([]int(v)) }

func (v *Accumulator) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*v = append(*v, n)
	return nil
}

type ReuseSemantics struct {
	Values map[string]int `json:"values"`
	Inner  *ReuseInner    `json:"inner"`
	Accum  Accumulator    `json:"accum"`
}

var sideEffectCalls int

type SideEffectValue int

func (v SideEffectValue) MarshalJSON() ([]byte, error) { return []byte("0"), nil }

func (v *SideEffectValue) UnmarshalJSON([]byte) error {
	sideEffectCalls++
	return nil
}

type OrderedSideEffects struct {
	First SideEffectValue `json:"first"`
	Last  int             `json:"last"`
}

type PoolAccumulator []int

func (v PoolAccumulator) AppendZJSON(dst []byte) ([]byte, error) {
	if len(v) == 0 {
		return append(dst, "null"...), nil
	}
	return zerojson.AppendInt64(dst, int64(v[len(v)-1])), nil
}

func (v *PoolAccumulator) ReadZJSON(l *zerojson.Lexer) {
	*v = append(*v, int(l.ReadInt64()))
}

func (v PoolAccumulator) MarshalJSON() ([]byte, error) {
	if len(v) == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(v[len(v)-1])
}

func (v *PoolAccumulator) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*v = append(*v, n)
	return nil
}

type PoolAppenderHolder struct {
	Value *PoolAccumulator `json:"value"`
}

// BytesCases keeps both encoding/json input forms for []byte in the generated
// differential corpus: the common base64 string and the uncommon numeric
// array. The latter is easy to miss with byte-level mutation alone.
type BytesCases struct {
	Data  []byte            `json:"data"`
	Ptr   *[]byte           `json:"ptr"`
	Lists [][]byte          `json:"lists"`
	Index map[string][]byte `json:"index"`
}

// PointerCollections exercises pointer-to-slice and pointer-to-map fields
// (*[]T, *map[string]T — shapes encoding/json supports): a nil pointer
// encodes as null and omitempty gates on the pointer, not the pointee; on
// decode a pointer to a NIL map must not be adopted as the arena's map
// cell (the first assignment would panic), and `{}` / `[]` yield a
// pointer to a non-nil empty collection, as stdlib does.
type PointerCollections struct {
	Names *[]string          `json:"names"`
	Index *map[string]int    `json:"index"`
	Legs  *[]EventLeg        `json:"legs,omitempty"`
	Tags  *map[string]string `json:"tags,omitempty"`
}

// NumberCases exercises encoding/json.Number (the literal verbatim, bare on
// the wire — NOT a quoted enum, which its named-string shape would
// otherwise suggest) in every position, plus the `,string` quoted-null
// no-op on value and pointer fields.
type NumberCases struct {
	N    json.Number            `json:"n"`
	NO   json.Number            `json:"no,omitempty"`
	NP   *json.Number           `json:"np"`
	NS   []json.Number          `json:"ns"`
	NM   map[string]json.Number `json:"nm"`
	CntS uint32                 `json:"cnt_s,string"`
	PtrS *int64                 `json:"ptr_s,string"`
}

// NullAware is a nullable custom type: UnmarshalJSON("null") clears Valid,
// which is exactly the case where a decoder that skipped the callback on
// null (as easyjson does) would silently leave a stale Valid=true.
type NullAware struct {
	Valid bool
	N     int
}

func (v NullAware) MarshalJSON() ([]byte, error) {
	if !v.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(v.N)
}

func (v *NullAware) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = NullAware{}
		return nil
	}
	v.Valid = true
	return json.Unmarshal(b, &v.N)
}

// NullCases exercises null delivery to value-typed Unmarshalers in every
// position, and the pointer position (cleared).
type NullCases struct {
	One   NullAware            `json:"one"`
	Many  []NullAware          `json:"many"`
	Index map[string]NullAware `json:"index"`
	Ptr   *NullAware           `json:"ptr"`
}

// FoldNames exercises case-insensitive key lookup: declared names that an
// ASCII key reaches only through a non-ASCII simple fold (Kelvin sign, long
// s), a name no ASCII key reaches, folded ties that field order resolves,
// and names that differ only in case. fold_test.go pins the lookup against
// encoding/json and against an exact-then-EqualFold reference.
type FoldNames struct {
	Kelvin  int "json:\"\u212a\"" // "K" folds onto it; exact "k" is Lower
	Lower   int `json:"k"`
	LongS   int "json:\"\u017fize\"" // "size" and "SIZE" fold onto it, ahead of Size
	Size    int `json:"Size"`
	Cafe    int "json:\"caf\u00e9\"" // no ASCII key matches it
	OrderID int `json:"orderID"`
	OrderId int `json:"OrderId"` // "ORDERID" folds onto orderID, the earlier field
	Dashed  int `json:"x-y_z"`
	Longest int `json:"a_declared_name_longer_than_the_others"`
}

// FoldUnicodeNames declares only names containing a rune with no ASCII
// simple fold (dotless i, e with acute), so no ASCII key matches any field
// and the generated lookup has no ASCII switch.
type FoldUnicodeNames struct {
	Ilk  int "json:\"\u0131lk\""
	Cafe int "json:\"caf\u00e9\""
}

// OrderedLookup is an ordered-eligible type for the ordered decoder's key
// lookup (zjSeekOrderedKeyOrderedLookup): names that are prefixes of one
// another, names that differ only in case (the exact one wins, otherwise
// the first under folding), and names on both sides of 32 bytes.
type OrderedLookup struct {
	N      int    `json:"n"`
	No     int    `json:"no"`
	Nos    int    `json:"nos"`
	UpID   int    `json:"ID"`
	LowID  int    `json:"id"` // "Id" folds onto ID, the earlier field
	Dash   int    `json:"-,"`
	Dashed int    `json:"x-y"`
	Long   int    `json:"a_declared_name_longer_than_thirty_two"`
	Last   string `json:"last"`
}

// IgnoredOnly has no JSON fields at all, so every key is unknown to its
// ordered decoder.
type IgnoredOnly struct {
	Hidden int `json:"-"`
}
