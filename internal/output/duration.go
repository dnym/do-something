package output

import (
	"dosomething/internal/i18n"
	"encoding/json"
	"math"
	"strings"
)

// FormatDuration renders active hours in seconds, minutes, or hours. Hours
// remain the largest unit, since active time is not elapsed calendar time.
func FormatDuration(hours float64, tr *i18n.Translator) string {
	value, unit := hours, "hours"
	switch {
	case hours == 0:
		value, unit = 0, "minutes"
	case hours < 1.0/60:
		value, unit = hours*3600, "seconds"
		if math.Round(value*100)/100 >= 60 {
			value, unit = 1, "minutes"
		}
	case hours < 1:
		value, unit = hours*60, "minutes"
		if math.Round(value*100)/100 >= 60 {
			value, unit = 1, "hours"
		}
	}
	number := strings.TrimRight(strings.TrimRight(FormatFloat(value), "0"), ".")
	parts := strings.SplitN(number, ".", 2)
	whole := parts[0]
	groups := []string{}
	for len(whole) > 3 {
		groups = append([]string{whole[len(whole)-3:]}, groups...)
		whole = whole[:len(whole)-3]
	}
	groups = append([]string{whole}, groups...)
	parts[0] = strings.Join(groups, tr.T("output.digit_group_separator"))
	return tr.T("output.duration."+unit, "value", strings.Join(parts, "."))
}

func durationField(key string) bool {
	switch key {
	case "total_duration", "remaining_duration", "min_session_duration", "duration", "remaining_before", "remaining_after":
		return true
	}
	return false
}

func reportScalar(key string, value any, fields map[string]any, tr *i18n.Translator) any {
	n, ok := value.(json.Number)
	if !ok {
		return textScalar(value)
	}
	hours, err := n.Float64()
	if err != nil {
		return textScalar(value)
	}
	property, _ := fields["property"].(string)
	if durationField(key) || key == "value" && durationField(property) {
		return FormatDuration(hours, tr)
	}
	if key == "session_seconds" || key == "finish_within_seconds" {
		return FormatDuration(hours/3600, tr)
	}
	return textScalar(value)
}
