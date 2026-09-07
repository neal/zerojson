package zerojson

import (
	"strings"
	"testing"
)

// TestSkipDepthCountsFromCurrentNesting pins that Skip and ReadInterface
// measure nesting from where they sit in the document, not from zero, so
// a decode agrees with Valid (and encoding/json) about the 10000-level
// limit at every position — previously an unknown field's value nested
// exactly 10000 deep inside an object decoded successfully while Valid
// rejected it (Skip started its count at 0 instead of 1).
func TestSkipDepthCountsFromCurrentNesting(t *testing.T) {
	// A hand-driven object decode, the way every generated fallback
	// decoder walks one: EnterObject / ReadKey / Skip / NextMember.
	decode := func(doc []byte, unknownValue func(l *Lexer)) error {
		l := NewLexer(doc, false)
		if l.EnterObject() {
			for {
				l.ReadKey()
				unknownValue(&l)
				if l.Err() != nil {
					break
				}
				if !l.NextMember() {
					break
				}
			}
		}
		l.Finish()
		return l.Err()
	}
	skip := func(l *Lexer) { l.Skip() }
	iface := func(l *Lexer) { l.ReadInterface() }

	for _, n := range []int{maxDepth - 2, maxDepth - 1, maxDepth, maxDepth + 1} {
		doc := []byte(`{"x":` + strings.Repeat("[", n) + strings.Repeat("]", n) + `}`)
		valid := Valid(doc) == nil
		for name, fn := range map[string]func(*Lexer){"Skip": skip, "ReadInterface": iface} {
			got := decode(doc, fn) == nil
			if got != valid {
				t.Errorf("%d arrays under an object via %s: decode ok=%v, Valid ok=%v", n, name, got, valid)
			}
		}
	}
	// Sanity: the limit itself is where stdlib puts it — exactly maxDepth
	// total levels accepted (object + maxDepth-1 arrays), one more rejected.
	deepOK := []byte(`{"x":` + strings.Repeat("[", maxDepth-1) + strings.Repeat("]", maxDepth-1) + `}`)
	if err := decode(deepOK, skip); err != nil {
		t.Fatalf("%d total levels must decode: %v", maxDepth, err)
	}
	deepBad := []byte(`{"x":` + strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth) + `}`)
	if err := decode(deepBad, skip); err == nil {
		t.Fatalf("%d total levels must be rejected", maxDepth+1)
	}
}

// TestOrderedBailRestoresDepth pins that OrderedBail restores the nesting
// depth along with the position: a generated ordered decoder Enter()s the
// object it is matching, and a bail before its Leave() would otherwise
// leave the fallback retry one level deeper than the document is.
func TestOrderedBailRestoresDepth(t *testing.T) {
	l := NewLexer([]byte(`{"a":1}`), false)
	m := l.Mark()
	if !l.Enter() {
		t.Fatal("Enter failed at depth 0")
	}
	l.SetPos(3)
	l.setErr("speculative attempt failed")
	l.OrderedBail(m)
	if l.Pos() != 0 || l.Err() != nil || l.depth != 0 {
		t.Fatalf("OrderedBail did not restore: pos=%d err=%v depth=%d", l.Pos(), l.Err(), l.depth)
	}
}

// TestReadInterfaceDepthIsBalanced pins that ReadInterface leaves the
// depth counter exactly where it found it: it Enters a container and
// relies on NextMember/NextElem to Leave on the closing delimiter, so a
// second decrement (the previous bug) would drive the counter negative
// and let a later sibling exceed the limit undetected.
func TestReadInterfaceDepthIsBalanced(t *testing.T) {
	for _, doc := range []string{
		`{"a":[1,[2]],"b":{"c":{}},"d":[],"e":[[[]]]}`,
		`[{},[],{"x":[{}]}]`,
		`{}`, `[]`, `[[]]`,
	} {
		l := NewLexer([]byte(doc), false)
		l.ReadInterface()
		if l.Err() != nil || l.depth != 0 {
			t.Errorf("%s: err=%v depth=%d (want 0)", doc, l.Err(), l.depth)
		}
	}
	// The observable consequence: after a preceding `any` sibling, a
	// second sibling nested to the limit must still be rejected exactly
	// where Valid rejects it.
	deep := strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth)
	doc := []byte(`{"a":[[1]],"b":` + deep + `}`)
	l := NewLexer(doc, false)
	l.ReadInterface()
	if (l.Err() == nil) != (Valid(doc) == nil) {
		t.Fatalf("sibling after an any value: decode err=%v, Valid err=%v", l.Err(), Valid(doc))
	}
}

// TestEmptyContainersCountTowardDepth pins that `[]` / `{}` occupy a
// nesting level (as Valid and encoding/json count them): 10001 nested
// arrays whose innermost is empty must be rejected on every decode path
// — the hand-driven EnterArray protocol, Skip, and ReadInterface.
func TestEmptyContainersCountTowardDepth(t *testing.T) {
	nest := func(n int) []byte {
		return []byte(strings.Repeat("[", n) + strings.Repeat("]", n))
	}
	viaEnterArray := func(doc []byte) error {
		l := NewLexer(doc, false)
		var walk func()
		walk = func() {
			if l.EnterArray() {
				for {
					walk()
					if l.Err() != nil {
						return
					}
					if !l.NextElem() {
						break
					}
				}
			}
		}
		walk()
		l.Finish()
		return l.Err()
	}
	viaSkip := func(doc []byte) error {
		l := NewLexer(doc, false)
		l.Skip()
		l.Finish()
		return l.Err()
	}
	viaInterface := func(doc []byte) error {
		l := NewLexer(doc, false)
		l.ReadInterface()
		l.Finish()
		return l.Err()
	}
	for _, n := range []int{maxDepth - 1, maxDepth, maxDepth + 1} {
		doc := nest(n)
		want := Valid(doc) == nil
		for name, fn := range map[string]func([]byte) error{"EnterArray": viaEnterArray, "Skip": viaSkip, "ReadInterface": viaInterface} {
			if got := fn(doc) == nil; got != want {
				t.Errorf("%d nested arrays ending in [] via %s: ok=%v, Valid ok=%v", n, name, got, want)
			}
		}
	}
	// And the object flavour, through EnterObject.
	l := NewLexer([]byte(strings.Repeat(`{"a":`, maxDepth)+`{}`+strings.Repeat("}", maxDepth)), false)
	var walk func()
	walk = func() {
		if l.EnterObject() {
			for {
				l.ReadKey()
				walk()
				if l.Err() != nil {
					return
				}
				if !l.NextMember() {
					break
				}
			}
		}
	}
	walk()
	if l.Err() == nil {
		t.Fatalf("%d nested objects ending in {} must be rejected", maxDepth+1)
	}
}
