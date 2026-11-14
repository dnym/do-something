package output

import (
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"fmt"
	"io"
	"strings"
	"time"
)

// ReviewRating is one applicable rating and how far it has decayed.
type ReviewRating struct {
	Property       model.Property      `json:"property"`
	State          model.PropertyState `json:"state"`
	Value          *float64            `json:"value"`
	RatedAt        *time.Time          `json:"rated_at"`
	AgeMonths      *float64            `json:"age_months"`       // null when unknown or undated
	HalfLifeMonths *float64            `json:"half_life_months"` // null = never decays
	// Decay is the fraction (0..1) of the rating's weight lost toward the
	// prior; unknown ratings count as 1.
	Decay float64 `json:"decay"`
	Stale bool    `json:"stale"` // unknown, or decay ≥ stale_decay
}

// ReviewItem is an item whose ratings need attention.
type ReviewItem struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Kind      model.Kind     `json:"kind"`
	Type      *string        `json:"type"`
	Status    model.Status   `json:"status"`
	Staleness float64        `json:"staleness"` // mean decay over applicable ratings
	Ratings   []ReviewRating `json:"ratings"`
}

// Review lists items in review order (stalest first).
type Review struct {
	SchemaVersion int          `json:"schema_version"`
	StaleDecay    float64      `json:"stale_decay"`
	Matched       int          `json:"matched_count"`
	Returned      int          `json:"returned_count"`
	Items         []ReviewItem `json:"items"`
	AllIDs        []string     `json:"-"`
	Single        bool         `json:"-"` // one named item: show every rating
}

// ReviewSaved counts the rating changes saved for one item.
type ReviewSaved struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated int    `json:"updated"`
}

// ReviewResult summarizes an interactive review session.
type ReviewResult struct {
	SchemaVersion int           `json:"schema_version"`
	Changed       bool          `json:"changed"`
	Reviewed      int           `json:"reviewed"`
	Updated       int           `json:"updated"`
	Items         []ReviewSaved `json:"items"`
}

// ReviewText lists the candidates without prompting (non-interactive use).
func ReviewText(w io.Writer, r Review, tr *i18n.Translator) error {
	if len(r.Items) == 0 {
		_, err := fmt.Fprintln(w, tr.T("output.review.nothing"))
		return err
	}
	for _, it := range r.Items {
		parts := []string{}
		for _, rr := range it.Ratings {
			if !rr.Stale && !r.Single {
				continue
			}
			name := label(tr, "rating.label."+string(rr.Property), string(rr.Property))
			switch {
			case rr.Value == nil:
				parts = append(parts, name+" "+tr.T("output.review.unknown"))
			case rr.AgeMonths != nil:
				parts = append(parts, fmt.Sprintf("%s %s (%s, %s)", name, FormatFloat(*rr.Value), tr.T("output.review.age", "months", fmt.Sprintf("%.0f", *rr.AgeMonths)), tr.T("output.review.decay", "percent", fmt.Sprintf("%.0f", rr.Decay*100))))
			default:
				parts = append(parts, name+" "+FormatFloat(*rr.Value))
			}
		}
		line := fmt.Sprintf("%s  %s", tr.T("output.item_reference", "title", it.Title, "id", Prefix(it.ID, r.AllIDs)), strings.Join(parts, " · "))
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if r.Matched > r.Returned {
		if _, err := fmt.Fprintln(w, tr.T("output.review.more", "count", fmt.Sprint(r.Matched-r.Returned))); err != nil {
			return err
		}
	}
	return nil
}

// ReviewResultText summarizes what a session saved.
func ReviewResultText(w io.Writer, r ReviewResult, tr *i18n.Translator) error {
	_, err := fmt.Fprintln(w, tr.T("output.review.summary", "items", tr.TN("output.review.items", r.Reviewed), "ratings", tr.TN("output.review.ratings", r.Updated)))
	return err
}
