package zerojson

import (
	"testing"
	"time"
)

// TestEscapedTimestampAndUUID pins that an escaped spelling of a
// timestamp or UUID string decodes like the plain spelling in zerojson.
func TestEscapedTimestampAndUUID(t *testing.T) {
	want, _ := time.Parse(time.RFC3339, "2024-05-06T07:08:09Z")
	l := NewLexer([]byte(`"\u0032024-05-06T07:08:09Z"`), false) // \u0032 is "2"
	if got := l.ReadTime(); l.Err() != nil || !got.Equal(want) {
		t.Fatalf("escaped timestamp: %v err=%v", got, l.Err())
	}
	l = NewLexer([]byte(`"2024-05-06T07:08:09\u005a"`), false)
	if got := l.ReadTime(); l.Err() != nil || got.Year() != 2024 || got.Second() != 9 {
		t.Fatalf("escaped Z timestamp: %v err=%v", got, l.Err())
	}
	l = NewLexer([]byte(`"\u0031\u0032345678-1234-1234-1234-123456789abc"`), false)
	u := l.ReadUUIDBytes()
	if l.Err() != nil || u[0] != 0x12 || u[15] != 0xbc {
		t.Fatalf("escaped uuid: %x err=%v", u, l.Err())
	}
}
