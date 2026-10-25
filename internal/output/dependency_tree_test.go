package output

import (
	"bytes"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"strings"
	"testing"
)

func TestDependencyTreeText(t *testing.T) {
	tr, err := i18n.New("en")
	if err != nil {
		t.Fatal(err)
	}
	r := DependencyTree{
		Selected:      DependencyTreeItem{ID: "root-123456A", Title: "Sand walls", Status: model.StatusInProgress},
		AllIDs:        []string{"root-123456A", "root-123456B", "prep01", "paint1", "buy001", "hang01", "other1"},
		Prerequisites: []DependencyTreeRow{{Item: DependencyTreeItem{ID: "prep01", Title: "Remove wallpaper", Status: model.StatusDone}, Relation: "requires"}},
		Dependents: []DependencyTreeRow{
			{Item: DependencyTreeItem{ID: "paint1", Title: "Paint room", Status: model.StatusNotStarted}, Relation: "dependent"},
			{Item: DependencyTreeItem{ID: "buy001", Title: "Buy paint", Status: model.StatusDropped}, Relation: "also_requires", Depth: 1},
			{Item: DependencyTreeItem{ID: "hang01", Title: "Hang pictures", Status: model.StatusNotStarted}, Relation: "dependent", Depth: 1},
			{Item: DependencyTreeItem{ID: "prep01", Title: "Remove wallpaper", Status: model.StatusDone}, Relation: "also_requires", Depth: 2, Reference: true},
			{Item: DependencyTreeItem{ID: "other1", Title: "Another dependent", Status: model.StatusNotStarted}, Relation: "dependent"},
		},
	}
	var out bytes.Buffer
	if err := DependencyTreeText(&out, r, tr); err != nil {
		t.Fatal(err)
	}
	want := `"Sand walls" [root-123456A] [in_progress] ← selected
Prerequisites:
└─ requires: "Remove wallpaper" [prep01] [done]
Dependents:
├─ needed by: "Paint room" [paint1] [not_started]
│  ├─ also requires: "Buy paint" [buy001] [dropped]
│  └─ needed by: "Hang pictures" [hang01] [not_started]
│     └─ also requires: "Remove wallpaper" [prep01] [done] (reference)
└─ needed by: "Another dependent" [other1] [not_started]
`
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestDependencyTreeTextIsolated(t *testing.T) {
	tr, _ := i18n.New("en")
	var out bytes.Buffer
	if err := DependencyTreeText(&out, DependencyTree{Selected: DependencyTreeItem{ID: "alone1", Title: "Alone", Status: model.StatusNotStarted}}, tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Prerequisites:\n  none\nDependents:\n  none\n") {
		t.Fatal(out.String())
	}
}
