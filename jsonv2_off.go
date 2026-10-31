//go:build !goexperiment.jsonv2

package zerojson

// Without the jsonv2 experiment (off by default before Go 1.27, and turned
// off with GOEXPERIMENT=nojsonv2 since), encoding/json is the classic
// implementation, which calls neither MarshalJSONTo nor AppendText, so no
// value needs to be left to json.Marshal for them (see jsonv2_on.go).

func hasMarshalJSONTo(any) bool { return false }

func hasAppendText(any) bool { return false }
