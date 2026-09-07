package p

import (
	"encoding/json"
	"fmt"

	"github.com/neal/zerojson"
)

var campaignCalls int

type CampaignCounter int

func (v CampaignCounter) MarshalJSON() ([]byte, error) { return json.Marshal(int(v)) }
func (v *CampaignCounter) UnmarshalJSON(data []byte) error {
	campaignCalls++
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("negative counter")
	}
	*v += CampaignCounter(n)
	return nil
}

type CampaignNested struct {
	P *int `json:"p"`
}
type CampaignState struct {
	A      *int             `json:"a"`
	B      *int             `json:"b"`
	C      *CampaignCounter `json:"c"`
	D      *CampaignCounter `json:"d"`
	Any    any              `json:"any"`
	S      *string          `json:"s"`
	R      zerojson.Raw     `json:"r"`
	Nested CampaignNested   `json:"nested"`
}
