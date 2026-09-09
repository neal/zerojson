package codec

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
)

// metamorphic_test.go holds oracle-free metamorphic property fuzzers over
// Event decode. Each takes an input that decodes successfully, applies a
// semantics-preserving (or, for duplicate keys, semantics-defining)
// JSON-aware transformation, and requires the transformed input to decode
// to the same value. "Oracle-free" because the property is zerojson
// against ITSELF across the transformation — no second implementation is
// needed to say what the right answer is, only that a semantically
// equivalent rewrite must not change it (the duplicate-key case
// additionally pins last-wins against encoding/json, since that IS a
// semantics question a metamorphic relation alone can't settle).
//
// Every transformer is validated on its own output (transformed bytes
// must still be json.Valid) so a transformer bug can never masquerade as
// a decoder bug: if the rewrite produced invalid JSON, the test fails on
// the transformer, not on zerojson.
//
// The three relations:
//
//	(a) whitespace invariance — inserting arbitrary RFC 8259 whitespace
//	    between tokens (never inside a string or number) must not change
//	    the decode. This drives every skipWS site and, on the ordered fast
//	    path, every point where whitespace can appear around a declared
//	    key/value.
//	(b) key-order invariance — permuting object member order (top-level
//	    and nested) must not change the decode. This systematically
//	    exercises the ordered matcher's out-of-order bail and its fallback.
//	    Skipped for any object carrying duplicate keys, whose meaning is
//	    order-dependent.
//	(c) duplicate-key last-wins — appending a duplicate of an existing
//	    member with a different value must yield the later value, matching
//	    encoding/json on the identical bytes.

// jnode is a faithful structural model of a JSON value: objects keep their
// members in order and preserve duplicates; scalars (string, number,
// bool, null) keep their exact source bytes, so re-serialization changes
// only structural whitespace and member order, never a value's spelling.
type jnode struct {
	kind  byte // 'o' object, 'a' array, 's' scalar (verbatim raw)
	raw   []byte
	memb  []jmember
	elems []*jnode
}

type jmember struct {
	keyRaw []byte // the key string token, quotes included, verbatim
	val    *jnode
}

// jparser is a minimal recursive-descent parser for already-valid JSON
// (every input here decoded successfully via zerojson, which implies
// grammar-validity). It returns ok=false rather than panicking if it ever
// meets something unexpected, so a caller simply skips that input.
type jparser struct {
	b   []byte
	pos int
}

func parseJSONTree(b []byte) (*jnode, bool) {
	p := &jparser{b: b}
	p.ws()
	n, ok := p.value()
	if !ok {
		return nil, false
	}
	p.ws()
	if p.pos != len(p.b) {
		return nil, false
	}
	return n, true
}

