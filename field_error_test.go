package zerojson

import (
	"errors"
	"strings"
	"testing"
)

// TestFieldErrorPath pins the lazily built field path on decode errors.
func TestFieldErrorPath(t *testing.T) {
	l := NewLexer([]byte(`x`), false)
	l.ReadBool()
	l.AnnotateField([]byte("price"))
	l.AnnotateField([]byte("legs"))
	err := l.Err()
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "field legs.price" {
		t.Fatalf("got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "field legs.price: zerojson: expected bool") {
		t.Fatalf("message: %v", err)
	}
	if l.Err() != err {
		t.Fatal("Err must be idempotent once annotated")
	}
	// A bail discards annotations recorded during the abandoned attempt.
	l2 := NewLexer([]byte(`{"a":x}`), false)
	m := l2.Mark()
	l2.ReadBool()
	l2.AnnotateFieldString("a")
	l2.OrderedBail(m)
	if l2.Err() != nil {
		t.Fatal("bail did not clear")
	}
}
