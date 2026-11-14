package output

import (
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/store"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// HistoryItem names an event's item; Title is null when the item no longer
// exists locally (events keep opaque attribution).
type HistoryItem struct {
	ID    string  `json:"id"`
	Title *string `json:"title"`
}

// HistoryEvent is one event as shown by history, newest first.
type HistoryEvent struct {
	ID       string                `json:"id"`
	Type     model.EventType       `json:"type"`
	At       time.Time             `json:"at"`
	Item     *HistoryItem          `json:"item"`
	Activity *model.LoggedActivity `json:"activity"`
	// Suggested lists the returned candidates of a suggested event.
	Suggested []HistoryItem `json:"suggested"`
	// Reverts is the undone event's id (reverted events only).
	Reverts *string `json:"reverts"`
	// RevertedBy is the id of the reverted event that undid this event.
	RevertedBy *string `json:"reverted_by"`

	// The undone event, for text rendering (not part of the JSON contract).
	TargetType     model.EventType       `json:"-"`
	TargetAt       time.Time             `json:"-"`
	TargetActivity *model.LoggedActivity `json:"-"`
}

type History struct {
	SchemaVersion int            `json:"schema_version"`
	Matched       int            `json:"matched_count"`
	Returned      int            `json:"returned_count"`
	Events        []HistoryEvent `json:"events"`
	AllIDs        []string       `json:"-"`
}

// LinkTargets lets text output describe what each reverted event undid.
func (h *History) LinkTargets(byID map[string]*HistoryEvent) {
	for i := range h.Events {
		if r := h.Events[i].Reverts; r != nil {
			if t := byID[*r]; t != nil {
				h.Events[i].TargetType, h.Events[i].TargetAt, h.Events[i].TargetActivity = t.Type, t.At, t.Activity
			}
		}
	}
}

// LocalTime renders an event time for people, in the local zone.
func LocalTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

func historyTitle(it *HistoryItem, ids []string, tr *i18n.Translator) string {
	if it == nil {
		return ""
	}
	title := tr.T("output.history.missing_item")
	if it.Title != nil {
		title = *it.Title
	}
	return tr.T("output.item_reference", "title", title, "id", Prefix(it.ID, ids))
}

// HistoryText prints one line per event, newest first.
func HistoryText(w io.Writer, h History, tr *i18n.Translator) error {
	if len(h.Events) == 0 {
		_, err := fmt.Fprintln(w, tr.T("output.history.empty"))
		return err
	}
	typeWidth := 0
	for _, ev := range h.Events {
		typeWidth = max(typeWidth, utf8.RuneCountInString(string(ev.Type)))
	}
	for _, ev := range h.Events {
		detail := []string{}
		subject := historyTitle(ev.Item, h.AllIDs, tr)
		switch ev.Type {
		case model.EventLogged:
			detail = append(detail, ActivitySummary(ev.Activity, tr))
		case model.EventSuggested:
			names := []string{}
			for i, s := range ev.Suggested {
				if i == 3 {
					names = append(names, "…")
					break
				}
				names = append(names, historyTitle(&s, h.AllIDs, tr))
			}
			subject = tr.T("output.history.suggested", "count", fmt.Sprint(len(ev.Suggested)), "items", strings.Join(names, ", "))
		case model.EventReverted:
			if ev.TargetType != "" {
				what := string(ev.TargetType)
				if ev.TargetType == model.EventLogged {
					what = ActivitySummary(ev.TargetActivity, tr)
				}
				detail = append(detail, tr.T("output.history.undid", "what", what, "at", LocalTime(ev.TargetAt)))
			} else if ev.Reverts != nil {
				detail = append(detail, tr.T("output.history.undid_unknown", "id", *ev.Reverts))
			}
		}
		if ev.RevertedBy != nil {
			detail = append(detail, tr.T("output.history.undone"))
		}
		line := fmt.Sprintf("%s  %-*s  %s", LocalTime(ev.At), typeWidth, ev.Type, subject)
		if d := strings.Join(nonEmpty(detail), " · "); d != "" {
			line += "  " + d
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return nil
}

// ActivitySummary describes a logged session in one phrase.
func ActivitySummary(a *model.LoggedActivity, tr *i18n.Translator) string {
	if a == nil {
		return ""
	}
	parts := []string{}
	if a.Duration != nil {
		part := FormatDuration(*a.Duration, tr)
		if a.RemainingBefore != nil && a.RemainingAfter != nil {
			part += " " + tr.T("output.history.remaining", "before", FormatDuration(*a.RemainingBefore, tr), "after", FormatDuration(*a.RemainingAfter, tr))
		}
		parts = append(parts, part)
	}
	if a.Cost != nil {
		parts = append(parts, tr.T("output.history.cost", "cost", FormatFloat(*a.Cost)))
	}
	return strings.Join(parts, " · ")
}

// UndoMutation reports an undone (or, with dry_run, undoable) session.
type UndoMutation struct {
	SchemaVersion int                `json:"schema_version"`
	Changed       bool               `json:"changed"`
	DryRun        bool               `json:"dry_run"`
	Cancelled     bool               `json:"cancelled"` // declined at the confirmation prompt
	Undo          store.RevertResult `json:"undo"`
	AllIDs        []string           `json:"-"`
}

// UndoText describes the session and each restored value.
func UndoText(w io.Writer, r UndoMutation, tr *i18n.Translator) error {
	if r.Cancelled {
		_, err := fmt.Fprintln(w, tr.T("output.undo.cancelled"))
		return err
	}
	u := r.Undo
	at, _ := time.Parse(time.RFC3339Nano, u.At)
	key := "output.undo.done"
	if r.DryRun {
		key = "output.undo.preview"
	}
	if _, err := fmt.Fprintln(w, tr.T(key, "what", ActivitySummary(&u.Activity, tr), "title", u.Title, "id", Prefix(u.ID, r.AllIDs), "at", LocalTime(at))); err != nil {
		return err
	}
	dur := func(v *float64) string {
		if v == nil {
			return "—"
		}
		return FormatDuration(*v, tr)
	}
	num := func(v *float64) string {
		if v == nil {
			return "—"
		}
		return FormatFloat(*v)
	}
	lines := [][3]string{}
	if !sameFloat(u.RemainingBefore, u.RemainingAfter) {
		lines = append(lines, [3]string{label(tr, "field.remaining_duration", "remaining_duration"), dur(u.RemainingBefore), dur(u.RemainingAfter)})
	}
	if !sameFloat(u.CostLeftBefore, u.CostLeftAfter) {
		lines = append(lines, [3]string{label(tr, "field.cost_left", "cost_left"), num(u.CostLeftBefore), num(u.CostLeftAfter)})
	}
	if u.StatusBefore != u.StatusAfter {
		lines = append(lines, [3]string{label(tr, "field.status", "status"), string(u.StatusBefore), string(u.StatusAfter)})
	}
	for _, l := range lines {
		if _, err := fmt.Fprintf(w, "  %s: %s → %s\n", l[0], l[1], l[2]); err != nil {
			return err
		}
	}
	return nil
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// StatsBucket aggregates logged sessions for one kind and type.
type StatsBucket struct {
	Kind     *model.Kind `json:"kind"` // null when the item no longer exists
	Type     *string     `json:"type"`
	Hours    float64     `json:"hours"`
	Cost     float64     `json:"cost"`
	Sessions int         `json:"sessions"`
}

// StatsWeek aggregates logged time per ISO week (local time).
type StatsWeek struct {
	Week     string  `json:"week"` // ISO 8601, e.g. 2026-W46
	Hours    float64 `json:"hours"`
	Sessions int     `json:"sessions"`
}

// StatsEstimate compares a finished item's total estimate with logged time.
type StatsEstimate struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	EstimateHours float64 `json:"estimate_hours"`
	ActualHours   float64 `json:"actual_hours"`
	Ratio         float64 `json:"ratio"` // actual / estimate
}

// Stats summarizes activity in a period. Undone sessions and completions are
// excluded.
type Stats struct {
	SchemaVersion int             `json:"schema_version"`
	Since         *string         `json:"since"` // inclusive local date; null = all time
	Until         string          `json:"until"` // inclusive local date
	Sessions      int             `json:"sessions"`
	Hours         float64         `json:"hours_logged"`
	Cost          float64         `json:"cost_logged"`
	Started       int             `json:"started"`
	Completed     int             `json:"completed"`
	Dropped       int             `json:"dropped"`
	ByType        []StatsBucket   `json:"by_type"`
	ByWeek        []StatsWeek     `json:"by_week"`
	Estimates     []StatsEstimate `json:"estimates"`
	MedianRatio   *float64        `json:"median_estimate_ratio"`
}

// StatsText prints the summary, per-type and per-week time, and estimates.
func StatsText(w io.Writer, s Stats, tr *i18n.Translator) error {
	var b strings.Builder
	if s.Since == nil {
		b.WriteString(tr.T("output.stats.period_all", "until", s.Until) + "\n")
	} else {
		b.WriteString(tr.T("output.stats.period", "since", *s.Since, "until", s.Until) + "\n")
	}
	b.WriteString("  " + tr.TN("output.stats.time", s.Sessions, "time", FormatDuration(s.Hours, tr)) + "\n")
	if s.Cost > 0 {
		b.WriteString("  " + tr.T("output.stats.cost", "cost", FormatFloat(s.Cost)) + "\n")
	}
	b.WriteString("  " + tr.T("output.stats.status", "started", fmt.Sprint(s.Started), "completed", fmt.Sprint(s.Completed), "dropped", fmt.Sprint(s.Dropped)) + "\n")

	if len(s.ByType) > 0 {
		names := make([]string, len(s.ByType))
		width := 0
		for i, t := range s.ByType {
			kind, typ := tr.T("output.stats.unknown_item"), ""
			if t.Kind != nil {
				kind = string(*t.Kind)
			}
			if t.Type != nil {
				typ = " · " + *t.Type
			}
			names[i] = kind + typ
			width = max(width, utf8.RuneCountInString(names[i]))
		}
		b.WriteString("\n" + tr.T("output.stats.by_type") + "\n")
		for i, t := range s.ByType {
			b.WriteString(fmt.Sprintf("  %s  %8s  %s\n", padRight(names[i], width), FormatDuration(t.Hours, tr), tr.TN("output.stats.sessions", t.Sessions)))
		}
	}
	if len(s.ByWeek) > 0 {
		b.WriteString("\n" + tr.T("output.stats.by_week") + "\n")
		top := 0.0
		for _, wk := range s.ByWeek {
			top = max(top, wk.Hours)
		}
		for _, wk := range s.ByWeek {
			bar := ""
			if top > 0 {
				bar = strings.Repeat("█", max(1, int(wk.Hours/top*20+0.5)))
			}
			b.WriteString(fmt.Sprintf("  %s  %8s  %s\n", wk.Week, FormatDuration(wk.Hours, tr), bar))
		}
	}
	if len(s.Estimates) > 0 {
		width := 0
		for _, e := range s.Estimates {
			width = max(width, utf8.RuneCountInString(e.Title))
		}
		b.WriteString("\n" + tr.T("output.stats.estimates") + "\n")
		for _, e := range s.Estimates {
			b.WriteString("  " + padRight(e.Title, width) + "  " + tr.T("output.stats.estimate", "estimate", FormatDuration(e.EstimateHours, tr), "actual", FormatDuration(e.ActualHours, tr), "ratio", fmt.Sprintf("%.2f", e.Ratio)) + "\n")
		}
		if s.MedianRatio != nil {
			b.WriteString("  " + tr.TN("output.stats.median", len(s.Estimates), "ratio", fmt.Sprintf("%.2f", *s.MedianRatio)) + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