func (p *jparser) ws() {
	for p.pos < len(p.b) {
		switch p.b[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jparser) value() (*jnode, bool) {
	if p.pos >= len(p.b) {
		return nil, false
	}
	switch p.b[p.pos] {
	case '{':
		return p.object()
	case '[':
		return p.array()
	case '"':
		s, ok := p.stringToken()
		if !ok {
			return nil, false
		}
		return &jnode{kind: 's', raw: s}, true
	default:
		return p.scalar()
	}
}

func (p *jparser) object() (*jnode, bool) {
	p.pos++ // '{'
	n := &jnode{kind: 'o'}
	p.ws()
	if p.pos < len(p.b) && p.b[p.pos] == '}' {
		p.pos++
		return n, true
	}
	for {
		p.ws()
		key, ok := p.stringToken()
		if !ok {
			return nil, false
		}
		p.ws()
		if p.pos >= len(p.b) || p.b[p.pos] != ':' {
			return nil, false
		}
		p.pos++
		p.ws()
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		n.memb = append(n.memb, jmember{keyRaw: key, val: v})
		p.ws()
		if p.pos >= len(p.b) {
			return nil, false
		}
		switch p.b[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return n, true
		default:
			return nil, false
		}
	}
}

func (p *jparser) array() (*jnode, bool) {
	p.pos++ // '['
	n := &jnode{kind: 'a', elems: []*jnode{}}
	p.ws()
	if p.pos < len(p.b) && p.b[p.pos] == ']' {
		p.pos++
		return n, true
	}
	for {
		p.ws()
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		n.elems = append(n.elems, v)
		p.ws()
		if p.pos >= len(p.b) {
			return nil, false
		}
		switch p.b[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return n, true
		default:
			return nil, false
		}
	}
}

// stringToken returns the full string token including both quotes.
func (p *jparser) stringToken() ([]byte, bool) {
	if p.pos >= len(p.b) || p.b[p.pos] != '"' {
		return nil, false
	}
	start := p.pos
	p.pos++
	for p.pos < len(p.b) {
		switch p.b[p.pos] {
		case '\\':
			p.pos += 2 // skip escaped byte; \uXXXX's hex are ordinary bytes to the token scan
		case '"':
			p.pos++
			return p.b[start:p.pos], true
		default:
			p.pos++
		}
	}
	return nil, false
}

// scalar consumes a bare number/true/false/null token verbatim.
func (p *jparser) scalar() (*jnode, bool) {
	start := p.pos
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return nil, false
	}
	return &jnode{kind: 's', raw: p.b[start:p.pos]}, true
}

// wsSource hands out whitespace strings drawn from fuzz entropy so the
// injector's choices are reproducible from the corpus entry.
type wsSource struct {
	r      *rand.Rand
	inject bool
}

func (w *wsSource) gap() []byte {
	if !w.inject {
		return nil
	}
	n := w.r.Intn(3) // 0..2 whitespace bytes between tokens
	if n == 0 {
		return nil
	}
	const ws = " \t\n\r"
	out := make([]byte, n)
	for i := range out {
		out[i] = ws[w.r.Intn(len(ws))]
	}
	return out
}

// serialize walks the tree, optionally injecting whitespace at every
// structural gap and optionally permuting object member order. Scalars and
// key tokens are emitted verbatim, so only whitespace and order change.
func serialize(dst []byte, n *jnode, w *wsSource, permute *rand.Rand) []byte {
	switch n.kind {
	case 's':
		return append(dst, n.raw...)
	case 'o':
		dst = append(dst, '{')
		order := make([]int, len(n.memb))
		for i := range order {
			order[i] = i
		}
		if permute != nil {
			permute.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		}
		for idx, mi := range order {
			if idx > 0 {
				dst = append(dst, w.gap()...)
				dst = append(dst, ',')
			}
			dst = append(dst, w.gap()...)
			dst = append(dst, n.memb[mi].keyRaw...)
			dst = append(dst, w.gap()...)
			dst = append(dst, ':')
			dst = append(dst, w.gap()...)
			dst = serialize(dst, n.memb[mi].val, w, permute)
		}
		dst = append(dst, w.gap()...)
		return append(dst, '}')
	case 'a':
		dst = append(dst, '[')
		for i, e := range n.elems {
			if i > 0 {
				dst = append(dst, w.gap()...)
				dst = append(dst, ',')
			}
			dst = append(dst, w.gap()...)
			dst = serialize(dst, e, w, permute)
		}
		dst = append(dst, w.gap()...)
		return append(dst, ']')
	}
	return dst
}

// hasDuplicateKeys reports whether any object anywhere in the tree has two
// members with the same key token bytes — the order-dependent case
// key-order permutation must skip.
func hasDuplicateKeys(n *jnode) bool {
	switch n.kind {
	case 'o':
		seen := map[string]bool{}
		for _, m := range n.memb {
			k := string(m.keyRaw)
			if seen[k] {
				return true
			}
			seen[k] = true
			if hasDuplicateKeys(m.val) {
				return true
			}
		}
	case 'a':
		for _, e := range n.elems {
			if hasDuplicateKeys(e) {
				return true
			}
		}
	}
	return false
}

// decodeEventEqual decodes a and b into Event via zerojson and reports
// whether they are DeepEqual, plus whether each decoded without error.
func decodeEvent(t *testing.T, data []byte) (*Event, bool) {
	t.Helper()
	var ev Event
	if err := ev.UnmarshalZJSON(data); err != nil {
		return nil, false
	}
	return &ev, true
}

// FuzzMetamorphicWhitespace: inserting legal whitespace between tokens
// must not change the Event decode.
func FuzzMetamorphicWhitespace(f *testing.F) {
	for _, s := range metamorphicSeeds(f) {
		f.Add(s, int64(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		if !json.Valid(data) {
			return // metamorphic domain is grammar-valid JSON; lenient-but-
			// invalid inputs (a known field's string value carrying a raw
			// control byte or an invalid escape — decode-lenient by design,
			// see Layer 1's decode-surface allowlist) are out of scope, and
			// the transformer's own json.Valid self-check presumes valid input.
		}
		if hugeExponent(data) {
			return // avoid decimal-library expansion bombs; see hugeExponent
		}
		base, ok := decodeEvent(t, data)
		if !ok {
			return
		}
		tree, ok := parseJSONTree(data)
		if !ok {
			return
		}
		w := &wsSource{r: rand.New(rand.NewSource(seed)), inject: true}
		out := serialize(nil, tree, w, nil)

		// Transformer self-check: output must still be valid JSON, and must
		// differ only by whitespace (its compact form equals the input's).
		if !json.Valid(out) {
			t.Fatalf("whitespace injector produced invalid JSON\nin:  %q\nout: %q", data, out)
		}
		if !bytes.Equal(compact(t, out), compact(t, data)) {
			t.Fatalf("whitespace injector changed non-whitespace content\nin:  %q\nout: %q", data, out)
		}

		got, ok := decodeEvent(t, out)
		if !ok {
			t.Fatalf("whitespace-injected variant failed to decode\nin:  %q\nout: %q", data, out)
		}
		if !reflect.DeepEqual(base, got) {
			t.Fatalf("whitespace invariance broken\nin:  %q\nout: %q\nbase: %+v\ngot:  %+v", data, out, base, got)
		}
	})
}

// FuzzMetamorphicKeyOrder: permuting object member order must not change
// the Event decode (skipped when duplicate keys make order significant).
func FuzzMetamorphicKeyOrder(f *testing.F) {
	for _, s := range metamorphicSeeds(f) {
		f.Add(s, int64(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		if !json.Valid(data) {
			return // grammar-valid JSON only (see FuzzMetamorphicWhitespace)
		}
		if hugeExponent(data) {
			return // avoid decimal-library expansion bombs; see hugeExponent
		}
		base, ok := decodeEvent(t, data)
		if !ok {
			return
		}
		tree, ok := parseJSONTree(data)
		if !ok {
			return
		}
		if hasDuplicateKeys(tree) {
			return // order-dependent; not an invariance case
		}
		w := &wsSource{inject: false}
		out := serialize(nil, tree, w, rand.New(rand.NewSource(seed)))

		if !json.Valid(out) {
			t.Fatalf("key permuter produced invalid JSON\nin:  %q\nout: %q", data, out)
		}
		got, ok := decodeEvent(t, out)
		if !ok {
			t.Fatalf("key-permuted variant failed to decode\nin:  %q\nout: %q", data, out)
		}
		if !reflect.DeepEqual(base, got) {
			t.Fatalf("key-order invariance broken\nin:  %q\nout: %q\nbase: %+v\ngot:  %+v", data, out, base, got)
		}
	})
}

// FuzzMetamorphicDuplicateKey: for a chosen object member (k: v), append a
// second (k: v2) with v2 different from v. zerojson must decode the LATER
// value, and must agree with encoding/json on the same transformed bytes.
func FuzzMetamorphicDuplicateKey(f *testing.F) {
	for _, s := range metamorphicSeeds(f) {
		f.Add(s, uint32(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, pick uint32) {
		if !json.Valid(data) {
			return // grammar-valid JSON only (see FuzzMetamorphicWhitespace)
		}
		if hugeExponent(data) {
			return // json.Marshal expands decimal exponents into full digits
		}
		// Canonicalize to isolate the duplicate-member transformation.
		// Original-byte fuzzers separately exercise spelling and grammar.
		var seed Event
		if json.Unmarshal(data, &seed) != nil {
			return
		}
		canonical, err := json.Marshal(&seed)
		if err != nil {
			return
		}
		data = canonical
		if _, ok := decodeEvent(t, data); !ok {
			return
		}
		tree, ok := parseJSONTree(data)
		if !ok {
			return
		}
		// Collect objects with at least one member.
		var objs []*jnode
		collectObjects(tree, &objs)
		if len(objs) == 0 {
			return
		}
		obj := objs[int(pick)%len(objs)]
		mi := int(pick>>8) % len(obj.memb)
		orig := obj.memb[mi]
		alt := differentScalar(orig.val)
		if alt == nil {
			return // couldn't manufacture a distinct value (e.g. nested object) — skip
		}
		// Append the duplicate as a new last member of this object.
		obj.memb = append(obj.memb, jmember{keyRaw: orig.keyRaw, val: alt})

		out := serialize(nil, tree, &wsSource{inject: false}, nil)
		if !json.Valid(out) {
			t.Fatalf("duplicate-key transform produced invalid JSON\nin:  %q\nout: %q", data, out)
		}

		zj, ok := decodeEvent(t, out)
		if !ok {
			// If zerojson rejects it, encoding/json must reject it too.
			var ev Event
			if json.Unmarshal(out, &ev) == nil {
				t.Fatalf("zerojson rejected a duplicate-key doc that encoding/json accepts\nout: %q", out)
			}
			return
		}
		var std Event
		if err := json.Unmarshal(out, &std); err != nil {
			t.Fatalf("encoding/json rejected a duplicate-key doc zerojson accepts\nout: %q\nerr: %v", out, err)
		}
		a, _ := json.Marshal(&std)
		b, _ := json.Marshal(zj)
		if !bytes.Equal(a, b) {
			t.Fatalf("duplicate-key last-wins mismatch\nout: %q\nstdlib:   %s\nzerojson: %s", out, a, b)
		}
	})
}

func collectObjects(n *jnode, out *[]*jnode) {
	switch n.kind {
	case 'o':
		if len(n.memb) > 0 {
			*out = append(*out, n)
		}
		for _, m := range n.memb {
			collectObjects(m.val, out)
		}
	case 'a':
		for _, e := range n.elems {
			collectObjects(e, out)
		}
	}
}

// differentScalar returns a scalar jnode whose value differs from v, or
// nil if v is not a scalar (so we don't have to synthesize a structurally
// distinct object/array). It keeps the same JSON type where it can so the
// duplicate still satisfies the field's decoder.
func differentScalar(v *jnode) *jnode {
	if v.kind != 's' {
		return nil
	}
	raw := v.raw
	switch {
	case len(raw) > 0 && raw[0] == '"':
		// string: flip to a different, still-valid string literal
		if bytes.Equal(raw, []byte(`"zj_alt"`)) {
			return &jnode{kind: 's', raw: []byte(`"zj_alt2"`)}
		}
		return &jnode{kind: 's', raw: []byte(`"zj_alt"`)}
	case bytes.Equal(raw, []byte("true")):
		return &jnode{kind: 's', raw: []byte("false")}
	case bytes.Equal(raw, []byte("false")):
		return &jnode{kind: 's', raw: []byte("true")}
	case bytes.Equal(raw, []byte("null")):
		return &jnode{kind: 's', raw: []byte("null")} // null->null is not "different"; skip
	default:
		// number: append a digit to change magnitude while staying a number
		if bytes.Equal(raw, []byte("0")) {
			return &jnode{kind: 's', raw: []byte("7")}
		}
		if raw[len(raw)-1] >= '0' && raw[len(raw)-1] <= '9' {
			nn := append(append([]byte{}, raw...), '1')
			return &jnode{kind: 's', raw: nn}
		}
	}
	return nil
}

func compact(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatalf("json.Compact failed on %q: %v", b, err)
	}
	return buf.Bytes()
}

// metamorphicSeeds reuses the shared corpus (Event variants, golden
// corpus, truncation corpus, ordered traps) assembled by validSeedCorpus,
// keeping only inputs that are grammar-valid JSON AND decode as an Event —
// so every seed is in the metamorphic domain and exercises a real
// transform. (A lenient-but-invalid input, e.g. a known string field
// holding a raw control byte, decodes fine yet is not valid JSON; those
// belong to Layer 1's decode-surface analysis, not here.)
func metamorphicSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	var out [][]byte
	for _, s := range validSeedCorpus(tb) {
		if !json.Valid(s) {
			continue
		}
		var ev Event
		if ev.UnmarshalZJSON(s) == nil {
			out = append(out, s)
		}
	}
	return out
}
