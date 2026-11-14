package store

import (
	"time"

	"dosomething/internal/model"
)

// SnoozeResult reports one item's snooze before and after the operation.
type SnoozeResult struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Status  string     `json:"status"`
	Before  *time.Time `json:"before"`
	After   *time.Time `json:"after"`
	Changed bool       `json:"changed"`
}

// Snooze sets (until non-nil) or clears (until nil) the snooze of every item
// in one transaction. Only not_started and in_progress items can be snoozed;
// clearing works for any status. All references are checked before writing.
func (s *Store) Snooze(ids []string, until *time.Time) ([]SnoozeResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := []SnoozeResult{}
	problems := []IDProblem{}
	items := []*model.Item{}
	for _, id := range ids {
		it, err := s.getItemTx(tx, id)
		if ItemNotFound(err) {
			problems = append(problems, IDProblem{ID: id, Code: "INVALID_ID"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if until != nil && it.Status != model.StatusNotStarted && it.Status != model.StatusInProgress {
			problems = append(problems, IDProblem{ID: id, Code: "INVALID_STATE"})
			continue
		}
		items = append(items, it)
	}
	if len(problems) > 0 {
		return nil, &SnoozeError{Problems: problems}
	}
	now := nowRFC3339()
	for _, it := range items {
		r := SnoozeResult{ID: it.ID, Title: it.Title, Status: string(it.Status), Before: it.SnoozedUntil, After: until}
		var value any
		if until != nil {
			value = until.UTC().Format(time.RFC3339Nano)
		}
		r.Changed = !sameInstant(it.SnoozedUntil, until)
		if r.Changed {
			if _, err := tx.Exec(`UPDATE items SET snoozed_until=?, updated_at=? WHERE id=?`, value, now, it.ID); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, tx.Commit()
}

// SnoozeError lists every reference a snooze could not apply to: unknown
// items (INVALID_ID) and finished items (INVALID_STATE).
type SnoozeError struct {
	Problems []IDProblem `json:"problems"`
}

func (e *SnoozeError) Error() string {
	msg := problemsText("snooze", e.Problems)
	for _, p := range e.Problems {
		if p.Code == "INVALID_STATE" {
			return msg + " (done and dropped items cannot be snoozed)"
		}
	}
	return msg
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
