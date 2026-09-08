//go:build !nounsafe

package zerojson

import "unsafe"

// bytesToString returns a string that aliases b without copying. Every
// caller satisfies the borrow-lifetime contract documented in the "Unsafe
// inventory" section of conformance/DOMAINS.md. Building with the
// `nounsafe` tag swaps in a real-copy implementation (unsafe_off.go) that
// produces byte-identical results at the cost of an allocation.
func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// stringToBytes returns a read-only []byte view aliasing s without
// copying, for word-at-a-time (SWAR) scanning. The view must never be
// written to and must not escape the caller. Building with the `nounsafe`
// tag swaps in a real-copy implementation (unsafe_off.go).
func stringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// sameMap compares the map pointers behind two map values. A map value is
// a single pointer to its table, so reading it through the variable's
// address is exact and allocation-free; the nounsafe counterpart uses
// reflect.
func sameMap[K comparable, V any](a, b map[K]V) bool {
	return *(*unsafe.Pointer)(unsafe.Pointer(&a)) == *(*unsafe.Pointer)(unsafe.Pointer(&b))
}
