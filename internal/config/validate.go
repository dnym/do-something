package config

import (
	"dosomething/internal/model"
	"fmt"
	"math"
	"strings"
)

// ValidateValue rejects malformed calibration instead of silently changing its type.
func ValidateValue(key, value string) error {
	b := BuiltinValues()
	hint, ok := b[key]
	if !ok {
		switch {
		case strings.HasPrefix(key, "scoring.weights."), strings.HasPrefix(key, "scoring.k."), strings.HasPrefix(key, "defaults.min_session_duration."), strings.HasPrefix(key, "scoring.half_life."):
			hint = float64(0)
		case strings.HasPrefix(key, "vocabulary."):
			hint = []string{}
		default:
			return fmt.Errorf("unknown config key %q", key)
		}
	}
	v, e := parseValue(value, hint)
	if e != nil {
		return e
	}
	if n, ok := v.(float64); ok {
		if math.IsNaN(n) || n < 0 || math.IsInf(n, 0) && !strings.HasPrefix(key, "scoring.half_life.") {
			return fmt.Errorf("invalid nonnegative finite value for %s", key)
		}
		if (key == "scoring.beta" || key == "scoring.prior" || strings.HasPrefix(key, "scoring.effort.")) && n > 1 {
			return fmt.Errorf("%s must be in [0,1]", key)
		}
		if strings.HasPrefix(key, "scoring.k.") && n <= 0 {
			return fmt.Errorf("half-saturation constants must be positive")
		}
	}
	if key == "scoring.duration_anchors" {
		v := v.([]float64)
		if len(v) != 4 {
			return fmt.Errorf("duration_anchors requires four values")
		}
		for _, n := range v {
			if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("invalid duration anchor")
			}
		}
	}
	if key == "output.default_limit" && v.(int) < 0 {
		return fmt.Errorf("negative limit")
	}
	if key == "output.unknown_policy" && value != "include" && value != "exclude" {
		return fmt.Errorf("unknown policy must be include or exclude")
	}
	if strings.HasPrefix(key, "scoring.group_members.") {
		for _, p := range v.([]string) {
			found := false
			for _, prop := range model.AllProperties() {
				found = found || p == string(prop)
			}
			if !found {
				return fmt.Errorf("unknown group property %s", p)
			}
		}
	}
	return nil
}
