package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"dosomething/internal/model"
)

// UndoError describes why a logged session cannot be undone, using a stable
// reason code (an INVALID_STATE condition).
type UndoError struct {
	Reason  string `json:"reason"`
	EventID string `json:"event_id,omitempty"`
}

func (e *UndoError) Error() string {
	switch e.Reason {
	case "nothing_to_undo":
		return "no logged session to undo"
	case "already_undone":
		return "this session has already been undone"
	case "not_a_log":
		return "only logged sessions can be undone"
	case "item_missing":
		return "the session's item no longer exists"
	default: // changed_since
		return "the item's remaining time, cost, or status changed after this session; adjust it with edit instead"
	}
}

// RevertResult describes an undone session and the restored item state.
type RevertResult struct {
	ID              string               `json:"id"`
	Title           string               `json:"title"`
	EventID         string               `json:"event_id"` // the undone logged event
	At              string               `json:"at"`       // when the session was logged
	Activity        model.LoggedActivity `json:"activity"`
	RevertEventIDs  []string             `json:"revert_event_ids"`
	Uncompleted     bool                 `json:"uncompleted"` // the log's completion was undone too
	StatusBefore    model.Status         `json:"status_before"`
	StatusAfter     model.Status         `json:"status_after"`
	RemainingBefore *float64             `json:"remaining_before"`
	RemainingAfter  *float64             `json:"remaining_after"`
	CostLeftBefore  *float64             `json:"cost_left_before"`
	CostLeftAfter   *float64             `json:"cost_left_after"`
}

// RevertedIDs returns the ids of every event undone by a `reverted` event.
func (s *Store) RevertedIDs() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT reverts, id FROM events WHERE type='reverted'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var target, id string
		if err := rows.Scan(&target, &id); err != nil {
			return nil, err
		}
		out[target] = id
	}
	return out, rows.Err()
}

// LastUndoableLog returns the most recent logged session that has not been
// undone, optionally restricted to one item.
func (s *Store) LastUndoableLog(itemID string) (string, error) {
	q := `SELECT id FROM events e WHERE type='logged'
	  AND NOT EXISTS (SELECT 1 FROM events r WHERE r.type='reverted' AND r.reverts=e.id)`
	args := []any{}
	if itemID != "" {
		q += ` AND item_id=?`
		args = append(args, itemID)
	}
	var id string
	err := s.db.QueryRow(q+` ORDER BY at DESC, id DESC LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &UndoError{Reason: "nothing_to_undo"}
	}
	return id, err
}

// RevertLog undoes one logged session atomically: it restores the remaining
// time and cost the session consumed, reverses a completion the log caused,
// and appends `reverted` events (the log itself is never deleted, so the
// undo survives sync's event union). It refuses when the item changed since,
// so a later edit or session is never silently overwritten. With dryRun it
// only reports what would happen. The caller holds the application lock.
func (s *Store) RevertLog(eventID string, dryRun bool) (RevertResult, error) {
	var res RevertResult
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	ev, err := scanEvent(tx.QueryRow(`SELECT `+eventColumns+` FROM events WHERE id=?`, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return res, &UndoError{Reason: "nothing_to_undo", EventID: eventID}
	}
	if err != nil {
		return res, err
	}
	if ev.Type != model.EventLogged || ev.Activity == nil || ev.ItemID == nil {
		return res, &UndoError{Reason: "not_a_log", EventID: eventID}
	}
	if reverted, err := isReverted(tx, eventID); err != nil || reverted {
		if err != nil {
			return res, err
		}
		return res, &UndoError{Reason: "already_undone", EventID: eventID}
	}
	it, err := s.getItemTx(tx, *ev.ItemID)
	if ItemNotFound(err) {
		return res, &UndoError{Reason: "item_missing", EventID: eventID}
	}
	if err != nil {
		return res, err
	}
	a := *ev.Activity
	res = RevertResult{ID: it.ID, Title: it.Title, EventID: eventID, At: ev.At.UTC().Format(time.RFC3339Nano), Activity: a, RevertEventIDs: []string{},
		StatusBefore: it.Status, StatusAfter: it.Status, RemainingBefore: it.RemainingDuration, RemainingAfter: it.RemainingDuration, CostLeftBefore: it.CostLeft, CostLeftAfter: it.CostLeft}
	changed := func() (RevertResult, error) { return res, &UndoError{Reason: "changed_since", EventID: eventID} }
	if a.Duration != nil && !a.Ongoing {
		if !sameValue(it.RemainingDuration, a.RemainingAfter) {
			return changed()
		}
		res.RemainingAfter = model.FloatPtr(*a.RemainingBefore)
	}
	if a.Cost != nil && a.CostBefore != nil {
		if !sameValue(it.CostLeft, a.CostAfter) {
			return changed()
		}
		res.CostLeftAfter = model.FloatPtr(*a.CostBefore)
	}
	// LogTime records the completion it causes as a `completed` event with
	// the same timestamp as the session.
	var completion string
	err = tx.QueryRow(`SELECT id FROM events e WHERE type='completed' AND item_id=? AND at=?
	  AND NOT EXISTS (SELECT 1 FROM events r WHERE r.type='reverted' AND r.reverts=e.id)`,
		it.ID, ev.At.UTC().Format(time.RFC3339Nano)).Scan(&completion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	if completion != "" {
		if it.Status != model.StatusDone {
			return changed()
		}
		res.Uncompleted = true
		res.StatusAfter = model.StatusInProgress
		if a.StatusBefore != nil && model.Status(*a.StatusBefore).Valid() {
			res.StatusAfter = model.Status(*a.StatusBefore)
		}
	}
	if dryRun {
		return res, nil
	}
	now := nowRFC3339()
	at, _ := parseTime(now)
	if _, err := tx.Exec(`UPDATE items SET remaining_duration=?, cost_left=?, status=?, updated_at=? WHERE id=?`, res.RemainingAfter, res.CostLeftAfter, res.StatusAfter, now, it.ID); err != nil {
		return res, fmt.Errorf("store: undo log: %w", err)
	}
	targets := []string{eventID}
	if completion != "" {
		targets = append(targets, completion)
	}
	for _, target := range targets {
		id, err := s.appendEventTx(tx, model.Event{ItemID: &it.ID, Type: model.EventReverted, At: at, Reverts: &target})
		if err != nil {
			return res, err
		}
		res.RevertEventIDs = append(res.RevertEventIDs, id)
	}
	return res, tx.Commit()
}

func isReverted(tx *sql.Tx, eventID string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM events WHERE type='reverted' AND reverts=?`, eventID).Scan(&n)
	return n > 0, err
}

// sameValue compares stored estimates; both unknown counts as equal.
func sameValue(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) <= 1e-9*math.Max(1, math.Abs(*b))
}
