package zerojson

// SameBacking reports whether both slices have capacity and their first
// backing elements have the same address. It does not detect overlapping
// slices with different starting offsets. For zero-sized elements, address
// equality does not establish shared storage.
//
// Retained for compatibility with older generated codecs; current decoders
// do not use it because each call has fresh working storage.
func SameBacking[T any](a, b []T) bool {
	if cap(a) == 0 || cap(b) == 0 {
		return false
	}
	// Reslicing to length 1 is legal whenever the capacity allows it, so
	// this addresses the first backing element even for empty headers.
	return &a[:1][0] == &b[:1][0]
}

// SameMap reports whether a and b are the same map (two headers over one
// table), the map counterpart of SameBacking. Two nil maps are the same.
// The default build reads the map pointer directly (unsafe_on.go); the
// nounsafe build goes through reflect (unsafe_off.go).
func SameMap[K comparable, V any](a, b map[K]V) bool {
	return sameMap(a, b)
}
