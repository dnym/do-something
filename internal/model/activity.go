package model

import (
	"encoding/json"
	"fmt"
	"math"
)

// LoggedActivity records actual time and/or money spent and the resulting
// estimates. It is historical evidence; sync never replays it against the
// current item.
type LoggedActivity struct {
	Ongoing         bool     `json:"ongoing"`
	Duration        *float64 `json:"duration"`
	RemainingBefore *float64 `json:"remaining_before"`
	RemainingAfter  *float64 `json:"remaining_after"`
	Cost            *float64 `json:"cost"`
	CostBefore      *float64 `json:"cost_before"`
	CostAfter       *float64 `json:"cost_after"`
}

// RemainingAfterLog clamps overrun and floating-point subtraction noise to zero.
func RemainingAfterLog(before, duration float64) float64 {
	after := math.Max(0, before-duration)
	if after <= 1e-12*math.Max(before, duration) {
		return 0
	}
	return after
}

func (a LoggedActivity) Validate() error {
	if a.Duration == nil && a.Cost == nil {
		return fmt.Errorf("activity requires time or cost")
	}
	if a.Duration == nil {
		if a.RemainingBefore != nil || a.RemainingAfter != nil {
			return fmt.Errorf("activity without time cannot have remaining estimates")
		}
	} else {
		if !positiveFinite(*a.Duration) {
			return fmt.Errorf("invalid activity duration")
		}
		if a.Ongoing {
			if a.RemainingBefore != nil || a.RemainingAfter != nil {
				return fmt.Errorf("ongoing activity cannot have remaining estimates")
			}
		} else {
			if a.RemainingBefore == nil || a.RemainingAfter == nil {
				return fmt.Errorf("finite activity requires remaining estimates")
			}
			for _, v := range []float64{*a.RemainingBefore, *a.RemainingAfter} {
				if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("invalid activity duration")
				}
			}
			if *a.RemainingAfter != RemainingAfterLog(*a.RemainingBefore, *a.Duration) {
				return fmt.Errorf("invalid remaining estimate")
			}
		}
	}
	if a.Cost == nil {
		if a.CostBefore != nil || a.CostAfter != nil {
			return fmt.Errorf("activity without cost cannot have cost estimates")
		}
	} else {
		if !positiveFinite(*a.Cost) {
			return fmt.Errorf("invalid activity cost")
		}
		if (a.CostBefore == nil) != (a.CostAfter == nil) {
			return fmt.Errorf("incomplete cost estimates")
		}
		if a.CostBefore != nil {
			for _, v := range []float64{*a.CostBefore, *a.CostAfter} {
				if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("invalid activity cost")
				}
			}
			if *a.CostAfter != RemainingAfterLog(*a.CostBefore, *a.Cost) {
				return fmt.Errorf("invalid remaining cost")
			}
		}
	}
	return nil
}

func positiveFinite(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func DecodeLoggedActivity(raw string) (*LoggedActivity, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, err
	}
	// The three original keys remain required. Cost keys are optional when
	// decoding time-only events written before cost logging was introduced.
	for _, key := range []string{"duration", "remaining_before", "remaining_after"} {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("activity requires %s", key)
		}
	}
	var a LoggedActivity
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, err
	}
	return &a, a.Validate()
}
