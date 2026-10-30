//go:build go1.27

package zerojson

import (
	"encoding"
	"encoding/json/jsontext"
)

// encoding/json built on encoding/json/v2 prefers MarshalJSONTo over
// MarshalJSON, and AppendText over MarshalText, so AppendInterface leaves a
// value with one of them to json.Marshal rather than call the method it
// outranks. The checks see v's own method set, as encoding/json does: a
// pointer-receiver method counts only when v holds the pointer.

func hasMarshalJSONTo(v any) bool {
	_, ok := v.(interface {
		MarshalJSONTo(*jsontext.Encoder) error
	})
	return ok
}

func hasAppendText(v any) bool {
	_, ok := v.(encoding.TextAppender)
	return ok
}
