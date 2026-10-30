package main

import (
	"go/token"
	"go/types"
	"sort"
	"strings"
	"unicode"
)

// Map fields and map roots decode through one zjReadMap helper per map type,
// shared by every field of that type. A new map is made once, at its final
// size, because growing it a member at a time costs more than decoding the
// members. The helper holds up to mapSlots members on the stack; one more
// moves them to a zjSpillMap helper, whose larger buffer lives in its own
// frame so small maps never pay to zero it. Past that buffer the map is
// made with room for twice as many members and filled directly. The runtime
// does the same for objects decoded by ReadInterface (interface.go).
const (
	mapSlots = 8
	// mapSpillBytes bounds the spill buffer, which holds at most
	// mapSpillSlots members. Entries too large for mapSpillMinSlots of them
	// (big struct values) keep the plain grow-as-you-go loop: copying them
	// twice would cost more than the growth it saves.
	mapSpillBytes    = 1280
	mapSpillSlots    = 40
	mapSpillMinSlots = 16
)

// mapEntrySizes sizes buffered key/value pairs. The budget is approximate,
// so one 64-bit layout serves every target.
var mapEntrySizes = types.SizesFor("gc", "amd64")

// mapEntrySize is the size of one buffered key/value pair of a map[k]v.
func mapEntrySize(k, v types.Type) int64 {
	return mapEntrySizes.Sizeof(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "k", k, false),
		types.NewField(token.NoPos, nil, "v", v, false),
	}, nil))
}

// mapSpillCap is the spill buffer length for entries of the given size, or
// 0 when the map should not be buffered at all.
func mapSpillCap(entrySize int64) int64 {
	slots := min(int64(mapSpillSlots), mapSpillBytes/max(entrySize, 1))
	if slots < mapSpillMinSlots {
		return 0
	}
	return slots
}

// mapHelper is one emitted zjReadMap helper (with its spill helper and entry
// type, when buffered).
type mapHelper struct {
	id   string // suffix shared by the helper's declarations
	code string
}

// mapHelperID marks where a helper's id goes in its code until a unique id
// is chosen.
const mapHelperID = "\x00"

// mapReadCall registers (once) and returns the name of the helper that
// decodes a JSON object into a map of type typeStr with keyed values vi:
// func(l *zerojson.Lexer, m typeStr) typeStr. A non-nil m is added to; a
// nil m yields a new map. Fields with the same map type and options share
// one helper.
func (g *generator) mapReadCall(typeStr, keyTypeStr string, vi *fieldInfo, intern bool, entrySize int64) string {
	// The helper is emitted to a scratch buffer: its code is also its
	// de-duplication key.
	saved := g.buf
	g.buf = strings.Builder{}
	g.emitMapHelper(typeStr, keyTypeStr, vi, intern, entrySize)
	code := g.buf.String()
	g.buf = saved
	if h, ok := g.mapHelpers[code]; ok {
		return "zjReadMap_" + h.id
	}
	// The sanitized type is lossy (map[a]b and map_a_b collide), so it is
	// de-duplicated against the ids of other helpers.
	id := helperIdent(typeStr)
	if intern {
		id += "_intern"
	}
	for taken := true; taken; {
		taken = false
		for _, other := range g.mapHelpers {
			if other.id == id {
				id += "_"
				taken = true
				break
			}
		}
	}
	g.mapHelpers[code] = &mapHelper{id: id, code: strings.ReplaceAll(code, mapHelperID, id)}
	return "zjReadMap_" + id
}

// helperIdent turns a type expression into identifier characters.
func helperIdent(typeStr string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, typeStr)
}

// emitMapHelpers writes every registered map helper, in a stable order,
// after the type bodies.
func (g *generator) emitMapHelpers() {
	helpers := make([]*mapHelper, 0, len(g.mapHelpers))
	for _, h := range g.mapHelpers {
		helpers = append(helpers, h)
	}
	sort.Slice(helpers, func(i, j int) bool { return helpers[i].id < helpers[j].id })
	for _, h := range helpers {
		g.buf.WriteString(h.code)
	}
}

