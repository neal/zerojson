package zerojson_test

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/neal/zerojson"
)

// Example_valid validates an untrusted payload at a trust boundary without
// decoding it — the use case Valid exists for (a payload stored or
// forwarded but never decoded has no decode call to inherit a
// grammar-validity guarantee from).
func Example_valid() {
	trusted := []byte(`{"user":"alice","roles":["admin"]}`)
	fmt.Println(zerojson.Valid(trusted) == nil)

	malformed := []byte(`{"user":"alice",}`)
	fmt.Println(zerojson.Valid(malformed) == nil)
	// Output:
	// true
	// false
}

// Ratio joins the zero-alloc encode/decode path by implementing the
// Appender/Reader interface pair directly — no -leaf registration and no
// reflection. Any generated struct with a Ratio field picks up the fast
// path automatically.
type Ratio struct{ Num, Den int }

// AppendZJSON encodes r as "num/den". It follows the same contract as every
// generated AppendZJSON: append to dst, never reset it, return the extended
// slice.
func (r Ratio) AppendZJSON(dst []byte) ([]byte, error) {
	dst = append(dst, '"')
	dst = strconv.AppendInt(dst, int64(r.Num), 10)
	dst = append(dst, '/')
	dst = strconv.AppendInt(dst, int64(r.Den), 10)
	return append(dst, '"'), nil
}

// ReadZJSON decodes "num/den" from l, reporting failures via l.Fail — the
// leaf read convention (pull a token, report errors on the Lexer, no return
// value).
func (r *Ratio) ReadZJSON(l *zerojson.Lexer) {
	s := l.ReadString()
	if l.Err() != nil {
		return
	}
	num, den, ok := strings.Cut(s, "/")
	n, err1 := strconv.Atoi(num)
	d, err2 := strconv.Atoi(den)
	if !ok || err1 != nil || err2 != nil {
		l.Fail("ratio %q: invalid", s)
		return
	}
	r.Num, r.Den = n, d
}

// Example_appenderReader shows a hand-written type joining the fast path via
// the Appender/Reader pair, round-tripped through the same append/read
// calling convention the generator emits.
func Example_appenderReader() {
	enc, _ := Ratio{3, 4}.AppendZJSON(nil)
	fmt.Println(string(enc))

	var got Ratio
	l := zerojson.NewLexer(enc, false)
	got.ReadZJSON(&l)
	fmt.Printf("%d/%d\n", got.Num, got.Den)
	// Output:
	// "3/4"
	// 3/4
}

// appendColor / readColor are a custom-leaf codec pair for a type you don't
// own (here, a plain uint32 treated as an RGB color). Registering them with
// the generator —
//
//	-leaf 'yourpkg.Color=yourpkg.appendColor,yourpkg.readColor'
//
// makes every Color field encode as "#rrggbb" on the fast path. The append
// function is infallible; the read function reports errors via the Lexer.
func appendColor(dst []byte, c *uint32) []byte {
	return fmt.Appendf(dst, `"#%06x"`, *c&0xffffff)
}

func readColor(l *zerojson.Lexer, c *uint32) {
	s := l.ReadString()
	if l.Err() != nil {
		return
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		l.Fail("color %q: invalid", s)
		return
	}
	*c = uint32(v)
}

// Example_customLeaf exercises a -leaf codec pair directly (the generator
// would emit calls to exactly these functions at each Color field).
func Example_customLeaf() {
	c := uint32(0x3366ff)
	enc := appendColor(nil, &c)
	fmt.Println(string(enc))

	var got uint32
	l := zerojson.NewLexer(enc, false)
	readColor(&l, &got)
	fmt.Printf("%#06x\n", got)
	// Output:
	// "#3366ff"
	// 0x3366ff
}
