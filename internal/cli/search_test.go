package cli

import (
	"path/filepath"
	"reflect"
	"strings"
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

func TestFuzzySearch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	exact := success(t, db, "add", "--title", "Buy paint", "--kind", "project", "--status", "done", "--interest", "0")["id"].(string)
	partial := success(t, db, "add", "--title", "Buy paint online", "--kind", "project", "--interest", "0.5")["id"].(string)
	approximate := success(t, db, "add", "--title", "Buy plaint", "--kind", "media", "--status", "dropped", "--interest", "1")["id"].(string)
	notes := success(t, db, "add", "--title", "Supplies", "--notes", strings.Repeat("Other supplies. ", 30)+"Buy paint", "--kind", "project", "--interest", "1")["id"].(string)
	success(t, db, "add", "--title", "Unrelated", "--kind", "project", "--type", "Buy paint")
	before := success(t, db, "export", "json")
	r := success(t, db, "search", "buy paint", "--fuzzy", "--limit", "0")
	if r["recorded"] != false || r["filters"].(map[string]any)["fuzzy"] != true || r["matched_count"] != float64(4) {
		t.Fatal(r)
	}
	items := r["items"].([]any)
	for i, id := range []string{exact, partial, approximate, notes} {
		it := items[i].(map[string]any)
		if it["id"] != id || it["score"] == nil {
			t.Fatal(items)
		}
		m := it["search_match"].(map[string]any)
		if m["relevance"] == nil || m["kind"] == nil || m["excerpt"] == nil {
			t.Fatal(m)
		}
		if i < 3 && m["field"] != "title" || i == 3 && m["field"] != "notes" {
			t.Fatal(m)
		}
	}
	if items[0].(map[string]any)["score"].(float64) >= items[2].(map[string]any)["score"].(float64) {
		t.Fatal("fixture must distinguish relevance from activity score")
	}
	limited := success(t, db, "search", "buy paint", "--fuzzy", "--limit", "1")
	if limited["matched_count"] != float64(4) || limited["returned_count"] != float64(1) || limited["items"].([]any)[0].(map[string]any)["id"] != exact {
		t.Fatal(limited)
	}
	filtered := success(t, db, "search", "buy paint", "--fuzzy", "--kind", "media", "--status", "dropped")
	if filtered["matched_count"] != float64(1) || filtered["items"].([]any)[0].(map[string]any)["id"] != approximate {
		t.Fatal(filtered)
	}
	minScore := success(t, db, "search", "buy paint", "--fuzzy", "--min-score", "1")
	if minScore["matched_count"] != float64(0) {
		t.Fatal(minScore)
	}
	// Exact search retains its old predicate and has no fuzzy evidence.
	plain := success(t, db, "search", "buy paint")
	if plain["matched_count"] != float64(3) {
		t.Fatal(plain)
	}
	for _, raw := range plain["items"].([]any) {
		if _, ok := raw.(map[string]any)["search_match"]; ok {
			t.Fatal(plain)
		}
	}
	reordered := success(t, db, "search", "paint buy", "--fuzzy")
	if reordered["matched_count"] != float64(4) {
		t.Fatal(reordered)
	}
	empty := success(t, db, "search", "zzzzzzzz", "--fuzzy")
	if empty["matched_count"] != float64(0) {
		t.Fatal(empty)
	}
	code, _, stderr := invoke(t, db, "search", "zzzzzzzz", "--fuzzy", "--fail-empty")
	if code != 3 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// Fuzzy lookup never changes mutating-command resolution.
	code, _, stderr = invoke(t, db, "done", "Buy pait")
	if code != 2 || !strings.Contains(stderr, "INVALID_ID") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if after := success(t, db, "export", "json"); !reflect.DeepEqual(before, after) {
		t.Fatal("search changed stored data")
	}
	contract := success(t, db, "schema")
	responses := contract["responses"].(map[string]any)
	if err := checkShape(r, responses["search"].(map[string]any), "search"); err != nil {
		t.Fatal(err)
	}
}

func TestFuzzySearchExamplesAndScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	for _, title := range []string{"Woodworking course", "Build a book shelf", "Sanding walls"} {
		success(t, db, "add", "--title", title, "--kind", "project")
	}
	for _, tc := range []struct{ query, title string }{{"woodworkng", "Woodworking course"}, {"bookshelf", "Build a book shelf"}, {"sandng", "Sanding walls"}} {
		r := success(t, db, "search", tc.query, "--fuzzy")
		if r["matched_count"] != float64(1) || r["items"].([]any)[0].(map[string]any)["title"] != tc.title {
			t.Fatal(r)
		}
	}
	for _, args := range [][]string{{"list", "--fuzzy"}, {"suggest", "--fuzzy"}, {"edit", "Sandng walls", "--fuzzy"}} {
		code, _, stderr := invoke(t, db, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d: %s", args, code, stderr)
		}
	}
}
