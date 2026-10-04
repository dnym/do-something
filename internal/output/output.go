package output

import (
	"dosomething/internal/filter"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/scoring"
	"dosomething/internal/store"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

var canonical, _ = i18n.New("en")

type Reason struct {
	Factor       string  `json:"factor"`
	Label        string  `json:"label"`
	Contribution float64 `json:"contribution"`
	Key          string  `json:"-"`
}
type Readiness struct {
	Ready     bool            `json:"ready"`
	BlockedBy []store.Blocker `json:"blocked_by"`
}
type Item struct {
	Ongoing  bool         `json:"ongoing"`
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Kind     model.Kind   `json:"kind"`
	Type     *string      `json:"type"`
	Category *string      `json:"category"`
	Status   model.Status `json:"status"`
	Tags     []string     `json:"tags"`
	// Modes are the built-in activity kinds the item carries; empty = unclassified.
	Modes []string `json:"modes"`
	// Engagements are the built-in engagement styles the item carries; empty = unclassified.
	Engagements       []string                       `json:"engagements"`
	Score             *float64                       `json:"score"`
	Readiness         Readiness                      `json:"readiness"`
	Reasons           []Reason                       `json:"reasons"`
	Breakdown         *scoring.Result                `json:"score_breakdown"`
	Evidence          map[string]string              `json:"filter_evidence"`
	Properties        map[string]any                 `json:"properties"`
	States            map[string]model.PropertyState `json:"property_states"`
	URL               *string                        `json:"url"`
	Notes             *string                        `json:"notes"`
	Dependencies      []string                       `json:"dependencies"`
	EstimatedProgress *float64                       `json:"estimated_progress_percent"`
	LegacyID          *int64                         `json:"legacy_id"`
}
type Result struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Recorded      bool         `json:"recorded"`
	Filters       filter.Query `json:"filters"`
	Matched       int          `json:"matched_count"`
	Returned      int          `json:"returned_count"`
	Items         []Item       `json:"items"`
	AllIDs        []string     `json:"-"` // Display prefixes must be unique across the store.
}

