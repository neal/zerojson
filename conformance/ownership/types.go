package ownership

//go:generate go run ../../cmd/zerojsongen -dir . -types Sample,Item,Payload,ObservedPayload -pool

import (
	"encoding/json"
	"time"
)

var observe func()

type Observed int

func (v Observed) MarshalJSON() ([]byte, error) { return json.Marshal(int(v)) }
func (v *Observed) UnmarshalJSON(b []byte) error {
	if observe != nil {
		observe()
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*v = Observed(n)
	return nil
}

type ObservedPayload struct{ Values []Observed }

type Sample struct{ Values []int }
type Item struct {
	ID       string
	Quantity int
	Price    float64
}
type Payload struct {
	ID       string
	Count    int
	Ratio    float64
	Stamp    time.Time
	Names    []string
	Items    []Item
	Index    map[string]int
	Optional *int
}
