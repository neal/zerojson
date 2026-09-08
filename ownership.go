package zerojson

import "reflect"

// SameBacking reports whether a and b share a backing array: both have
// capacity and their first backing element is the same. Generated pooled
// decoders use it to tell an arena cell handed back through the target —
// after an ordered-path rewind re-reads a member, or on a duplicate key —
// from storage a caller or callback supplied, which must not be recycled
// on the next pooled call. Recycling our own cell is what -pool exists for;
// misclassifying it as foreign would make every non-canonical document
// (out-of-order or duplicated keys) reallocate the cell every call.
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
func SameMap[K comparable, V any](a, b map[K]V) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