func NewItem(it *model.Item, r scoring.Result, blockers []store.Blocker, evidence map[string]string, explain bool, beta float64) Item {
	if blockers == nil {
		blockers = []store.Blocker{}
	}
	o := Item{Ongoing: it.Ongoing, ID: it.ID, Title: it.Title, Kind: it.Kind, Type: it.Type, Category: it.Category, Status: it.Status, Tags: it.SortedTags(), Modes: it.Modes, Engagements: it.Engagements, Readiness: Readiness{len(blockers) == 0, blockers}, Reasons: []Reason{}, Evidence: evidence, Properties: map[string]any{}, States: map[string]model.PropertyState{}, URL: it.URL, Notes: it.Notes, Dependencies: it.Dependencies, LegacyID: it.LegacyID, EstimatedProgress: it.EstimatedProgress()}
	if o.Modes == nil {
		o.Modes = []string{}
	}
	if o.Engagements == nil {
		o.Engagements = []string{}
	}
	if o.Dependencies == nil {
		o.Dependencies = []string{}
	}
	if o.Evidence == nil {
		o.Evidence = map[string]string{}
	}
	for _, p := range model.AllProperties() {
		state := it.PropertyStateOf(p)
		o.States[string(p)] = state
		o.Properties[string(p)] = nil
		if state == model.StateKnown {
			switch {
			case p.IsRating():
				o.Properties[string(p)] = it.RatingValue(p)
			case p == model.PropDeadline:
				o.Properties[string(p)] = it.Deadline.Format("2006-01-02")
			default:
				o.Properties[string(p)] = it.MeasurementValue(p)
			}
		}
	}
	o.Score = &r.Final
	if explain {
		o.Breakdown = &r
	}
	if len(r.Terms) == 0 {
		beta = 0
	}
	en := canonical
	for _, g := range r.Groups {
		key := "value_estimate"
		if g.D >= 0.67 {
			key = "high_value"
		}
		if g.Group == model.GroupInterest {
			key = "moderate_interest"
			if g.D >= 0.67 {
				key = "strong_interest"
			}
		}
		if g.Group == model.GroupCommitment {
			key = "commitment_estimate"
			if g.D >= 0.67 {
				key = "low_commitment"
			}
		}
		known := false
		for _, member := range g.Members {
			if member.State == model.StateKnown && member.Note == "" {
				known = true
			}
		}
		if !known {
			key = "unknown"
		}
		o.Reasons = append(o.Reasons, Reason{string(g.Group), en.T("output.why."+key, "factor", g.Group), (1 - beta) * g.Share * g.D, "output.why." + key})
	}
	sum := 0.0
	for _, t := range r.Terms {
		sum += t.Weight
	}
	for _, t := range r.Terms {
		key := map[string]string{"urgency": "urgent", "momentum": "momentum", "unblocking": "unblocking", "cooldown": "fresh"}[t.Name]
		if t.Name == scoring.TermModeMatch {
			switch {
			case t.V >= 1:
				key = "mode_match"
			case t.V <= 0:
				key = "mode_mismatch"
			default:
				key = "mode_unclassified"
			}
		}
		if t.Name == scoring.TermEngagementMatch {
			switch {
			case t.V >= 1:
				key = "engagement_match"
			case t.V <= 0:
				key = "engagement_mismatch"
			default:
				key = "engagement_unclassified"
			}
		}
		if sum > 0 {
			o.Reasons = append(o.Reasons, Reason{t.Name, en.T("output.why." + key), beta * t.Weight / sum * t.V, "output.why." + key})
		}
	}
	sort.SliceStable(o.Reasons, func(i, j int) bool { return o.Reasons[i].Contribution > o.Reasons[j].Contribution })
	if len(o.Reasons) > 3 {
		o.Reasons = o.Reasons[:3]
	}
	return o
}
func JSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return e.Encode(v)
}
func Prefix(id string, ids []string) string {
	for n := 6; n < len(id); n++ {
		unique := true
		for _, v := range ids {
			if v != id && strings.HasPrefix(v, id[:n]) {
				unique = false
				break
			}
		}
		if unique {
			return id[:n]
		}
	}
	return id
}
func Text(w io.Writer, r Result, tr *i18n.Translator, color ...bool) error {
	if len(r.Items) == 0 {
		_, e := fmt.Fprintln(w, tr.T("output.no_results"))
		return e
	}
	ids := r.AllIDs
	if len(ids) == 0 {
		for _, it := range r.Items {
			ids = append(ids, it.ID)
		}
	}
	titleWidth, typeWidth, idWidth := 0, 0, 0
	for _, it := range r.Items {
		titleWidth = max(titleWidth, len([]rune(it.Title)))
		idWidth = max(idWidth, len([]rune(Prefix(it.ID, ids))))
		if it.Type != nil {
			typeWidth = max(typeWidth, len([]rune(*it.Type)))
		}
	}
	for _, it := range r.Items {
		score := FormatFloat(*it.Score)
		score = fmt.Sprintf("%5s", score)
		if len(color) > 0 && color[0] {
			score = "\x1b[36m" + score + "\x1b[0m"
		}
		reasons := []string{}
		for _, r := range it.Reasons {
			reasons = append(reasons, tr.T(r.Key, "factor", factorLabel(tr, r.Factor)))
		}
		typ, length := "", "—"
		if it.Type != nil {
			typ = *it.Type
		}
		if it.Ongoing {
			length = tr.T("output.ongoing")
		}
		prop := model.PropRemainingDuration
		if hours, ok := it.Properties[string(prop)].(*float64); ok && hours != nil {
			length = FormatDuration(*hours, tr)
		}
		if it.EstimatedProgress != nil {
			length += " · " + tr.T("output.estimated_progress", "percent", FormatFloat(*it.EstimatedProgress))
		}
		status := string(it.Status)
		if len(it.Modes) > 0 || len(it.Engagements) > 0 {
			parts := append([]string{}, it.Modes...)
			parts = append(parts, it.Engagements...)
			status += "·" + strings.Join(parts, ",")
		}
		if _, e := fmt.Fprintf(w, "%s  %-*s  %-*s  %-*s  %7s  [%s]  %s\n", score, idWidth, Prefix(it.ID, ids), titleWidth, it.Title, typeWidth, typ, length, status, strings.Join(reasons, " · ")); e != nil {
			return e
		}
		for _, b := range it.Readiness.BlockedBy {
			fmt.Fprintln(w, "  "+tr.T("output.blocked_by", "id", Prefix(b.ID, ids), "title", b.Title, "status", b.Status))
		}
		for k, v := range it.Evidence {
			if v == "unknown" {
				fmt.Fprintln(w, "  "+tr.T("output.why.unknown", "factor", factorLabel(tr, k)))
			}
		}
		if it.Breakdown != nil {
			if e := RenderValue(w, it.Breakdown, tr); e != nil {
				return e
			}
		}
	}
	return nil
}

