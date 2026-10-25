package cli

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSearch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	success(t, db, "add", "--title", "WOODWORKING course", "--kind", "project", "--status", "done")
	success(t, db, "add", "--title", "A film", "--notes", "About woodworking", "--kind", "media", "--status", "dropped")
	success(t, db, "add", "--title", "Unrelated", "--type", "woodworking", "--category", "woodworking", "--kind", "project")
	before := success(t, db, "export", "json")
	r := success(t, db, "search", "woodworking", "--limit", "0")
	if r["matched_count"] != float64(2) || r["recorded"] != false {
		t.Fatal(r)
	}
	list := success(t, db, "list", "--text", "woodworking", "--limit", "0")
	if !reflect.DeepEqual(r["items"], list["items"]) || !reflect.DeepEqual(r["filters"], list["filters"]) {
		t.Fatalf("search differs from list: %v vs %v", r, list)
	}
	filtered := success(t, db, "search", "WOODWORKING", "--status", "dropped", "--kind", "media")
	if filtered["matched_count"] != float64(1) {
		t.Fatal(filtered)
	}
	phrase := success(t, db, "search", "woodworking course")
	if phrase["matched_count"] != float64(1) {
		t.Fatal(phrase)
	}
	limited := success(t, db, "search", "woodworking", "--limit", "1")
	if limited["matched_count"] != float64(2) || limited["returned_count"] != float64(1) {
		t.Fatal(limited)
	}
	empty := success(t, db, "search", "no such title")
	if empty["matched_count"] != float64(0) {
		t.Fatal(empty)
	}
	code, _, stderr := invoke(t, db, "search", "no such title", "--fail-empty")
	if code != 3 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	after := success(t, db, "export", "json")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("search changed stored content")
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "missing.db")
	for _, args := range [][]string{{"search"}, {"search", ""}, {"search", "  "}, {"search", "a", "b"}} {
		code, _, stderr := invoke(t, db, args...)
		if code != 2 {
			t.Fatalf("%q: exit %d: %s", args, code, stderr)
		}
	}
}
