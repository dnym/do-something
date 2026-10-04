package store

import (
	"database/sql"
	"fmt"
	"strings"

	"dosomething/internal/model"
)

// Transition is one status verb: the CLI's start / done / drop / reopen.
type Transition int

// The status verbs, in stable order.
const (
	Start Transition = iota
	Done
	Drop
	Reopen
)

// TransitionOf maps a CLI verb name to its Transition.
func TransitionOf(name string) (Transition, bool) {
	switch name {
	case "start":
		return Start, true
	case "done":
		return Done, true
	case "drop":
		return Drop, true
	case "reopen":
		return Reopen, true
	}
	return 0, false
}

func transitionVerbName(v Transition) string {
	switch v {
	case Start:
		return "start"
	case Done:
		return "done"
	case Drop:
		return "drop"
	case Reopen:
		return "reopen"
	}
	return "transition"
}

// StatusEvent is the event appended when an item's status becomes to: the
// same event the matching transition verb writes (see transitionTarget).
// not_started, reopen's target, appends none.
func StatusEvent(to model.Status) model.EventType {
	switch to {
	case model.StatusInProgress:
		return model.EventStarted
	case model.StatusDone:
		return model.EventCompleted
	case model.StatusDropped:
		return model.EventDropped
	}
	return ""
}

// transitionTarget is the status a verb moves an item to and the event it
// appends when the status actually changes. Reopen appends no event: the event
// includes a separate logged event for activity sessions.
var transitionTarget = map[Transition]struct {
	to    model.Status
	event model.EventType
}{
	Start:  {model.StatusInProgress, model.EventStarted},
	Done:   {model.StatusDone, model.EventCompleted},
	Drop:   {model.StatusDropped, model.EventDropped},
	Reopen: {model.StatusNotStarted, ""},
}

// TransitionResult is the outcome for one id.
type TransitionResult struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	From    model.Status `json:"before"`
	To      model.Status `json:"after"`
	Changed bool         `json:"changed"`
}

// IDProblem pairs an offending id with its machine-readable code.
type IDProblem struct {
	Candidates []string `json:"candidates,omitempty"`
	ID         string   `json:"id"`
	Code       string   `json:"code"` // INVALID_ID | INVALID_STATE
}

// TransitionError lists every id that failed an all-or-nothing multi-id
// mutation, each with its own code (decision 11: on failure the error lists
// every offending id with its code).
type TransitionError struct {
	Verb     Transition  `json:"verb"`
	Problems []IDProblem `json:"problems"`
}

func (e *TransitionError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: ", transitionVerbName(e.Verb))
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s: %s", p.ID, p.Code)
		if len(p.Candidates) > 0 {
			fmt.Fprintf(&b, " (candidates: %s)", strings.Join(p.Candidates, ", "))
		}
	}
	return b.String()
}

// TransitionStatus applies a status verb to every id in one all-or-nothing
// transaction. Every verb is idempotent: an item already at the target status
// reports Changed=false and no event. On any failure the transaction is rolled
// back and a TransitionError lists every offending id (unknown id →
// INVALID_ID).
//
// Allowed sources (plan §7 table): start from any except in_progress, done and
// drop from any, reopen from any except not_started — the "except" rows are exactly
// the current==target no-ops, so no source status is ever an error.
func (s *Store) TransitionStatus(verb Transition, ids []string) ([]TransitionResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Pass 1: load every id, collecting per-id problems so the failure lists
	// all of them at once.
	type pending struct {
		id   string
		item *model.Item
	}
	var pendings []pending
	var problems []IDProblem
	for _, id := range ids {
		it, err := s.getItemTx(tx, id)
		if err != nil {
			if ItemNotFound(err) {
				problems = append(problems, IDProblem{ID: id, Code: "INVALID_ID"})
				continue
			}
			return nil, err
		}
		if !it.Status.Valid() {
			problems = append(problems, IDProblem{ID: id, Code: "INVALID_STATE"})
			continue
		}
		pendings = append(pendings, pending{id: id, item: it})
	}
	if len(problems) > 0 {
		return nil, &TransitionError{Verb: verb, Problems: problems}
	}

	now := nowRFC3339()
	t, _ := parseTime(now)
	tt := transitionTarget[verb]
	results := make([]TransitionResult, 0, len(pendings))
	for _, p := range pendings {
		changed := p.item.Status != tt.to
		if changed {
			if _, err := tx.Exec(`UPDATE items SET status=?, updated_at=? WHERE id=?`, string(tt.to), now, p.id); err != nil {
				return nil, fmt.Errorf("store: %s %s: %w", transitionVerbName(verb), p.id, err)
			}
			if tt.event != "" {
				id := p.id
				if _, err := s.appendEventTx(tx, model.Event{ItemID: &id, Type: tt.event, At: t}); err != nil {
					return nil, err
				}
			}
		}
		results = append(results, TransitionResult{ID: p.id, Title: p.item.Title, From: p.item.Status, To: tt.to, Changed: changed})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

// Blocker is a dependency target that does not unblock its item: its status is
// not done (a dropped prerequisite is still a blocker, decision 10).
type Blocker struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Status model.Status `json:"status"`
}

// BlockedBy returns the item's dependency targets whose status is not done, in
// deterministic (id) order. An item is ready exactly when the result is empty;
// dropped blockers are reported as such so the user can reopen or dissolve the
// edge explicitly.
func (s *Store) BlockedBy(id string) ([]Blocker, error) {
	deps, err := s.Dependencies(id)
	if err != nil {
		return nil, err
	}
	out := []Blocker{}
	for _, d := range deps {
		var title, st string
		err := s.db.QueryRow(`SELECT title,status FROM items WHERE id=?`, d).Scan(&title, &st)
		if err == sql.ErrNoRows {
			// A dangling edge is impossible while FK checks pass; skip
			// defensively rather than failing the readiness report.
			continue
		}
		if err != nil {
			return nil, err
		}
		if model.Status(st) != model.StatusDone {
			out = append(out, Blocker{ID: d, Title: title, Status: model.Status(st)})
		}
	}
	return out, nil
}