// StatusText presents item references with titles and store-unique UUID prefixes.
func StatusText(w io.Writer, r StatusMutation, tr *i18n.Translator) error {
	for _, item := range r.Items {
		if _, err := fmt.Fprintln(w, tr.T("output.status_transition", "title", item.Title, "id", Prefix(item.ID, r.AllIDs), "before", item.From, "after", item.To)); err != nil {
			return err
		}
	}
	return nil
}

// DependenciesText presents dependency references with titles and unique prefixes.
func DependenciesText(w io.Writer, r Dependencies, tr *i18n.Translator) error {
	if _, err := fmt.Fprintln(w, tr.T("output.dependencies", "title", r.Title, "id", Prefix(r.ID, r.AllIDs))); err != nil {
		return err
	}
	if len(r.DependencyItems) == 0 {
		_, err := fmt.Fprintln(w, "  "+tr.T("output.none"))
		return err
	}
	for _, dep := range r.DependencyItems {
		if _, err := fmt.Fprintln(w, "  "+tr.T("output.item_reference", "title", dep.Title, "id", Prefix(dep.ID, r.AllIDs))); err != nil {
			return err
		}
	}
	return nil
}

func factorLabel(tr *i18n.Translator, factor string) string {
	key := "factor." + factor
	label := tr.T(key)
	if label == key {
		return factor
	}
	return label
}

// RenderValue renders command reports without leaking Go map formatting into UI.
func RenderValue(w io.Writer, v any, tr *i18n.Translator) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if e = decoder.Decode(&value); e != nil {
		return e
	}
	var render func(any, int) error
	render = func(v any, depth int) error {
		pad := strings.Repeat("  ", depth)
		switch x := v.(type) {
		case map[string]any:
			keys := []string{}
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				label := tr.T("field." + k)
				if label == "field."+k {
					label = k
				}
				switch x[k].(type) {
				case map[string]any, []any:
					if _, e := fmt.Fprintf(w, "%s%s:\n", pad, label); e != nil {
						return e
					}
					if e = render(x[k], depth+1); e != nil {
						return e
					}
				default:
					if _, e := fmt.Fprintf(w, "%s%s: %v\n", pad, label, reportScalar(k, x[k], x, tr)); e != nil {
						return e
					}
				}
			}
		case []any:
			for _, v := range x {
				if e = render(v, depth); e != nil {
					return e
				}
			}
		default:
			_, e = fmt.Fprintf(w, "%s%v\n", pad, textScalar(x))
		}
		return e
	}
	return render(value, 0)
}

// LogText acknowledges both the estimate update and the completion decision.
func LogText(w io.Writer, r LogMutation, tr *i18n.Translator) error {
	l := r.Log
	if l.Activity.Duration != nil {
		if l.Activity.Ongoing {
			if _, err := fmt.Fprintln(w, tr.T("output.log_ongoing", "duration", FormatDuration(*l.Activity.Duration, tr))); err != nil {
				return err
			}
		} else {
			status := tr.T("output.log_status_unchanged")
			if l.Completed {
				status = tr.T("output.log_completed")
			}
			if l.EstimatedProgress != nil {
				status += " " + tr.T("output.estimated_progress", "percent", FormatFloat(*l.EstimatedProgress))
			}
			if _, err := fmt.Fprintln(w, tr.T("output.log", "duration", FormatDuration(*l.Activity.Duration, tr), "before", FormatDuration(*l.Activity.RemainingBefore, tr), "after", FormatDuration(*l.Activity.RemainingAfter, tr), "status", status)); err != nil {
				return err
			}
		}
	}
	if l.Activity.Cost != nil {
		key := "output.log_cost_unknown"
		args := []any{"cost", FormatFloat(*l.Activity.Cost)}
		if l.Activity.CostBefore != nil {
			key = "output.log_cost"
			args = append(args, "before", FormatFloat(*l.Activity.CostBefore), "after", FormatFloat(*l.Activity.CostAfter))
		}
		if _, err := fmt.Fprintln(w, tr.T(key, args...)); err != nil {
			return err
		}
	}
	return nil
}

// FormatFloat rounds human-facing numeric output only. Stored and JSON values
// retain their original precision.
func FormatFloat(v float64) string {
	text := fmt.Sprintf("%.2f", v)
	if text == "-0.00" {
		return "0.00"
	}
	return text
}

func textScalar(v any) any {
	if n, ok := v.(json.Number); ok && strings.ContainsAny(string(n), ".eE") {
		if f, err := n.Float64(); err == nil {
			return FormatFloat(f)
		}
	}
	return v
}
