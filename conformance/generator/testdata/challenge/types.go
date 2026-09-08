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
