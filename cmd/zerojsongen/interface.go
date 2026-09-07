package main

// A nil interface (and a non-pointer concrete value) decodes into generic
// JSON shapes. A non-nil concrete pointer instead retains its receiver and
// method set, as encoding/json does. The reflection/stdlib path is only for
// that reused-target case; ordinary fresh and pooled inputs keep our readers.
func (g *generator) emitInterfaceRead(target, ind string) {
	rq := g.importAlias("reflect", "reflect")
	jq := g.importAlias("encoding/json", "json")
	g.p("%sif %s == nil {", ind, target)
	g.p("%s\t%s = l.ReadInterface()", ind, target)
	g.p("%s} else if rv := %s.ValueOf(%s); rv.Kind() == %s.Pointer && !rv.IsNil() {", ind, rq, target, rq)
	g.p("%s\tif raw := l.RawValue(); l.Err() == nil {", ind)
	g.p("%s\t\tl.AddError(%s.Unmarshal(raw, &%s))", ind, jq, target)
	g.p("%s\t}", ind)
	g.p("%s} else {", ind)
	g.p("%s\t%s = l.ReadInterface()", ind, target)
	g.p("%s}", ind)
}

// A null usually clears an interface, but a **T stored inside it can have
// its inner pointer cleared instead. Let stdlib preserve that distinction.
func (g *generator) emitInterfaceNull(target, ind string) {
	jq := g.importAlias("encoding/json", "json")
	g.p("%sif %s != nil {", ind, target)
	g.p("%s\tl.AddError(%s.Unmarshal(l.NullBytes(), &%s))", ind, jq, target)
	g.p("%s}", ind)
}
