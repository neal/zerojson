package codec

import (
	"errors"
	"github.com/neal/zerojson"
	"testing"
)

// TestDecodeErrorCarriesFieldPath pins the field path on decode errors
// through nested structs, slices, and delegated Unmarshalers.
func TestDecodeErrorCarriesFieldPath(t *testing.T) {
	for _, c := range []struct{ doc, path string }{
		{`{"seq":"abc"}`, "seq"},
		{`{"items":[{"ratio":"1.5","label":1}]}`, "items.label"},
		{`{"meta":{"internal":5}}`, "meta.internal"},
	} {
		var ev Event
		err := ev.UnmarshalZJSON([]byte(c.doc))
		var fe *zerojson.FieldError
		if err == nil || !errors.As(err, &fe) || fe.Path != c.path {
			t.Errorf("%s: err=%v (want path %q)", c.doc, err, c.path)
		}
	}
}
