package main

// genPool emits a reusable-arena Decoder for a top-level type: it holds one
// arena that is reused across decodes, eliminating the per-decode arena
// allocation. Pool the Decoder (one per goroutine, or a sync.Pool) for a
// zero-allocation decode loop.
//
// The arena is deliberately NOT wiped between calls (that would defeat the
// slice/map cells' whole purpose of reusing their backing storage): every
// field kind resets whatever it's about to (re)populate itself — pointer
// cells are unconditionally overwritten before use, kStruct pointer cells
// are explicitly zeroed before the nested decoder recurses into them,
// slice cells are resliced to length 0 before repopulating (keeping
// capacity), map cells are make'd once and clear()ed thereafter — so a
// stale arena is never observable through v: any field the decoder didn't
// just (re)assign is a field v itself never got told to have.
//
// Lifetime: the decoded struct's slice, map, and pointer fields alias the
// Decoder's arena, so a decoded value is invalidated by the next
// Decode/DecodeBorrow on the same Decoder. Finish with (or copy out of)
// one result before decoding the next.
func (g *generator) genPool(ti typeInfo) {
	n := ti.name
	dec := n + "Decoder"
	g.p("")
	g.p("// %s decodes %s values reusing a single arena across calls. The", dec, n)
	g.p("// decoded value aliases that arena (and, in borrow mode, the input),")
	g.p("// so it is invalidated by the next Decode/DecodeBorrow on this Decoder;")
	g.p("// finish with or copy out of one result before decoding the next.")
	g.p("type %s struct {", dec)
	g.p("\ta %s", arenaName(n))
	g.p("}")
	g.p("")
	g.p("// Decode decodes data into v, reusing the Decoder's arena (no")
	g.p("// per-decode allocation). v must be zero-valued; the result is")
	g.p("// invalidated by the next Decode/DecodeBorrow on this Decoder.")
	g.p("func (d *%s) Decode(data []byte, v *%s) error {", dec, n)
	g.p("\tl := zerojson.NewLexer(data, false)")
	g.p("\t%s(&l, v, &d.a)", decodeName(n))
	g.p("\tl.Finish()")
	g.p("\treturn l.Err()")
	g.p("}")
	g.p("")
	g.p("// DecodeBorrow is Decode with zero-copy strings: decoded strings")
	g.p("// alias data, which must stay live and unmodified while v is used.")
	g.p("func (d *%s) DecodeBorrow(data []byte, v *%s) error {", dec, n)
	g.p("\tl := zerojson.NewLexer(data, true)")
	g.p("\t%s(&l, v, &d.a)", decodeName(n))
	g.p("\tl.Finish()")
	g.p("\treturn l.Err()")
	g.p("}")
}
