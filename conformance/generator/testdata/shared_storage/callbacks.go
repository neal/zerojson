package p

import (
	"encoding/json"
	zj "github.com/neal/zerojson"
)

var Calls int

type Callback int

func (c Callback) MarshalJSON() ([]byte, error) { return json.Marshal(int(c)) }
func (c *Callback) UnmarshalJSON(b []byte) error {
	Calls++
	var n int
	if e := json.Unmarshal(b, &n); e != nil {
		return e
	}
	*c = Callback(n)
	return nil
}

type Callbacks struct {
	A Callback
	S []Callback
	M map[string]Callback
	Z int
}
type Leaf int64

var LeafCalls int

func AppendLeaf(dst []byte, v *Leaf) []byte { return zj.AppendInt64(dst, int64(*v)) }
func ReadLeaf(l *zj.Lexer, v *Leaf)         { LeafCalls++; *v = Leaf(l.ReadInt64()) }

type Leaves struct {
	A Leaf
	Z int
}
