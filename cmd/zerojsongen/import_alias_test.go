package main

import (
	"go/types"
	"testing"
)

func TestImportAliasesAvoidPredeclaredNames(t *testing.T) {
	g := &generator{imports: map[string]string{}, aliasOwner: map[string]string{}}
	for _, name := range types.Universe.Names() {
		alias := g.importAlias("example.com/"+name, name)
		if types.Universe.Lookup(alias) != nil {
			t.Errorf("predeclared import qualifier: %s", alias)
		}
	}
}

// TestImportAliasIsCollisionFree pins that packages whose names collide
// with generated identifiers, with each other, or with an already-aliased
// name all receive distinct import aliases, and that the runtime keeps its
// fixed `zerojson` alias.
func TestImportAliasIsCollisionFree(t *testing.T) {
	g := &generator{imports: map[string]string{}, aliasOwner: map[string]string{}}
	got := map[string]string{}
	for _, c := range []struct{ path, name string }{
		{runtimePath, "zerojson"},
		{"example.com/x/a", "a"},
		{"example.com/y/a_", "a_"},
		{"example.com/p/data", "data"},
		{"example.com/q/data", "data"},
		{"example.com/other/zerojson", "zerojson"},
		{"example.com/x/a", "a"}, // repeat: stable
	} {
		got[c.path] = g.importAlias(c.path, c.name)
	}
	if got[runtimePath] != "zerojson" {
		t.Errorf("runtime alias = %q", got[runtimePath])
	}
	seen := map[string]string{}
	for path, alias := range got {
		if other, dup := seen[alias]; dup && other != path {
			t.Errorf("alias %q shared by %s and %s", alias, other, path)
		}
		seen[alias] = path
		if generatedIdents[alias] && path != runtimePath {
			t.Errorf("alias %q for %s collides with a generated identifier", alias, path)
		}
	}
	if got["example.com/x/a"] == got["example.com/y/a_"] {
		t.Errorf("a and a_ collapsed to %q", got["example.com/x/a"])
	}

	// Registration ORDER must not matter. The runtime import is emitted
	// unconditionally under `zerojson` whether or not it was ever passed
	// through importAlias, so a dependency package named `zerojson` that
	// registers FIRST must still be pushed off that qualifier — otherwise
	// the generated file carries two imports under one name.
	g2 := &generator{imports: map[string]string{}, aliasOwner: map[string]string{}}
	foreign := g2.importAlias("example.com/other/zerojson", "zerojson")
	if foreign == "zerojson" {
		t.Errorf("dependency package named zerojson claimed the runtime qualifier")
	}
	if rt := g2.importAlias(runtimePath, "zerojson"); rt != "zerojson" {
		t.Errorf("runtime alias = %q after a same-named dependency registered first", rt)
	}
	if again := g2.importAlias("example.com/other/zerojson", "zerojson"); again != foreign {
		t.Errorf("dependency alias changed from %q to %q", foreign, again)
	}
}
