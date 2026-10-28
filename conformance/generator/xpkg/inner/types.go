// Package inner holds zerojson-generated types that package xpkg nests
// inside its own generated types: the cross-package composition fixture.
package inner

//go:generate go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types Ext,Exts,ExtIndex -pool -out zerojson_gen.go -tests zerojson_gen_fuzz_test.go

type Kind string

const (
	KindA Kind = "a"
	KindB Kind = "b"
)

// Ext mixes fast-path leaves with a nested same-package pointer struct, so
// its own arena is non-trivial when it is decoded on behalf of another
// package's type.
type Ext struct {
	Name  string   `json:"name"`
	Kind  Kind     `json:"kind,omitempty"`
	N     int64    `json:"n"`
	Score float64  `json:"score,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Sub   *Sub     `json:"sub,omitempty"`
}

type Sub struct {
	A string `json:"a"`
	B []int  `json:"b,omitempty"`
}

// Exts and ExtIndex are slice and map roots, which a foreign field may
// also reference.
type Exts []Ext

type ExtIndex map[string]Ext
