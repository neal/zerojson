// Package xpkg verifies cross-package composition: its generated types
// contain types that were generated separately in package inner. Those are
// reached through inner's generated AppendZJSON/ReadZJSON pair (the
// zerojson.Appender/Reader extension interface), and the result must
// match encoding/json exactly.
package xpkg

//go:generate go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types Outer,Chain -pool -out zerojson_gen.go -tests zerojson_gen_fuzz_test.go

import "github.com/neal/zerojson/conformance/generator/xpkg/inner"

type Outer struct {
	ID    string               `json:"id"`
	V     inner.Ext            `json:"v"`
	P     *inner.Ext           `json:"p,omitempty"`
	S     []inner.Ext          `json:"s,omitempty"`
	SP    []*inner.Ext         `json:"sp,omitempty"`
	M     map[string]inner.Ext `json:"m,omitempty"`
	Roots inner.Exts           `json:"roots,omitempty"`
	Index inner.ExtIndex       `json:"index,omitempty"`
	Tail  int                  `json:"tail"`
}

// Chain nests a same-package generated type that itself holds a foreign
// generated type, so the foreign Reader runs two levels down.
type Chain struct {
	Head  string `json:"head"`
	Outer *Outer `json:"outer,omitempty"`
}
