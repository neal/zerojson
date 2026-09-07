package p

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/neal/zerojson/conformance/internal/collide/src"
)

var calls int

type Token struct{}

func (Token) MarshalJSON() ([]byte, error) { return []byte(`"token"`), nil }
func (*Token) UnmarshalJSON(b []byte) error {
	calls++
	if string(b) != `"token"` {
		return errors.New("bad token")
	}
	return nil
}

type EmptyText struct{}

func (EmptyText) MarshalText() ([]byte, error) { return []byte("token"), nil }
func (*EmptyText) UnmarshalText(b []byte) error {
	calls++
	if string(b) != "token" {
		return errors.New("bad text")
	}
	return nil
}

type PointerToken struct{}

func (*PointerToken) MarshalJSON() ([]byte, error) { return []byte(`"pointer"`), nil }
func (*PointerToken) UnmarshalJSON([]byte) error   { calls++; return nil }

type MapHolder struct {
	V map[string]Token `json:"v"`
}
type TokenIndex map[string]Token
type TextIndex map[string]EmptyText
type PointerIndex map[string]PointerToken

type hidden struct {
	Visible int `json:"visible"`
}
type Named struct {
	hidden `json:"nested"`
	Own    int `json:"own"`
}
type Options struct {
	hidden `json:",omitempty"`
	Own    int `json:"own"`
}
type Ignored struct {
	hidden `json:"-"`
	Own    int `json:"own"`
}

type Byte byte
type ByteAlias = Byte
type Bytes []Byte
type CustomByte byte

func (v CustomByte) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.Itoa(int(v)))
}
func (v *CustomByte) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	n, err := strconv.ParseUint(s, 10, 8)
	*v = CustomByte(n)
	return err
}

type CustomAlias = CustomByte
type ByteHolder struct {
	Plain    []Byte            `json:"plain"`
	Alias    []ByteAlias       `json:"alias"`
	Named    *Bytes            `json:"named"`
	Nested   [][]Byte          `json:"nested"`
	Map      map[string][]Byte `json:"map"`
	Custom   []CustomByte      `json:"custom"`
	Via      []CustomAlias     `json:"via"`
	External []src.Byte        `json:"external"`
}

type EmptyArray [0]int

func (EmptyArray) MarshalJSON() ([]byte, error) { return []byte(`"present"`), nil }
func (*EmptyArray) UnmarshalJSON([]byte) error  { calls++; return nil }

type EmptyTextArray [0]int

func (EmptyTextArray) MarshalText() ([]byte, error) { return []byte("present"), nil }
func (*EmptyTextArray) UnmarshalText([]byte) error  { calls++; return nil }

type FullArray [2]int

func (FullArray) MarshalJSON() ([]byte, error) { return []byte(`"present"`), nil }
func (*FullArray) UnmarshalJSON([]byte) error  { calls++; return nil }

type Arrays struct {
	Empty EmptyArray     `json:"empty,omitempty"`
	Text  EmptyTextArray `json:"text,omitempty"`
	Full  FullArray      `json:"full,omitempty"`
}

type Enum string

const Known Enum = "known"

type Child struct {
	N int `json:"n"`
}
type Refs struct {
	A *int            `json:"a"`
	B *int            `json:"b"`
	C *Child          `json:"c"`
	D *Child          `json:"d"`
	E *Enum           `json:"e"`
	F *Enum           `json:"f"`
	G *[]int          `json:"g"`
	H *[]int          `json:"h"`
	I *map[string]int `json:"i"`
	J *map[string]int `json:"j"`
	K *string         `json:"k"`
	L *string         `json:"l"`
}

type Acc int

func (v Acc) MarshalJSON() ([]byte, error) { return json.Marshal(int(v)) }
func (v *Acc) UnmarshalJSON(b []byte) error {
	calls++
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*v += Acc(n)
	return nil
}

type CustomRefs struct {
	A *Acc `json:"a"`
	B *Acc `json:"b"`
}

type Positive int

func (v *Positive) UnmarshalJSON(b []byte) error {
	calls++
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	if n < 0 {
		return errors.New("negative value rejected")
	}
	*v = Positive(n)
	return nil
}

type AnyBox struct {
	V any `json:"v"`
}
type Interfaces struct {
	V    any    `json:"v"`
	P    *any   `json:"p"`
	Box  AnyBox `json:"box"`
	Tail int    `json:"tail"`
}

type Text string

func (v Text) MarshalText() ([]byte, error)  { return []byte(v), nil }
func (v *Text) UnmarshalText(b []byte) error { calls++; *v = Text(b); return nil }

type TextHolder struct {
	V Text            `json:"v"`
	P *Text           `json:"p"`
	S []Text          `json:"s"`
	M map[string]Text `json:"m"`
	L []*Text         `json:"l"`
}
type TextList []Text
