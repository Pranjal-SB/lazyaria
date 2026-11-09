package daemon

import (
	"encoding/json"
	"time"
)

type progressWire struct {
	ID         string `json:"id"`
	Done       int64  `json:"done"`
	Total      int64  `json:"total"`
	SpeedDown  int64  `json:"speed_down"`
	SpeedUp    int64  `json:"speed_up"`
	Conns      int64  `json:"conns"`
	ETASeconds *int64 `json:"eta_seconds"` // null when unknown
}

func (p ProgressItem) MarshalJSON() ([]byte, error) {
	w := progressWire{ID: p.ID, Done: p.Done, Total: p.Total, SpeedDown: p.Down, SpeedUp: p.Up, Conns: p.Conns}
	if p.ETA != nil {
		s := int64(p.ETA.Round(time.Second) / time.Second)
		w.ETASeconds = &s
	}
	return json.Marshal(w)
}

func (p *ProgressItem) UnmarshalJSON(b []byte) error {
	var w progressWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*p = ProgressItem{ID: w.ID, Done: w.Done, Total: w.Total, Down: w.SpeedDown, Up: w.SpeedUp, Conns: w.Conns}
	if w.ETASeconds != nil {
		d := time.Duration(*w.ETASeconds) * time.Second
		p.ETA = &d
	}
	return nil
}
