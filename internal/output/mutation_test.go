package output

import (
	"bytes"
	"strings"
	"testing"

	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/scoring"
)

func TestItemMutationText(t *testing.T) {
	tr, _ := i18n.New("en")
	id := "01973a2c-8f4e-7c1b-9a2d-3e4f5a6b7c8d"
	two, three := 2.0, 3.0
	before := &model.Item{ID: id, Title: "Fix bike", Kind: model.KindProject, Status: model.StatusNotStarted, RemainingDuration: &two}
	after := *before
	after.RemainingDuration = &three
	after.Tags = []string{"home"}

	var b bytes.Buffer
	if err := ItemMutationText(&b, ItemMutation{ID: id, Changed: true, After: before, AllIDs: []string{id}}, tr); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "Added \"Fix bike\" [01973a]: project · not_started · 2h\n" {
		t.Fatalf("add: %q", got)
	}
	b.Reset()
	if err := ItemMutationText(&b, ItemMutation{ID: id, Changed: true, Before: before, After: &after, AllIDs: []string{id}}, tr); err != nil {
		t.Fatal(err)
	}
	want := "Updated \"Fix bike\" [01973a]:\n  Remaining duration: 2h → 3h\n  Tags: — → home\n"
	if got := b.String(); got != want {
		t.Fatalf("edit:\n%s\nwant:\n%s", got, want)
	}
	b.Reset()
	if err := ItemMutationText(&b, ItemMutation{ID: id, Before: before, After: before, AllIDs: []string{id}}, tr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "No changes") {
		t.Fatalf("unchanged: %q", b.String())
	}
}

func TestExplainTextIsCompact(t *testing.T) {
	tr, _ := i18n.New("en")
	two := 2.0
	it := &model.Item{ID: "x", Title: "T", Kind: model.KindProject, Status: model.StatusNotStarted, RemainingDuration: &two}
	cfg := scoring.DefaultConfig()
	r, err := scoring.Score(it, scoring.Context{}, cfg, it.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := ExplainText(&b, r, cfg.Beta, tr); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	members := 0
	for _, g := range r.Groups {
		members += len(g.Members)
	}
	// One header, base and right-now headings, a row per group, member, and term.
	if lines := strings.Count(out, "\n"); lines != 3+len(r.Groups)+members+len(r.Terms) {
		t.Fatalf("%d lines:\n%s", lines, out)
	}
	for _, want := range []string{"score ", "80% base", "Remaining duration", "= 2h", "unknown, prior", "cooldown"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
