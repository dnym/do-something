package cli

import (
	"bytes"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func treeFixture(id string, status model.Status, deps ...string) *model.Item {
	return &model.Item{ID: id, Title: id, Status: status, Dependencies: deps}
}

func rowSummary(rows []output.DependencyTreeRow) []string {
	out := []string{}
	for _, row := range rows {
		ref := ""
		if row.Reference {
			ref = " ref"
		}
		out = append(out, strings.Repeat(" ", row.Depth)+row.ParentID+" "+row.Relation+" "+row.Item.ID+ref)
	}
	return out
}

func TestDependencyTreeScope(t *testing.T) {
	items := []*model.Item{
		treeFixture("Sand walls", model.StatusInProgress, "Remove wallpaper"),
		treeFixture("Remove wallpaper", model.StatusDone),
		treeFixture("Paint room", model.StatusNotStarted, "Sand walls", "Buy paint"),
		treeFixture("Hang pictures", model.StatusDropped, "Paint room"),
		treeFixture("Buy paint", model.StatusNotStarted, "Select color"),
		treeFixture("Select color", model.StatusDone),
		treeFixture("Paint fence", model.StatusNotStarted, "Buy paint"),
		treeFixture("Dispose paper", model.StatusNotStarted, "Remove wallpaper"),
		treeFixture("Unrelated", model.StatusNotStarted),
	}
	r := buildDependencyTree(items, "Sand walls")
	if r.Selected.ID != "Sand walls" || r.Selected.Status != model.StatusInProgress {
		t.Fatal(r.Selected)
	}
	wantUp := []string{"Sand walls requires Remove wallpaper"}
	wantDown := []string{"Sand walls dependent Paint room", " Paint room also_requires Buy paint", " Paint room dependent Hang pictures"}
	if got := rowSummary(r.Prerequisites); !reflect.DeepEqual(got, wantUp) {
		t.Fatalf("prerequisites: %v", got)
	}
	if got := rowSummary(r.Dependents); !reflect.DeepEqual(got, wantDown) {
		t.Fatalf("dependents: %v", got)
	}
	if r.Prerequisites[0].Item.Status != model.StatusDone || r.Dependents[2].Item.Status != model.StatusDropped {
		t.Fatal("historical statuses lost")
	}
	if len(r.AllIDs) != len(items) {
		t.Fatal("display prefix scope must include the whole store")
	}
	// Input and edge order cannot affect the displayed topology.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	for _, it := range items {
		for i, j := 0, len(it.Dependencies)-1; i < j; i, j = i+1, j-1 {
			it.Dependencies[i], it.Dependencies[j] = it.Dependencies[j], it.Dependencies[i]
		}
	}
	reversed := buildDependencyTree(items, "Sand walls")
	if !reflect.DeepEqual(r.Prerequisites, reversed.Prerequisites) || !reflect.DeepEqual(r.Dependents, reversed.Dependents) {
		t.Fatal("unstable order")
	}
}

func TestDependencyTreeSharedTasks(t *testing.T) {
	t.Run("prerequisite diamond", func(t *testing.T) {
		r := buildDependencyTree([]*model.Item{
			treeFixture("S", model.StatusNotStarted, "B", "A"),
			treeFixture("A", model.StatusDone, "C"),
			treeFixture("B", model.StatusDropped, "C"),
			treeFixture("C", model.StatusDone),
		}, "S")
		want := []string{"S requires A", " A requires C", "S requires B", " B requires C ref"}
		if got := rowSummary(r.Prerequisites); !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
	})
	t.Run("context does not suppress main paths", func(t *testing.T) {
		r := buildDependencyTree([]*model.Item{
			treeFixture("S", model.StatusNotStarted),
			treeFixture("A", model.StatusNotStarted, "S", "B"),
			treeFixture("B", model.StatusNotStarted, "S"),
			treeFixture("Z", model.StatusNotStarted, "A", "B"),
		}, "S")
		want := []string{"S dependent A", " A also_requires B ref", " A dependent Z", "  Z also_requires B ref", "S dependent B", " B dependent A ref", " B dependent Z ref"}
		if got := rowSummary(r.Dependents); !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
	})
	t.Run("prerequisite reused as context", func(t *testing.T) {
		r := buildDependencyTree([]*model.Item{
			treeFixture("S", model.StatusNotStarted, "A"),
			treeFixture("A", model.StatusDone),
			treeFixture("D", model.StatusNotStarted, "S", "A"),
		}, "S")
		if got := rowSummary(r.Dependents); !reflect.DeepEqual(got, []string{"S dependent D", " D also_requires A ref"}) {
			t.Fatal(got)
		}
	})
}

func TestDependencyTreeCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	prep := success(t, db, "add", "--title", "Remove wallpaper", "--kind", "project", "--status", "done")["id"].(string)
	selected := success(t, db, "add", "--title", "Sand walls", "--kind", "project", "--status", "in_progress", "--depends-on", prep)["id"].(string)
	success(t, db, "add", "--title", "Buy paint", "--kind", "project")
	success(t, db, "add", "--title", "Paint room", "--kind", "project", "--depends-on", "Sand walls,Buy paint")
	before := success(t, db, "export", "json")
	contract := success(t, db, "schema")
	responses := contract["responses"].(map[string]any)
	var first map[string]any
	for _, ref := range []string{"Sand walls", selected, selected[:len(selected)-1]} {
		r := success(t, db, "deps", "tree", ref)
		if err := checkShape(r, responses["deps_tree"].(map[string]any), "deps_tree"); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = r
		} else if !reflect.DeepEqual(r, first) {
			t.Fatal("reference forms produce different trees")
		}
	}
	if got := first["selected"].(map[string]any); got["id"] != selected || got["status"] != "in_progress" {
		t.Fatal(got)
	}
	if len(first["prerequisites"].([]any)) != 1 || len(first["dependents"].([]any)) != 2 {
		t.Fatal(first)
	}
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	for _, lang := range []string{"en", "sv"} {
		var out, errout bytes.Buffer
		code := Run([]string{"--db", db, "--no-input", "--lang", lang, "deps", "tree", "Sand walls"}, in, &out, &errout)
		if code != 0 {
			t.Fatalf("%d: %s", code, &errout)
		}
		for _, title := range []string{"Sand walls", "Remove wallpaper", "Paint room", "Buy paint"} {
			if !strings.Contains(out.String(), title) {
				t.Fatal(out.String())
			}
		}
		if strings.Contains(out.String(), "output.tree.") {
			t.Fatal(out.String())
		}
	}
	if after := success(t, db, "export", "json"); !reflect.DeepEqual(before, after) {
		t.Fatal("tree changed stored content")
	}
	for _, args := range [][]string{{"deps", "tree"}, {"deps", "tree", "Sand walls", "Buy paint"}, {"deps", "tree", "missing"}} {
		code, _, stderr := invoke(t, db, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d: %s", args, code, stderr)
		}
		if !json.Valid([]byte(stderr)) {
			t.Fatal(stderr)
		}
	}
}

func TestDependencyTreeIsolatedAndMissingStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	code, _, stderr := invoke(t, db, "deps", "tree", "missing")
	if code != 1 || !strings.Contains(stderr, "NO_STORE") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("tree created a store")
	}
	success(t, db, "add", "--title", "Isolated", "--kind", "project")
	r := success(t, db, "deps", "tree", "Isolated")
	if len(r["prerequisites"].([]any)) != 0 || len(r["dependents"].([]any)) != 0 {
		t.Fatal(r)
	}
}
