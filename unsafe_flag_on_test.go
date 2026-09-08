//go:build !nounsafe

package zerojson

// borrowAliases reports whether borrow-mode strings alias the input in this
// build; the nounsafe build copies instead (unsafe_off.go).
const borrowAliases = true
