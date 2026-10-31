package main

import (
	"strings"
	"testing"
	"unicode"
)

// TestASCIIFoldMemberMatchesEqualFold checks asciiFoldMember against
// strings.EqualFold for every rune and every ASCII byte: an ASCII key byte
// c matches rune r exactly when r has an ASCII fold member equal to c
// lowercased. EqualFold compares rune by rune, so this is what makes the
// generated ASCII switch agree with the EqualFold chain it skips.
func TestASCIIFoldMemberMatchesEqualFold(t *testing.T) {
	var ascii [0x80]string
	for c := range ascii {
		ascii[c] = string(rune(c))
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		m, ok := asciiFoldMember(r)
		s := string(r)
		for c, cs := range ascii {
			want := strings.EqualFold(cs, s)
			lc := byte(c)
			if 'A' <= lc && lc <= 'Z' {
				lc += 'a' - 'A'
			}
			if got := ok && m == lc; got != want {
				t.Fatalf("rune %U vs %q: member=%q ok=%v, EqualFold=%v", r, cs, m, ok, want)
			}
		}
	}
}

func TestASCIIFoldKey(t *testing.T) {
	for _, c := range []struct {
		name, key string
		ok        bool
	}{
		{"OrderID", "orderid", true},
		{"x-y_z", "x-y_z", true},
		{"\u212a", "k", true},
		{"\u017fize", "size", true},
		{"caf\u00e9", "", false},
		{"\u0131d", "", false}, // dotless i has no ASCII simple fold
		{"\xff", "", false},    // invalid UTF-8 folds as U+FFFD
	} {
		key, ok := asciiFoldKey(c.name)
		if key != c.key || ok != c.ok {
			t.Errorf("asciiFoldKey(%q) = %q, %v; want %q, %v", c.name, key, ok, c.key, c.ok)
		}
	}
}