func (g *generator) emitMapHelper(typeStr, keyTypeStr string, vi *fieldInfo, intern bool, entrySize int64) {
	read := "zjReadMap_" + mapHelperID
	spillCap := mapSpillCap(entrySize)
	if spillCap == 0 {
		g.p("")
		g.p("// %s decodes a JSON object into m, or into a new map when m is nil.", read)
		g.p("func %s(l *zerojson.Lexer, m %s) %s {", read, typeStr, typeStr)
		g.p("\tif m == nil { m = make(%s) }", typeStr)
		g.p("\tif l.EnterObject() {")
		g.p("\t\tfor {")
		g.emitMapMember(keyTypeStr, vi, intern, "\t\t\t")
		g.p("\t\t\tif l.Err() != nil { break }")
		g.p("\t\t\tm[mk] = mv")
		g.p("\t\t\tif !l.NextMember() { break }")
		g.p("\t\t}")
		g.p("\t}")
		g.p("\treturn m")
		g.p("}")
		return
	}
	spill := "zjSpillMap_" + mapHelperID
	entry := "zjMapEntry_" + mapHelperID
	g.p("")
	g.p("// %s is one member of a map still being buffered.", entry)
	g.p("type %s struct {", entry)
	g.p("\tk %s", keyTypeStr)
	g.p("\tv %s", vi.typeStr)
	g.p("}")
	g.p("")
	g.p("// %s decodes a JSON object into m, or into a new map when m is nil.", read)
	g.p("// A new map is made at its final size: up to %d members wait on the", mapSlots)
	g.p("// stack, and one more moves them to %s.", spill)
	g.p("func %s(l *zerojson.Lexer, m %s) %s {", read, typeStr, typeStr)
	g.p("\tvar kv [%d]%s", mapSlots, entry)
	g.p("\tn := 0")
	g.p("\tif l.EnterObject() {")
	g.p("\t\tfor {")
	g.emitMapMember(keyTypeStr, vi, intern, "\t\t\t")
	g.p("\t\t\tif l.Err() != nil { break }")
	g.p("\t\t\tif m != nil {")
	g.p("\t\t\t\tm[mk] = mv")
	g.p("\t\t\t} else if n < len(kv) {")
	g.p("\t\t\t\tkv[n] = %s{mk, mv}", entry)
	g.p("\t\t\t\tn++")
	g.p("\t\t\t} else {")
	g.p("\t\t\t\treturn %s(l, &kv, mk, mv)", spill)
	g.p("\t\t\t}")
	g.p("\t\t\tif !l.NextMember() { break }")
	g.p("\t\t}")
	g.p("\t}")
	g.p("\tif m == nil {")
	g.p("\t\tm = make(%s, n)", typeStr)
	g.p("\t\tfor _, e := range kv[:n] { m[e.k] = e.v }")
	g.p("\t}")
	g.p("\treturn m")
	g.p("}")
	g.p("")
	g.p("// %s continues %s past %d members, given them and the", spill, read, mapSlots)
	g.p("// member just read. Past its own buffer it makes the map with room for")
	g.p("// twice as many members and inserts the rest directly.")
	g.p("func %s(l *zerojson.Lexer, first *[%d]%s, mk %s, mv %s) %s {", spill, mapSlots, entry, keyTypeStr, vi.typeStr, typeStr)
	g.p("\tvar kv [%d]%s", spillCap, entry)
	g.p("\tn := copy(kv[:], first[:])")
	g.p("\tkv[n] = %s{mk, mv}", entry)
	g.p("\tn++")
	g.p("\tvar m %s", typeStr)
	g.p("\tfor l.NextMember() {")
	g.emitMapMember(keyTypeStr, vi, intern, "\t\t")
	g.p("\t\tif l.Err() != nil { break }")
	g.p("\t\tif m != nil {")
	g.p("\t\t\tm[mk] = mv")
	g.p("\t\t} else if n < len(kv) {")
	g.p("\t\t\tkv[n] = %s{mk, mv}", entry)
	g.p("\t\t\tn++")
	g.p("\t\t} else {")
	g.p("\t\t\tm = make(%s, 2*len(kv))", typeStr)
	g.p("\t\t\tfor _, e := range kv[:n] { m[e.k] = e.v }")
	g.p("\t\t\tm[mk] = mv")
	g.p("\t\t}")
	g.p("\t}")
	g.p("\tif m == nil {")
	g.p("\t\tm = make(%s, n)", typeStr)
	g.p("\t\tfor _, e := range kv[:n] { m[e.k] = e.v }")
	g.p("\t}")
	g.p("\treturn m")
	g.p("}")
}

// emitMapMember reads one member's key into mk and value into mv.
func (g *generator) emitMapMember(keyTypeStr string, vi *fieldInfo, intern bool, ind string) {
	if intern {
		g.p("%smk := %s(zerojson.Intern(l.ReadKey()))", ind, keyTypeStr)
	} else {
		// string() copies the key: ReadKey aliases the input buffer and a
		// map key outlives the decode. Interning (which also copies) is
		// opt-in via zerojson:"intern" — map keys are attacker-controlled
		// and unbounded in cardinality, so it is not done by default.
		g.p("%smk := %s(string(l.ReadKey()))", ind, keyTypeStr)
	}
	g.p("%svar mv %s", ind, vi.typeStr)
	g.emitElemNullOpen(vi, "mv", ind)
	switch vi.kind {
	case kCustomLeaf:
		g.leafRead(vi.leaf, "&mv", ind+"\t")
	case kEmptyStruct:
		// struct{} set-map value: require an object (any contents,
		// ignored) — a number/array/string is rejected, matching
		// encoding/json and easyjson. null already handled by IsNull.
		g.p("%s\tl.SkipObject()", ind)
	case kStruct:
		g.p("%s\tvar ea %s", ind, arenaName(vi.elem))
		g.p("%s\t%s(l, &mv, &ea)", ind, decodeName(vi.elem))
	case kMarshaler:
		g.p("%s\tif raw := l.RawValue(); l.Err() == nil { if err := mv.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
	case kTextMarshaler:
		g.p("%s\tif txt := l.ReadTextBytes(); l.Err() == nil { if err := mv.UnmarshalText(txt); err != nil { l.AddError(err) } }", ind)
	case kAppender:
		g.p("%s\tmv.ReadZJSON(l)", ind)
	case kQuotedInt:
		if !vi.marshalOnValue {
			g.p("%s\tif raw := l.RawValue(); l.Err() == nil { if err := mv.UnmarshalJSON(raw); err != nil { l.AddError(err) } }", ind)
		} else {
			g.p("%s\tmv = %s", ind, g.leafReadExpr(vi))
		}
	case kInterface:
		g.p("%s\tmv = l.ReadInterface()", ind)
	default:
		g.p("%s\tmv = %s", ind, g.leafReadExpr(vi))
	}
	g.p("%s}", ind)
}
