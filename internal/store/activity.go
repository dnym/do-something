package store

import (
	"dosomething/internal/model"
	"fmt"
	"math"
)

// LogError describes invalid session input/state using a stable localized reason.
type LogError struct{ Reason string }

func (e *LogError) Error() string {
	switch e.Reason {
	case "ongoing_complete":
		return "ongoing items cannot be completed by logging; omit --complete"
	case "not_in_progress":
		return "start the item before logging, or use --force"
	case "complete_inactive":
		return "done or dropped items cannot be completed by logging"
	case "complete_without_time":
		return "--complete requires logged time"
	case "remaining_unknown":
		return "set remaining_duration before logging time"
	case "cost_unknown":
		return "set cost_left before logging cost"
	case "session_unknown":
		return "specify time passed or set a positive min_session_duration"
	case "cost_invalid":
		return "cost must be positive and finite"
	default:
		return "time passed must be positive and finite"
	}
}

// PrepareLog validates input and resolves the minimum-session fallback without writes.
func PrepareLog(it *model.Item, duration, cost *float64, force bool) (model.LoggedActivity, error) {
	fail := func(reason string) (model.LoggedActivity, error) { return model.LoggedActivity{}, &LogError{reason} }
	if it.Status != model.StatusInProgress && !force {
		return fail("not_in_progress")
	}
	if duration == nil && cost == nil {
		duration = it.MinSessionDuration
		if duration == nil || *duration <= 0 {
			return fail("session_unknown")
		}
	}
	a := model.LoggedActivity{Ongoing: it.Ongoing}
	if duration != nil {
		if *duration <= 0 || math.IsNaN(*duration) || math.IsInf(*duration, 0) {
			return fail("duration_invalid")
		}
		if !it.Ongoing && it.RemainingDuration == nil {
			return fail("remaining_unknown")
		}
		a.Duration = model.FloatPtr(*duration)
		if !it.Ongoing {
			a.RemainingBefore = model.FloatPtr(*it.RemainingDuration)
			a.RemainingAfter = model.FloatPtr(model.RemainingAfterLog(*it.RemainingDuration, *duration))
		}
	}
	if cost != nil {
		if *cost <= 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
			return fail("cost_invalid")
		}
		if it.CostLeft == nil && !it.Ongoing {
			return fail("cost_unknown")
		}
		a.Cost = model.FloatPtr(*cost)
		if it.CostLeft != nil {
			a.CostBefore = model.FloatPtr(*it.CostLeft)
			a.CostAfter = model.FloatPtr(model.RemainingAfterLog(*it.CostLeft, *cost))
		}
	}
	return a, a.Validate()
}

type LogResult struct {
	ID                string               `json:"id"`
	Title             string               `json:"title"`
	EventID           string               `json:"event_id"`
	Activity          model.LoggedActivity `json:"activity"`
	StatusBefore      model.Status         `json:"status_before"`
	StatusAfter       model.Status         `json:"status_after"`
	Completed         bool                 `json:"completed"`
	EstimatedProgress *float64             `json:"estimated_progress_percent"`
}

// LogTime records a session, adjusts remaining time, and optionally completes at
// zero, atomically. The caller holds the per-store application lock. Each call
// represents a distinct session, including when remaining time is already zero.
func (s *Store) LogTime(id string, duration, cost *float64, complete, force bool) (LogResult, error) {
	var result LogResult
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	it, err := s.getItemTx(tx, id)
	if err != nil {
		return result, err
	}
	a, err := PrepareLog(it, duration, cost, force)
	if err != nil {
		return result, err
	}
	if complete && it.Ongoing {
		return result, &LogError{Reason: "ongoing_complete"}
	}
	if complete && a.Duration == nil {
		return result, &LogError{Reason: "complete_without_time"}
	}
	if complete && (it.Status == model.StatusDone || it.Status == model.StatusDropped) {
		return result, &LogError{Reason: "complete_inactive"}
	}
	result = LogResult{ID: id, Title: it.Title, Activity: a, StatusBefore: it.Status, StatusAfter: it.Status}
	if complete && a.RemainingAfter != nil && *a.RemainingAfter == 0 {
		result.StatusAfter = model.StatusDone
		result.Completed = true
	}
	now := nowRFC3339()
	at, _ := parseTime(now)
	remaining, costLeft := it.RemainingDuration, it.CostLeft
	if a.Duration != nil {
		remaining = a.RemainingAfter
	}
	if a.Cost != nil && a.CostBefore != nil {
		costLeft = a.CostAfter
	}
	if _, err := tx.Exec(`UPDATE items SET remaining_duration=?, cost_left=?, status=?, updated_at=? WHERE id=?`, remaining, costLeft, result.StatusAfter, now, id); err != nil {
		return result, fmt.Errorf("store: log time: %w", err)
	}
	result.EventID, err = s.appendEventTx(tx, model.Event{ItemID: &id, Type: model.EventLogged, At: at, Activity: &a})
	if err != nil {
		return result, err
	}
	if result.Completed {
		if _, err := s.appendEventTx(tx, model.Event{ItemID: &id, Type: model.EventCompleted, At: at}); err != nil {
			return result, err
		}
	}
	it.RemainingDuration = remaining
	result.EstimatedProgress = it.EstimatedProgress()
	return result, tx.Commit()
}
