package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func refCommand(command, ref string) []string {
	args := []string{command, ref}
	switch command {
	case "edit":
		args = append(args, "--notes", "updated")
	case "log":
		args = append(args, "10m", "--no-complete", "--force")
	}
	return args
}

func TestItemReferencesEveryCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, command := range []string{"show", "edit", "log", "start", "done", "drop", "reopen"} {
		t.Run(command, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "items.db")
			id := success(t, db, "add", "--title", "A series", "--kind", "media", "--remaining-duration", "2h")["id"].(string)
			lookalike := success(t, db, "add", "--title", id, "--kind", "media", "--remaining-duration", "2h")["id"].(string)
			success(t, db, refCommand(command, "A series")...)
			// The UUID must keep referring to its item, despite the lookalike title.
			success(t, db, refCommand(command, id)...)
			shown := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
			if shown["id"] != id || shown["title"] != "A series" {
				t.Fatal(shown)
			}
			other := success(t, db, "show", lookalike)["items"].([]any)[0].(map[string]any)
			if other["status"] != "not_started" || other["notes"] != nil || other["properties"].(map[string]any)["remaining_duration"] != float64(2) {
				t.Fatal("modified UUID-shaped title instead of UUID", other)
			}
		})
	}
}

func TestAmbiguousTitlesEveryCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, command := range []string{"show", "edit", "log", "start", "done", "drop", "reopen"} {
		t.Run(command, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "items.db")
			for range 2 {
				success(t, db, "add", "--title", "Duplicate", "--kind", "media", "--remaining-duration", "2h")
			}
			before, err := json.Marshal(success(t, db, "export", "json"))
			if err != nil {
				t.Fatal(err)
			}
			code, _, stderr := invoke(t, db, refCommand(command, "Duplicate")...)
			if code != 2 || !strings.Contains(stderr, "INVALID_ID") || !strings.Contains(stderr, "candidates") {
				t.Fatal(code, stderr)
			}
			after, err := json.Marshal(success(t, db, "export", "json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("ambiguous reference changed content")
			}
		})
	}
}

func TestStatusReferencesAreAtomicAndDeduplicated(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "items.db")
	id := success(t, db, "add", "--title", "Unique title", "--kind", "project")["id"].(string)
	for range 2 {
		success(t, db, "add", "--title", "Duplicate", "--kind", "project")
	}
	code, _, stderr := invoke(t, db, "done", "Unique title", "Duplicate", "Missing")
	if code != 2 || !strings.Contains(stderr, `"id":"Duplicate"`) || !strings.Contains(stderr, `"id":"Missing"`) {
		t.Fatal(code, stderr)
	}
	shown := success(t, db, "show", "Unique title")["items"].([]any)[0].(map[string]any)
	if shown["status"] != "not_started" {
		t.Fatal("partially applied failed batch", shown)
	}
	result := success(t, db, "start", "Unique title", id)
	if len(result["items"].([]any)) != 1 {
		t.Fatal("duplicate references were not deduplicated", result)
	}
	exported := success(t, db, "export", "json")
	if len(exported["events"].([]any)) != 1 {
		t.Fatal("duplicated event", exported)
	}
	for _, command := range []string{"done", "reopen", "drop", "reopen"} {
		success(t, db, command, "Unique title")
	}
}
