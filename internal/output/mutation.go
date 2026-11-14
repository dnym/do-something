package output

import (
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"fmt"
	"io"
	"strings"
	"time"
)

// Warning is a non-fatal observation about a successful mutation. Code is a
// stable contract key; Items lists the items it concerns.
type Warning struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Items   []ItemReference `json:"items"`
}

// WarningSimilarTitle flags other items whose titles differ only in case or
// whitespace from the added or renamed item.
const WarningSimilarTitle = "SIMILAR_TITLE"

type itemField struct {
	key, label, value string
}

// ItemMutationText acknowledges add/edit in one line, followed by the changed
// fields for an edit. The full before/after snapshots remain in JSON.
func ItemMutationText(w io.Writer, r ItemMutation, tr *i18n.Translator) error {
	if r.After == nil {
		return nil
	}
	id := Prefix(r.ID, r.AllIDs)
	if r.Before == nil {
		parts := []string{string(r.After.Kind)}
		if r.After.Type != nil {
			parts = append(parts, *r.After.Type)
		}
		parts = append(parts, string(r.After.Status))
		switch {
		case r.After.Ongoing:
			parts = append(parts, tr.T("output.ongoing"))
		case r.After.RemainingDuration != nil:
			parts = append(parts, FormatDuration(*r.After.RemainingDuration, tr))
		}
		_, err := fmt.Fprintln(w, tr.T("output.item_added", "title", r.After.Title, "id", id, "summary", strings.Join(parts, " · ")))
		return err
	}
	if !r.Changed {
		_, err := fmt.Fprintln(w, tr.T("output.item_unchanged", "title", r.After.Title, "id", id))
		return err
	}
	if _, err := fmt.Fprintln(w, tr.T("output.item_updated", "title", r.After.Title, "id", id)); err != nil {
		return err
	}
	before := map[string]string{}
	for _, f := range itemFields(r.Before, r.Titles, tr) {
		before[f.key] = f.value
	}
	for _, f := range itemFields(r.After, r.Titles, tr) {
		if old := before[f.key]; old != f.value {
			if _, err := fmt.Fprintf(w, "  %s: %s → %s\n", f.label, old, f.value); err != nil {
				return err
			}
		}
	}
	return nil
}

// WarningsText prints mutation warnings, one per line, for a human reader.
func WarningsText(w io.Writer, warnings []Warning, ids []string, tr *i18n.Translator) error {
	for _, warn := range warnings {
		refs := make([]string, len(warn.Items))
		for i, it := range warn.Items {
			refs[i] = tr.T("output.item_reference", "title", it.Title, "id", Prefix(it.ID, ids))
		}
		key := "output.warning." + strings.ToLower(warn.Code)
		text := tr.T(key, "items", strings.Join(refs, ", "))
		if text == key {
			text = warn.Message
		}
		if _, err := fmt.Fprintln(w, tr.T("output.warning", "message", text)); err != nil {
			return err
		}
	}
	return nil
}

// itemFields lists every user-editable field of an item in display order,
// with values formatted for text output; unknown values render as "—".
func itemFields(it *model.Item, titles map[string]string, tr *i18n.Translator) []itemField {
	none := "—"
	str := func(v *string) string {
		if v == nil {
			return none
		}
		return *v
	}
	dur := func(v *float64) string {
		if v == nil {
			return none
		}
		return FormatDuration(*v, tr)
	}
	num := func(v *float64) string {
		if v == nil {
			return none
		}
		return FormatFloat(*v)
	}
	list := func(v []string) string {
		if len(v) == 0 {
			return none
		}
		return strings.Join(v, ", ")
	}
	deadline := none
	if it.Deadline != nil {
		deadline = it.Deadline.Format(time.DateOnly)
		if it.Deadline.Hour() != 0 || it.Deadline.Minute() != 0 || it.Deadline.Second() != 0 {
			deadline = it.Deadline.Format(time.RFC3339)
		}
	}
	deps := make([]string, len(it.Dependencies))
	for i, id := range it.Dependencies {
		deps[i] = id
		if t, ok := titles[id]; ok {
			deps[i] = fmt.Sprintf("%q", t)
		}
	}
	ongoing := tr.T("output.no")
	if it.Ongoing {
		ongoing = tr.T("output.yes")
	}
	fields := []itemField{
		{"title", "", it.Title},
		{"kind", "", string(it.Kind)},
		{"type", "", str(it.Type)},
		{"category", "", str(it.Category)},
		{"status", "", string(it.Status)},
		{"ongoing", "", ongoing},
		{"total_duration", "", dur(it.TotalDuration)},
		{"remaining_duration", "", dur(it.RemainingDuration)},
		{"min_session_duration", "", dur(it.MinSessionDuration)},
		{"cost_left", "", num(it.CostLeft)},
		{"deadline", "", deadline},
		{"url", "", str(it.URL)},
		{"notes", "", str(it.Notes)},
		{"tags", "", list(it.SortedTags())},
		{"modes", "", list(it.Modes)},
		{"engagements", "", list(it.Engagements)},
		{"dependencies", "", list(deps)},
	}
	for i := range fields {
		fields[i].label = label(tr, "field."+fields[i].key, fields[i].key)
	}
	for _, p := range model.AllProperties() {
		if !p.IsRating() {
			continue
		}
		fields = append(fields, itemField{"rating." + string(p), label(tr, "rating.label."+string(p), string(p)), num(it.RatingValue(p))})
	}
	return fields
}

func label(tr *i18n.Translator, key, fallback string) string {
	if v := tr.T(key); v != key {
		return v
	}
	return fallback
}
