package output

import (
	"bytes"
	"strings"
	"testing"

	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/scoring"
)

func TestDisplayedPrefixIncludesHiddenItems(t *testing.T) {
	tr, err := i18n.New("en")
	if err != nil {
		t.Fatal(err)
	}
	visible := "01973a2c-8f4e-7c1b-9a2d-3e4f5a6b7c8d"
	hidden := "01973a2c-8f4e-7c1b-9a2d-3e4f5a6b7c8e"
	score := 0.9
	result := Result{Items: []Item{{ID: visible, Title: "Visible", Score: &score}}, AllIDs: []string{visible, hidden}}
	var b bytes.Buffer
	if err = Text(&b, result, tr); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(b.String())
	if len(fields) < 2 {
		t.Fatal(b.String())
	}
	prefix := fields[1]
	if !strings.HasPrefix(visible, prefix) || strings.HasPrefix(hidden, prefix) {
		t.Fatalf("ambiguous displayed prefix %q", prefix)
	}
}

func TestDurationProgressText(t *testing.T) {
	for _, locale := range []string{"en", "sv"} {
		tr, err := i18n.New(locale)
		if err != nil {
			t.Fatal(err)
		}
		it := &model.Item{ID: "example", Title: "Series", Kind: model.KindMedia, Status: model.StatusNotStarted,
			TotalDuration: model.FloatPtr(40), RemainingDuration: model.FloatPtr(0.4)}
		var b bytes.Buffer
		render := func() {
			b.Reset()
			row := NewItem(it, scoring.Result{}, nil, nil, false, 0)
			if err := Text(&b, Result{Items: []Item{row}}, tr); err != nil {
				t.Fatal(err)
			}
		}
		render()
		if !strings.Contains(b.String(), FormatDuration(0.4, tr)) || !strings.Contains(b.String(), "~99.00%") || strings.Contains(b.String(), "output.") {
			t.Fatal(b.String())
		}
		it.TotalDuration = nil
		render()
		if strings.Contains(b.String(), "%") || !strings.Contains(b.String(), FormatDuration(0.4, tr)) {
			t.Fatal(b.String())
		}
	}
}

func TestSwedishUnknownReasons(t *testing.T) {
	tr, err := i18n.New("sv")
	if err != nil {
		t.Fatal(err)
	}
	score := 0.9
	result := Result{Items: []Item{{ID: "example", Title: "Title", Score: &score, Reasons: []Reason{{Factor: "commitment", Key: "output.why.unknown"}}, Evidence: map[string]string{"effort": "unknown"}}}}
	var b bytes.Buffer
	if err = Text(&b, result, tr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "commitment") || strings.Contains(b.String(), "effort") {
		t.Fatal(b.String())
	}
	if !strings.Contains(b.String(), "insats") || !strings.Contains(b.String(), "intensitet") {
		t.Fatal(b.String())
	}
}
