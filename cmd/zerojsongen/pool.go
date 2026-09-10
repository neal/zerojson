package main

// genPool emits a single-goroutine, reset-first decoder with per-call storage.
// The busy guard is released on panic as well as ordinary return.
func (g *generator) genPool(ti typeInfo) {
	n := ti.name
	dec := n + "Decoder"
	g.p("")
	g.p("// %s decodes %s values with fresh storage on every call.", dec, n)
	g.p("// Later calls do not invalidate earlier results. Borrowed strings still alias the input.")
	atomicQ := g.importAlias("sync/atomic", "atomic")
	g.p("type %s struct {", dec)
	g.p("\tbusy %s.Bool // reject overlapping calls on the same Decoder", atomicQ)
	g.p("}")
	for _, m := range []struct{ name, borrow, doc string }{
		{"Decode", "false", "// Decode decodes data into v using fresh per-call storage.\n// *v is reset first; its pre-existing storage is not adopted.\n// Storage supplied by callbacks may be used within this call.\n// Later calls do not invalidate the result. Concurrent calls on one\n// Decoder panic; use one Decoder per goroutine."},
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
		g.p("\tvar a %s", arenaName(n))
		g.p("\tl := zerojson.NewLexer(data, %s)", m.borrow)
		g.p("\t%s(&l, v, &a)", decodeName(n))
		g.p("\tl.Finish()")
		g.p("\treturn l.Err()")
		g.p("}")
	}
}
