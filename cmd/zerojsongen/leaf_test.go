package main

import "testing"

func TestParseLeaf(t *testing.T) {
	tp, ls, err := parseLeaf("github.com/foo/money.Amount=github.com/foo/jpmoney.Append,github.com/foo/jpmoney.Read")
	if err != nil {
		t.Fatal(err)
	}
	if tp != "github.com/foo/money.Amount" {
		t.Errorf("type path: %q", tp)
	}
	// Qualifiers are assigned later by the generator (useLeaf), from the
	// package's declared name, so parseLeaf leaves them empty.
	if ls.appendPkg != "github.com/foo/jpmoney" || ls.appendFn != "Append" || ls.appendQual != "" {
		t.Errorf("append: %+v", ls)
	}
	if ls.readPkg != "github.com/foo/jpmoney" || ls.readFn != "Read" {
		t.Errorf("read: %+v", ls)
	}

	for _, bad := range []string{"noequals", "t=onlyone", "t=a.A,b.B,c.C", "t=nopkgdot,x.Y"} {
		if _, _, err := parseLeaf(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
