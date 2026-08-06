// Package compat verifies the -compat and -compat-easyjson wrappers:
// with them present, encoding/json and easyjson route through zerojson.
package compat

//go:generate go run github.com/neal/zerojson/cmd/zerojsongen -dir . -types Point -compat-easyjson -out zerojson_gen.go

type Point struct {
	X    int    `json:"x,omitempty"`
	Y    int    `json:"y,omitempty"`
	Name string `json:"name,omitempty"`
}
