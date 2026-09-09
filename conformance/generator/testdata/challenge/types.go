package challenge

import "encoding/json"

var callback func(int) error

type Hook int

func (Hook) MarshalJSON() ([]byte, error) { return []byte("0"), nil }
func (*Hook) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	if callback != nil {
		return callback(n)
	}
	return nil
}

type Nested struct {
	P *int
	S []int
	H Hook
}
type Record struct {
	H     Hook
	S     []int
	T     []int
	P     *int
	Inner *Nested
}
type RootSlice []int
type RootMap map[string]int

var valueCallback func()

type Observed int

func (v Observed) MarshalJSON() ([]byte, error) { return json.Marshal(int(v)) }
func (v *Observed) UnmarshalJSON(data []byte) error {
	if valueCallback != nil {
		valueCallback()
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = Observed(n)
	return nil
}

type CallbackView struct {
	Values   []Observed
	Pointers []*Observed
	Indirect *[]Observed
	Index    map[string]Observed
	Nodes    []ContainerNode
	NodeMap  map[string]ContainerNode
}

type ContainerNode struct{ Value Observed }

type Ordinary struct {
	Name    int
	Numbers []int
	Items   []OrdinaryItem
	Upper   int `json:"A"`
	Lower   int `json:"a"`
	Kelvin  int `json:"k"`
	LongS   int `json:"s"`
}
type OrdinaryItem struct{ A, B int }

type ObservedList []Observed
type ObservedIndex map[string]Observed
