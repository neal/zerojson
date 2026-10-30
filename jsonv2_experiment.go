//go:build goexperiment.jsonv2 && !go1.27

package zerojson

import (
	"encoding"
	"reflect"
)

// Before Go 1.27, encoding/json is built on encoding/json/v2 only under
// GOEXPERIMENT=jsonv2, and this module's Go version predates the jsontext
// API, so MarshalJSONTo is found by name (see jsonv2_on.go).

func hasMarshalJSONTo(v any) bool {
	if v == nil {
		return false
	}
	_, ok := reflect.TypeOf(v).MethodByName("MarshalJSONTo")
	return ok
}

func hasAppendText(v any) bool {
	_, ok := v.(encoding.TextAppender)
	return ok
}
