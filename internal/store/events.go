package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"dosomething/internal/model"
)

// AppendEvent inserts an append-only event row. The row is never updated after
// insert. If ev.ID is empty a UUIDv7 is minted; if ev.At is zero the wall clock
// is used. ItemID is opaque attribution with no foreign key.
func (s *Store) AppendEvent(ev model.Event) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id, err := s.appendEventTx(tx, ev)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// appendEventTx inserts an event row into the caller's transaction (see
// AppendEvent for the id/at defaulting rules).
func (s *Store) appendEventTx(tx *sql.Tx, ev model.Event) (string, error) {
	id := ev.ID
	at := ev.At
	if at.IsZero() {
		t, _ := parseTime(nowRFC3339())
		at = t
	}
	itemID := ""
	if ev.ItemID != nil {
		itemID = *ev.ItemID
	}
	var itemIDs string
	if len(ev.ItemIDs) > 0 || ev.Type == model.EventSuggested {
		b, err := json.Marshal(ev.ItemIDs)
		if err != nil {
			return "", err
		}
		itemIDs = string(b)
	}
	query := ""
	if ev.Query != nil {
		query = *ev.Query
	}
	var activity any
	if ev.Type == model.EventLogged {
		if ev.Activity == nil || ev.ItemID == nil || *ev.ItemID == "" {
			return "", fmt.Errorf("logged event requires activity and item_id")
		}
		if err := ev.Activity.Validate(); err != nil {
			return "", err
		}
		b, err := json.Marshal(ev.Activity)
		if err != nil {
			return "", err
		}
		activity = string(b)
	} else if ev.Activity != nil {
		return "", fmt.Errorf("activity is only valid for logged events")
	}
	querySQL := `INSERT INTO events(id, item_id, item_ids, type, at, query, activity) VALUES(?,?,?,?,?,?,?)`
	args := []any{nullIfEmpty(itemID), nullIfEmpty(itemIDs), string(ev.Type), at.UTC().Format(time.RFC3339Nano), nullIfEmpty(query), activity}
	if id == "" {
		var err error
		id, err = insertGenerated(tx, newEventID, querySQL, args...)
		if err != nil {
			return "", fmt.Errorf("store: append event: %w", err)
		}
	} else if _, err := tx.Exec(querySQL, append([]any{id}, args...)...); err != nil {
		return "", fmt.Errorf("store: append event: %w", err)
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EventsByType returns all events of a type, most recent first.
func (s *Store) EventsByType(t model.EventType) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT id, item_id, item_ids, type, at, query, activity FROM events WHERE type=? ORDER BY at DESC, id`, string(t))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// AllEvents returns every event, most recent first (used by sync reconciliation).
func (s *Store) AllEvents() ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT id, item_id, item_ids, type, at, query, activity FROM events ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func scanEvent(rows *sql.Rows) (model.Event, error) {
	var (
		ev       model.Event
		itemID   sql.NullString
		itemIDs  sql.NullString
		at       string
		query    sql.NullString
		activity sql.NullString
	)
	if err := rows.Scan(&ev.ID, &itemID, &itemIDs, &ev.Type, &at, &query, &activity); err != nil {
		return ev, err
	}
	if activity.Valid {
		var err error
		ev.Activity, err = model.DecodeLoggedActivity(activity.String)
		if err != nil {
			return ev, err
		}
	}
	if itemID.Valid {
		v := itemID.String
		ev.ItemID = &v
	}
	if itemIDs.Valid && itemIDs.String != "" {
		_ = json.Unmarshal([]byte(itemIDs.String), &ev.ItemIDs)
	}
	if query.Valid && query.String != "" {
		v := query.String
		ev.Query = &v
	}
	if t, err := parseTime(at); err == nil {
		ev.At = t
	}
	return ev, nil
}

// DeleteEvents removes all events matching a set of ids (used by the sync
// bootstrap/take-remote path to replace the log, and by corruption handling).
// The append-only rule is about *live* operation; a deliberate whole-store
// replacement may reset the log.
func (s *Store) DeleteEvents(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if _, err := s.db.Exec(`DELETE FROM events WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ClearEvents removes every event row (whole-store replacement only).
func (s *Store) ClearEvents() error {
	_, err := s.db.Exec(`DELETE FROM events`)
	return err
}

// LastSuggestedFor returns, for each id in want, the most recent time it was
// returned in a recorded `suggested` event (as item_id or within item_ids).
// Items never suggested are absent from the returned map.
func (s *Store) LastSuggestedFor(want map[string]bool) (map[string]time.Time, error) {
	evs, err := s.EventsByType(model.EventSuggested)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, ev := range evs {
		// evs are ordered most-recent first, so the first hit per id is the
		// latest; later (older) hits must not overwrite it.
		mark := func(id string) {
			if want[id] {
				if old, ok := out[id]; !ok || ev.At.After(old) {
					out[id] = ev.At
				}
			}
		}
		if ev.ItemID != nil {
			mark(*ev.ItemID)
		}
		for _, id := range ev.ItemIDs {
			mark(id)
		}
	}
	return out, nil
}
