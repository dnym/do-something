package cli

import (
	"bytes"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOngoingWorkflow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, kind := range []string{"project", "media"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "items.db")
			added := success(t, db, "add", "--title", "Ongoing activity", "--kind", kind, "--ongoing", "--min-session-duration", "15m")
			id := added["id"].(string)
			if added["after"].(map[string]any)["ongoing"] != true {
				t.Fatal(added)
			}
			success(t, db, "start", id)
			for range 2 {
				logged := success(t, db, "log", id)["log"].(map[string]any)
				activity := logged["activity"].(map[string]any)
				if activity["duration"] != .25 || activity["ongoing"] != true || activity["remaining_before"] != nil || activity["remaining_after"] != nil || logged["completed"] != false || logged["estimated_progress_percent"] != nil {
					t.Fatal(logged)
				}
			}
			show := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
			states := show["property_states"].(map[string]any)
			if show["ongoing"] != true || show["status"] != "in_progress" || show["estimated_progress_percent"] != nil || states["remaining_duration"] != "not_applicable" || states["total_duration"] != "not_applicable" {
				t.Fatal(show)
			}
			if len(success(t, db, "list", "--session", "15m", "--unknown", "exclude")["items"].([]any)) != 1 {
				t.Fatal("session excluded ongoing activity")
			}
			if len(success(t, db, "list", "--session", "14m")["items"].([]any)) != 0 {
				t.Fatal("session ignored minimum")
			}
			for _, policy := range []string{"include", "exclude"} {
				if len(success(t, db, "list", "--finish-within", "1000h", "--unknown", policy)["items"].([]any)) != 0 {
					t.Fatal("finish filter included ongoing activity")
				}
			}
			for _, args := range [][]string{{"log", id, "--complete"}, {"edit", id, "--remaining-duration", "1h"}, {"edit", id, "--total-duration", "1h"}} {
				code, _, _ := invoke(t, db, args...)
				if code != 2 {
					t.Fatal(args, code)
				}
			}
			success(t, db, "edit", id, "--notes", "still ongoing")
			success(t, db, "drop", id)
			code, _, _ := invoke(t, db, "log", id)
			if code != 2 {
				t.Fatal("logged dropped activity")
			}
			success(t, db, "reopen", id)
			export := success(t, db, "export", "json")
			data, err := json.Marshal(export)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "export.json")
			if err = os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			imported := filepath.Join(dir, "imported.db")
			success(t, imported, "import", file)
			snapshot := filepath.Join(dir, "snapshot.db")
			success(t, db, "snapshot", snapshot)
			synced := filepath.Join(dir, "synced.db")
			success(t, synced, "sync", snapshot, "--take-remote")
			for _, target := range []string{imported, synced} {
				shown := success(t, target, "show", id)["items"].([]any)[0].(map[string]any)
				if shown["ongoing"] != true {
					t.Fatal(shown)
				}
				got := success(t, target, "export", "json")
				if len(got["events"].([]any)) != 4 {
					t.Fatal("lost session history", got)
				}
			}
			success(t, db, "edit", id, "--ongoing=false", "--total-duration", "2h", "--remaining-duration", "1h")
			finite := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
			if finite["ongoing"] != false || finite["estimated_progress_percent"] != float64(50) {
				t.Fatal(finite)
			}
			code, _, _ = invoke(t, db, "edit", id, "--ongoing")
			if code != 2 {
				t.Fatal("silently cleared duration estimates")
			}
			success(t, db, "edit", id, "--ongoing", "--clear-total-duration", "--clear-remaining-duration")
			success(t, db, "log", id, "30m", "--no-complete", "--force")
		})
	}
}

func TestOngoingInteractiveLogNeverPrompts(t *testing.T) {
	s, err := store.Open(store.OpenWrite, store.Options{DBPath: filepath.Join(t.TempDir(), "items.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ongoing := true
	id, err := s.Save(store.ItemSpec{Title: "Practice", Kind: model.KindProject, Ongoing: &ongoing, MinSessionDuration: model.FloatPtr(.25)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionStatus(store.Start, []string{id}); err != nil {
		t.Fatal(err)
	}
	tr, _ := i18n.New("en")
	var prompt bytes.Buffer
	result, err := logActivity(s, LogFlags{ID: id}, true, strings.NewReader(""), &prompt, tr)
	if err != nil || prompt.Len() != 0 || !result.Changed || result.Log.Completed {
		t.Fatal(result, err, prompt.String())
	}
	// Explicit completion is rejected without applying another log.
	_, err = logActivity(s, LogFlags{ID: id, Complete: true}, true, strings.NewReader("yes\n"), &prompt, tr)
	if err == nil || prompt.Len() != 0 {
		t.Fatal(err, prompt.String())
	}
	events, err := s.AllEvents()
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
}
