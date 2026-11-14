package output

import (
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/scoring"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ExplainText renders a score breakdown as an indented table: the final blend,
// one row per group and member, then one row per right-now term. JSON keeps
// the complete score_breakdown object.
func ExplainText(w io.Writer, r scoring.Result, beta float64, tr *i18n.Translator) error {
	const indent = "    "
	lines := []string{}
	if len(r.Terms) == 0 {
		lines = append(lines, tr.T("output.explain.final_base", "final", FormatFloat(r.Final), "base", FormatFloat(r.Base)))
	} else {
		lines = append(lines, tr.T("output.explain.final", "final", FormatFloat(r.Final),
			"base_share", percent(1-beta), "base", FormatFloat(r.Base),
			"beta", percent(beta), "right_now", FormatFloat(r.RightNow)))
	}

	// Pad labels to one column so the numbers line up across the breakdown.
	width := 0
	for _, g := range r.Groups {
		width = max(width, utf8.RuneCountInString(groupLabel(tr, g.Group)))
		for _, m := range g.Members {
			width = max(width, utf8.RuneCountInString(propertyLabel(tr, m.Property))+2)
		}
	}
	for _, t := range r.Terms {
		width = max(width, utf8.RuneCountInString(termLabel(tr, t.Name)))
	}
	row := func(pad, name, value string, notes ...string) {
		text := fmt.Sprintf("%s%s  %s", pad, padRight(name, width-(len(pad)-len(indent))), value)
		if notes = nonEmpty(notes); len(notes) > 0 {
			text += "  " + strings.Join(notes, " · ")
		}
		lines = append(lines, strings.TrimRight(text, " "))
	}

	lines = append(lines, "  "+tr.T("output.explain.base", "base", FormatFloat(r.Base)))
	for _, g := range r.Groups {
		row(indent, groupLabel(tr, g.Group), FormatFloat(g.D), weightNote(tr, g.Weight), tr.T("output.explain.share", "share", percent(g.Share)))
		for _, m := range g.Members {
			row(indent+"  ", propertyLabel(tr, m.Property), FormatFloat(m.D), memberNotes(tr, m)...)
		}
	}
	if len(r.Terms) > 0 {
		lines = append(lines, "  "+tr.T("output.explain.right_now", "right_now", FormatFloat(r.RightNow)))
		for _, t := range r.Terms {
			row(indent, termLabel(tr, t.Name), FormatFloat(t.V), append([]string{weightNote(tr, t.Weight)}, termNotes(tr, t)...)...)
		}
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, "  "+l); err != nil {
			return err
		}
	}
	return nil
}

func memberNotes(tr *i18n.Translator, m scoring.MemberResult) []string {
	notes := []string{}
	switch {
	case m.State == model.StateNotApplicable:
		notes = append(notes, tr.T("output.explain.not_applicable"))
	case m.Value == nil:
		notes = append(notes, tr.T("output.explain.unknown_prior"))
	default:
		value := FormatFloat(*m.Value)
		if durationField(string(m.Property)) {
			value = FormatDuration(*m.Value, tr)
		}
		notes = append(notes, tr.T("output.explain.value", "value", value))
		if m.RatedAt != nil && m.AgeMonths >= 0.05 {
			notes = append(notes, tr.T("output.explain.rated_ago", "months", fmt.Sprintf("%.1f", m.AgeMonths)))
		}
		if m.Note != "" {
			notes = append(notes, tr.T("output.explain.prior"))
		}
	}
	return append(notes, weightNote(tr, m.Weight))
}

func termNotes(tr *i18n.Translator, t scoring.TermResult) []string {
	notes := []string{}
	if t.DaysLeft != nil {
		notes = append(notes, tr.T("output.explain.days_left", "days", fmt.Sprintf("%.1f", *t.DaysLeft)))
	}
	if t.Pace != nil {
		notes = append(notes, tr.T("output.explain.pace", "pace", FormatFloat(*t.Pace)))
	}
	if t.Ready != nil && t.Total != nil {
		notes = append(notes, tr.T("output.explain.unblocks", "ready", fmt.Sprint(*t.Ready), "total", fmt.Sprint(*t.Total)))
	}
	if t.AnchorAge != nil {
		notes = append(notes, tr.T("output.explain.anchor_age", "days", fmt.Sprintf("%.1f", *t.AnchorAge)))
	}
	return notes
}

// weightNote is shown only for non-default weights to keep rows short.
func weightNote(tr *i18n.Translator, weight float64) string {
	if weight == 1 {
		return ""
	}
	return tr.T("output.explain.weight", "weight", FormatFloat(weight))
}

func groupLabel(tr *i18n.Translator, g model.Group) string {
	return label(tr, "factor."+string(g), string(g))
}

func propertyLabel(tr *i18n.Translator, p model.Property) string {
	if p.IsRating() {
		return label(tr, "rating.label."+string(p), string(p))
	}
	return label(tr, "field."+string(p), string(p))
}

func termLabel(tr *i18n.Translator, name string) string {
	return label(tr, "output.explain.term."+name, name)
}

func percent(v float64) string {
	return fmt.Sprintf("%.0f%%", v*100)
}

func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func nonEmpty(list []string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
