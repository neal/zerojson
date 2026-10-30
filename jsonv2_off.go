//go:build !go1.27 && !goexperiment.jsonv2

package zerojson

// The classic encoding/json calls neither MarshalJSONTo nor AppendText, so
// no value needs to be left to json.Marshal for them (see jsonv2_on.go).

func hasMarshalJSONTo(any) bool { return false }

func hasAppendText(any) bool { return false }
