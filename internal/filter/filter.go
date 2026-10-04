// Package filter evaluates value filters with explicit missing-value evidence.
package filter

import (
	"dosomething/internal/model"
	"fmt"
	"math"
	"strings"
)

// KindBoth selects projects and media together; it is a query-filter value
// only (items themselves always carry a single kind).
const KindBoth = "both"

type EffortConstraint struct {
	Value        string   `json:"value"`
	MaxIntensity *float64 `json:"max_intensity"`
}
type Query struct {
	EffortConstraint *EffortConstraint `json:"effort"`
	Effort           string            `json:"-"`
	MaxIntensity     *float64          `json:"-"`
	SessionSeconds   *float64          `json:"session_seconds,omitempty"`
	FinishSeconds    *float64          `json:"finish_within_seconds,omitempty"`
	Budget           *float64          `json:"budget,omitempty"`
	Type             string            `json:"type,omitempty"`
	Category         string            `json:"category,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Status           string            `json:"status,omitempty"`
	// Mode is the selected activity kind for this query. It is a soft
	// ranking preference (the mode_match scoring term), never a hard
	// filter: it never excludes items and produces no filter evidence.
	Mode string `json:"mode,omitempty"`
	// Engagement is the selected engagement style for this query. Like Mode
	// it is a soft ranking preference (the engagement_match scoring term),
	// never a hard filter.
	Engagement     string   `json:"engagement,omitempty"`
	Tags           []string `json:"tags"`
	NotTags        []string `json:"not_tags"`
	Text           string   `json:"text"`
	Unknown        string   `json:"unknown_policy"`
	IncludeUnready bool     `json:"include_unready"`
	MinScore       float64  `json:"min_score"`
	Limit          int      `json:"limit"`
	Random         bool     `json:"random"`
}

func (q Query) Validate() error {
	if q.Unknown != "include" && q.Unknown != "exclude" {
		return fmt.Errorf("unknown must be include or exclude")
	}
	if q.Kind != "" && q.Kind != KindBoth && !model.Kind(q.Kind).Valid() {
		return fmt.Errorf("invalid kind")
	}
	if q.Status != "" && !model.Status(q.Status).Valid() {
		return fmt.Errorf("invalid status")
	}
	if q.Mode != "" && !model.Mode(q.Mode).Valid() {
		return fmt.Errorf("invalid mode")
	}
	if q.Engagement != "" && !model.Engagement(q.Engagement).Valid() {
		return fmt.Errorf("invalid engagement")
	}
	if q.Effort != "" {
		if _, ok := model.ParseRatingLevel(q.Effort); !ok {
			return fmt.Errorf("effort must be a rating level: %s", strings.Join(levelNames(), ", "))
		}
	}
	if q.Limit < 0 || q.MinScore < 0 || q.MinScore > 1 || math.IsNaN(q.MinScore) {
		return fmt.Errorf("invalid limit or minimum score")
	}
	for _, v := range []*float64{q.SessionSeconds, q.FinishSeconds, q.Budget} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return fmt.Errorf("filter measurements must be finite and nonnegative")
		}
	}
	return nil
}
func (q Query) Match(it *model.Item) (bool, map[string]string) {
	ev := map[string]string{}
	pass := true
	value := func(name string, known, ok bool) {
		if !known {
			ev[name] = "unknown"
			if q.Unknown == "exclude" {
				pass = false
			}
		} else {
			ev[name] = "known"
			if !ok {
				pass = false
			}
		}
	}
	if q.Kind != "" {
		ok := string(it.Kind) == q.Kind
		if q.Kind == KindBoth {
			ok = it.Kind == model.KindProject || it.Kind == model.KindMedia
		}
		value("kind", true, ok)
	}
	if q.Status != "" {
		value("status", true, string(it.Status) == q.Status)
	}
	if q.Type != "" {
		value("type", it.Type != nil, it.Type != nil && *it.Type == q.Type)
	}
	if q.Category != "" {
		value("category", it.Category != nil, it.Category != nil && *it.Category == q.Category)
	}
	if q.Effort != "" {
		v := it.RatingValue(model.PropIntensity)
		ok := v == nil || q.MaxIntensity == nil || *v <= *q.MaxIntensity
		value("effort", v != nil, ok)
	}
	for _, p := range []struct {
		name     string
		bound, v *float64
		scale    float64
	}{{"session", q.SessionSeconds, it.MinSessionDuration, 3600}, {"budget", q.Budget, it.CostLeft, 1}, {"finish_within", q.FinishSeconds, it.RemainingDuration, 3600}} {
		if p.bound != nil {
			if p.name == "finish_within" && it.Ongoing {
				ev[p.name] = "not_applicable"
				pass = false
				continue
			}
			value(p.name, p.v != nil, p.v == nil || *p.v*p.scale <= *p.bound)
		}
	}
	has := func(tag string) bool {
		for _, t := range it.Tags {
			if t == tag {
				return true
			}
		}
		return false
	}
	for _, tag := range q.Tags {
		if !has(tag) {
			pass = false
		}
	}
	for _, tag := range q.NotTags {
		if has(tag) {
			pass = false
		}
	}
	if q.Text != "" {
		text := it.Title
		if it.Notes != nil {
			text += " " + *it.Notes
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(q.Text)) {
			pass = false
		}
		ev["text"] = "known"
	}
	return pass, ev
}

// levelNames lists the canonical rating level names, ascending.
func levelNames() []string {
	names := make([]string, len(model.AllLevels))
	for i, l := range model.AllLevels {
		names[i] = string(l)
	}
	return names
}

// SQL compiles the same value and set-membership rules into a parameterized
// predicate over items AS i. User data is always bound, never interpolated.
func (q Query) SQL() (string, []any) {
	conditions := []string{}
	args := []any{}
	add := func(sql string, v any) { conditions = append(conditions, sql); args = append(args, v) }
	value := func(column, op string, v any) {
		sql := column + op + "?"
		if q.Unknown != "exclude" {
			sql = "(" + column + " IS NULL OR " + sql + ")"
		}
		add(sql, v)
	}
	if q.Kind != "" {
		if q.Kind == KindBoth {
			conditions = append(conditions, "i.kind IN (?, ?)")
			args = append(args, string(model.KindProject), string(model.KindMedia))
		} else {
			add("i.kind = ?", q.Kind)
		}
	}
	if q.Status != "" {
		add("i.status = ?", q.Status)
	}
	if q.Type != "" {
		value("i.type", " = ", q.Type)
	}
	if q.Category != "" {
		value("i.category", " = ", q.Category)
	}
	for _, v := range []struct {
		column string
		n      *float64
		scale  float64
	}{{"i.min_session_duration", q.SessionSeconds, 3600}, {"i.cost_left", q.Budget, 1}, {"i.remaining_duration", q.FinishSeconds, 3600}} {
		if v.n != nil {
			value(v.column, " <= ", *v.n/v.scale)
		}
	}
	if q.FinishSeconds != nil {
		conditions = append(conditions, "i.ongoing = 0")
	}
	if q.Effort != "" {
		expr := "(SELECT value FROM ratings WHERE item_id=i.id AND property='intensity')"
		if q.MaxIntensity != nil {
			value(expr, " <= ", *q.MaxIntensity)
		} else if q.Unknown == "exclude" {
			conditions = append(conditions, expr+" IS NOT NULL")
		}
	}
	for _, tag := range q.Tags {
		add("EXISTS (SELECT 1 FROM item_tags WHERE item_id=i.id AND tag=?)", tag)
	}
	for _, tag := range q.NotTags {
		add("NOT EXISTS (SELECT 1 FROM item_tags WHERE item_id=i.id AND tag=?)", tag)
	}
	// Unicode-aware case folding for --text is evaluated by Match in Go.
	if len(conditions) == 0 {
		return "1", args
	}
	return strings.Join(conditions, " AND "), args
}
