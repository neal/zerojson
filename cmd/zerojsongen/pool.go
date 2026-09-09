package main

// genPool emits a single-goroutine decoder that reuses arena storage.
// Each call zeroes the target but retains arena capacity. Field readers reset
// cells before exposing them, so omitted fields do not leak old values.
// The next call invalidates arena-backed results, even if that call fails.
// The busy guard is released on panic as well as ordinary return.
func (g *generator) genPool(ti typeInfo) {
	n := ti.name
	dec := n + "Decoder"
	g.p("")
	g.p("// %s decodes %s values reusing a single arena across calls. The", dec, n)
	g.p("// decoded value aliases that arena (and, in borrow mode, the input),")
	g.p("// so it is invalidated by the next Decode/DecodeBorrow on this Decoder;")
	g.p("// finish with or copy out of one result before decoding the next.")
	atomicQ := g.importAlias("sync/atomic", "atomic")
	g.p("type %s struct {", dec)
	g.p("\ta    %s", arenaName(n))
	g.p("\tbusy %s.Bool // a Decoder is single-goroutine; concurrent use panics with a clear message instead of tearing the arena", atomicQ)
	g.p("}")
	for _, m := range []struct{ name, borrow, doc string }{
		{"Decode", "false", "// Decode decodes data into v, reusing the Decoder's arena.\n// Field decoding may still allocate. *v is reset to its zero value first,\n// so its pre-existing storage is not adopted. Storage supplied by callbacks\n// may be used within the call but is not recycled by a later call.\n// The result aliases the arena and is invalidated by the next\n// Decode/DecodeBorrow on this Decoder. Concurrent calls on one Decoder\n// panic; use one Decoder per goroutine (e.g. via sync.Pool)."},
		{"DecodeBorrow", "true", "// DecodeBorrow is Decode with zero-copy strings: decoded strings\n// alias data, which must stay live and unmodified while v is used."},
	} {
		g.p("")
		g.p("%s", m.doc)
		g.p("func (d *%s) %s(data []byte, v *%s) error {", dec, m.name, n)
		g.p("\tif !d.busy.CompareAndSwap(false, true) {")
		g.p("\t\tpanic(\"zerojson: concurrent %s on the same %s — a Decoder is single-goroutine; use one per goroutine (for example via sync.Pool)\")", m.name, dec)
		g.p("\t}")
		g.p("\t// Deferred so a panic inside the decode (a user UnmarshalJSON, a")
		g.p("\t// nil v) cannot leave the Decoder permanently \"busy\".")
		g.p("\tdefer d.busy.Store(false)")
		g.p("\tvar zero %s", n)
		g.p("\t*v = zero")
		g.p("\tif err := zerojson.Valid(data); err != nil { return err }")
		if ti.rootField == nil {
			g.p("\td.a.beginDecode()")
		}
		g.p("\tl := zerojson.NewLexer(data, %s)", m.borrow)
		g.p("\t%s(&l, v, &d.a)", decodeName(n))
		g.p("\tl.Finish()")
		g.p("\treturn l.Err()")
		g.p("}")
	}
}
