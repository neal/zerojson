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
	if !errors.As(err, &fe) || fe.Path != "legs.price" {
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

// TestFieldErrorPathHasNoPrefix pins that Path is the bare dotted path
// (usable as a metric label or for comparison) and that only Error adds
// the "field " prefix.
func TestFieldErrorPathHasNoPrefix(t *testing.T) {
	fe := &FieldError{Path: "legs.price", Err: errors.New("boom")}
	if fe.Error() != "field legs.price: boom" {
		t.Fatalf("Error() = %q", fe.Error())
	}
	if strings.HasPrefix(fe.Path, "field") {
		t.Fatalf("Path carries a prefix: %q", fe.Path)
	}
}

// TestNumberErrorsAreBounded pins that a decode error quotes at most a
// prefix of an oversized number token: a hostile multi-megabyte number
// must not turn into a multi-megabyte error string (twice, with
// strconv's own copy) in every log line.
func TestNumberErrorsAreBounded(t *testing.T) {
	huge := []byte(strings.Repeat("9", 1<<20))
	for name, read := range map[string]func(l *Lexer){
		"int64":   func(l *Lexer) { l.ReadInt64() },
		"uint64":  func(l *Lexer) { l.ReadUint64() },
		"float64": func(l *Lexer) { l.ReadFloat64() },
		"float32": func(l *Lexer) { l.ReadFloat32() },
		"any":     func(l *Lexer) { l.ReadInterface() },
	} {
		l := NewLexer(huge, false)
		read(&l)
		err := l.Err()
		if err == nil {
			// float64 parses 10^1048576 as +Inf → out of range; ints overflow.
			t.Fatalf("%s: expected an error for a %d-digit number", name, len(huge))
		}
		if n := len(err.Error()); n > 256 {
			t.Fatalf("%s: error message is %d bytes for a %d-byte token: %.100s...", name, n, len(huge), err.Error())
		}
		if !strings.Contains(err.Error(), "(1048576 bytes)") {
			t.Fatalf("%s: message does not report the token length: %s", name, err.Error())
		}
	}
	if _, err := AppendJSONNumber(nil, string(huge)+"x"); err == nil || len(err.Error()) > 256 {
		t.Fatalf("AppendJSONNumber: err=%v", err)
	}
}
