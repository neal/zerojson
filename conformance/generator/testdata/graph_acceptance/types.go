package acceptance

import "encoding/json"

type Child struct {
	S []int
	P *int
}
type Plain struct {
	A, B       []int
	Bytes      []byte
	BytePtr    *[]byte
	MapA, MapB map[string]int
	P          *int
	Child      Child
	Label      string
}

var observe func()

type Hook int

func (h Hook) MarshalJSON() ([]byte, error) { return json.Marshal(int(h)) }
func (h *Hook) UnmarshalJSON(data []byte) error {
	if observe != nil {
		observe()
	}
	return json.Unmarshal(data, (*int)(h))
}

type Observed struct {
	Before Hook
	State  Plain
	After  Hook
}

type Large struct{ A, B, C, D, E, F, G, H string }
type Cells struct {
	A, B   *int
	Nested *Child
}
type MapCases struct {
	Small   map[string]int
	Medium  map[string]Child
	Large   map[string]Large
	Dynamic any
}
type Interaction struct {
	Lead  int
	Cells Cells
	Maps  MapCases
	Tail  int
}
