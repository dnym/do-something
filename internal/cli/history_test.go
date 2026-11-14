package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIUndoHistoryStats(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	id := success(t, db, "add", "--title", "Course", "--kind", "project", "--type", "course", "--total-duration", "1h", "--remaining-duration", "1h", "--status", "in_progress")["id"].(string)
	success(t, db, "suggest") // recorded; hidden from history by default
	success(t, db, "log", id, "30m")
	success(t, db, "log", id, "30m", "--complete")

	h := success(t, db, "history")
	events := h["events"].([]any)
	if len(events) != 4 || events[0].(map[string]any)["type"] != "completed" {
		t.Fatalf("history %v", events)
	}
	if all := success(t, db, "history", "--all")["events"].([]any); len(all) != 5 {
		t.Fatalf("history --all %v", all)
	}
	if logged := success(t, db, "history", "Course", "--event", "logged", "--limit", "1")["events"].([]any); len(logged) != 1 {
		t.Fatal(logged)
	}

	// Non-interactive undo needs explicit confirmation; --dry-run previews.
	if code, _, _ := invoke(t, db, "undo"); code != 2 {
		t.Fatalf("undo without --yes: exit %d", code)
	}
	preview := success(t, db, "undo", "--dry-run")
	if preview["changed"] != false || preview["undo"].(map[string]any)["uncompleted"] != true {
		t.Fatal(preview)
	}
	if r := success(t, db, "undo", "--yes"); r["changed"] != true {
		t.Fatal(r)
	}
	show := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
	if show["status"] != "in_progress" || show["properties"].(map[string]any)["remaining_duration"] != 0.5 {
		t.Fatal(show)
	}
	h = success(t, db, "history")
	undone := 0
	for _, raw := range h["events"].([]any) {
		if raw.(map[string]any)["reverted_by"] != nil {
			undone++
		}
	}
	if undone != 2 {
		t.Fatalf("undone events %d: %v", undone, h)
	}

	// Stats exclude the undone session and completion.
	st := success(t, db, "stats")
	if st["sessions"] != float64(1) || st["hours_logged"] != 0.5 || st["completed"] != float64(0) || st["started"] != float64(1) {
		t.Fatal(st)
	}
	byType := st["by_type"].([]any)
	if len(byType) != 1 || byType[0].(map[string]any)["type"] != "course" {
		t.Fatal(byType)
	}
	tomorrow := time.Now().AddDate(0, 0, 1).Format(time.DateOnly)
	if code, _, _ := invoke(t, db, "stats", "--since", tomorrow, "--until", time.Now().Format(time.DateOnly)); code != 2 {
		t.Fatal("since after until accepted")
	}

	// A finished item is compared against its estimate.
	success(t, db, "log", id, "45m", "--complete")
	st = success(t, db, "stats")
	est := st["estimates"].([]any)
	if len(est) != 1 || est[0].(map[string]any)["ratio"] != 1.25 || st["median_estimate_ratio"] != 1.25 {
		t.Fatal(st)
	}
	success(t, db, "undo", "--yes")
	success(t, db, "undo", "--yes")
	if code, _, errout := invoke(t, db, "undo", "--yes"); code != 2 || !strings.Contains(errout, "INVALID_STATE") {
		t.Fatalf("%d %s", code, errout)
	}
}

func TestCLIListMarksCooldown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	success(t, db, "add", "--title", "Walk", "--kind", "project")
	cooling := func() bool {
		item := success(t, db, "list")["items"].([]any)[0].(map[string]any)
		for _, r := range item["reasons"].([]any) {
			if r.(map[string]any)["factor"] == "cooldown" {
				return r.(map[string]any)["contribution"].(float64) < 0
			}
		}
		return false
	}
	if cooling() {
		t.Fatal("cooldown reported before any suggestion")
	}
	success(t, db, "suggest")
	if !cooling() {
		t.Fatal("list does not explain the cooldown penalty")
	}
}

// An item added part-way through (90h total, 50m left) is compared against
// the remaining time when logging began, not its lifetime total.
func TestCLIStatsEstimateStartsAtFirstSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	success(t, db, "add", "--title", "Show", "--kind", "media", "--total-duration", "90h", "--remaining-duration", "50m", "--status", "in_progress")
	success(t, db, "log", "Show", "25m")
	success(t, db, "edit", "Show", "--remaining-duration", "50m") // re-estimate upward
	success(t, db, "log", "Show", "50m", "--complete")
	est := success(t, db, "stats")["estimates"].([]any)[0].(map[string]any)
	if est["estimate_hours"] != 50.0/60 || est["actual_hours"] != 75.0/60 {
		t.Fatal(est)
	}
	// suggest rotates by cooldown without listing it as a reason.
	success(t, db, "add", "--title", "Task", "--kind", "project", "--status", "in_progress")
	for _, r := range success(t, db, "suggest", "--peek")["items"].([]any)[0].(map[string]any)["reasons"].([]any) {
		if r.(map[string]any)["factor"] == "cooldown" {
			t.Fatal("suggest lists the cooldown reason")
		}
	}
}
