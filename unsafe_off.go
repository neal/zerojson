//go:build nounsafe

package zerojson

// This file is the provably-unsafe-free counterpart to unsafe_on.go,
// selected by `go build -tags nounsafe`. The helpers here copy where the
// default build aliases; output is byte-identical, only the allocation
// behavior differs. The root module imports no `unsafe` under this tag.

// bytesToString returns a freshly-allocated string copy of b. Safe
// counterpart to the unsafe.String alias in unsafe_on.go.
func bytesToString(b []byte) string {
	return string(b)
}

// stringToBytes returns a freshly-allocated []byte copy of s. Safe
// counterpart to the unsafe.Slice view in unsafe_on.go. Callers only read
// the result, so copying is behaviorally identical.
func stringToBytes(s string) []byte {
	return []byte(s)
}
