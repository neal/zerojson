package zerojson

import (
	"testing"
)

// TestParseUUIDBytesFallbacks hits two non-canonical parseUUIDBytes
// rejection branches: a 45-byte value that is NOT urn:uuid:-prefixed, and
// a 32-byte un-hyphenated value with a non-hex digit.
func TestParseUUIDBytesFallbacks(t *testing.T) {
	// 45 content bytes (36 + 9), first 9 bytes != "urn:uuid:".
	nonURN := `"xxxxxxxxx01234567-89ab-cdef-0123-456789abcdef"`
	if len(nonURN) != 45+2 {
		t.Fatalf("test setup: want 45 content bytes, got %d", len(nonURN)-2)
	}
	l := NewLexer([]byte(nonURN), false)
	_ = l.ReadUUIDBytes()
	if l.Err() == nil {
		t.Fatal("ReadUUIDBytes accepted a non-urn 45-byte value")
	}
	// 32 hex chars but the last is 'Z'.
	badHex := `"0123456789abcdef0123456789abcdeZ"`
	l2 := NewLexer([]byte(badHex), false)
	_ = l2.ReadUUIDBytes()
	if l2.Err() == nil {
		t.Fatal("ReadUUIDBytes accepted a 32-byte value with a non-hex digit")
	}
}

func TestEscapedUUID(t *testing.T) {
	l := NewLexer([]byte(`"\u0031\u0032345678-1234-1234-1234-123456789abc"`), false)
	u := l.ReadUUIDBytes()
	if l.Err() != nil || u[0] != 0x12 || u[15] != 0xbc {
		t.Fatalf("escaped uuid: %x err=%v", u, l.Err())
	}
}
