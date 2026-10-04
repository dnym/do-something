package cli

import (
	"bytes"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	db := filepath.Join(dir, "items.db")
	id := success(t, db, "add", "--kind", "media", "--title", "Series", "--total-duration", "40h", "--remaining-duration", "48m", "--min-session-duration", "24m")["id"].(string)
	dependent := success(t, db, "add", "--kind", "project", "--title", "Review", "--depends-on", id)["id"].(string)
	success(t, db, "start", id)
	first := success(t, db, "log", "Series")["log"].(map[string]any)
	if first["activity"].(map[string]any)["remaining_after"] != .4 || first["completed"] != false {
		t.Fatal(first)
	}
	second := success(t, db, "log", output.Prefix(id, []string{id, dependent}))["log"].(map[string]any)
	if second["activity"].(map[string]any)["remaining_after"] != float64(0) || second["status_after"] != "in_progress" || second["event_id"] == first["event_id"] {
		t.Fatal(second)
	}
	shown := success(t, db, "show", dependent)["items"].([]any)[0].(map[string]any)
	if shown["readiness"].(map[string]any)["ready"] != false {
		t.Fatal(shown)
	}
	third := success(t, db, "log", id, "1m", "--complete")["log"].(map[string]any)
	if third["completed"] != true || third["status_after"] != "done" {
		t.Fatal(third)
	}
	shown = success(t, db, "show", dependent)["items"].([]any)[0].(map[string]any)
	if shown["readiness"].(map[string]any)["ready"] != true {
		t.Fatal(shown)
	}
	code, _, stderr := invoke(t, db, "log", id, "1m")
	if code != 2 || !strings.Contains(stderr, "INVALID_STATE") {
		t.Fatal(code, stderr)
	}
	exported := success(t, db, "export", "json")
	if len(exported["events"].([]any)) != 5 {
		t.Fatal(exported)
	}
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "export.json")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(dir, "imported.db")
	success(t, imported, "import", file)
	if got := success(t, imported, "export", "json"); len(got["events"].([]any)) != 5 {
		t.Fatal(got)
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	success(t, db, "snapshot", snapshot)
	synced := filepath.Join(dir, "synced.db")
	success(t, synced, "sync", snapshot, "--take-remote")
	if got := success(t, synced, "export", "json"); len(got["events"].([]any)) != 5 {
		t.Fatal(got)
	}
}

func TestLogCompletionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, input                       string
		interactive, complete, noComplete bool
		remaining                         float64
		done, prompt, fail                bool
	}{
		{name: "noninteractive", input: "yes\n", remaining: .4},
		{name: "accept", input: "yes\n", interactive: true, remaining: .4, done: true, prompt: true},
		{name: "swedish yes", input: "ja\n", interactive: true, remaining: .4, done: true, prompt: true},
		{name: "decline", input: "no\n", interactive: true, remaining: .4, prompt: true},
		{name: "default no", input: "\n", interactive: true, remaining: .4, prompt: true},
		{name: "reprompt", input: "maybe\ny\n", interactive: true, remaining: .4, done: true, prompt: true},
		{name: "eof aborts", interactive: true, remaining: .4, prompt: true, fail: true},
		{name: "complete flag", complete: true, remaining: .4, done: true},
		{name: "complete skips prompt", interactive: true, complete: true, remaining: .4, done: true},
		{name: "no complete skips prompt", interactive: true, noComplete: true, remaining: .4},
		{name: "time left", interactive: true, remaining: 1},
		{name: "complete only at zero", complete: true, remaining: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := store.Open(store.OpenWrite, store.Options{DBPath: filepath.Join(t.TempDir(), "items.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			id, err := s.Save(store.ItemSpec{Title: "Activity", Kind: model.KindProject, RemainingDuration: model.FloatPtr(tc.remaining)}, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.TransitionStatus(store.Start, []string{id}); err != nil {
				t.Fatal(err)
			}
			tr, _ := i18n.New("en")
			var prompt bytes.Buffer
			result, err := logActivity(s, LogFlags{ID: id, Duration: model.FloatPtr(.4), Complete: tc.complete, NoComplete: tc.noComplete}, tc.interactive, strings.NewReader(tc.input), &prompt, tr)
			if (err != nil) != tc.fail || (prompt.Len() > 0) != tc.prompt {
				t.Fatal(result, err, prompt.String())
			}
			item, err := s.GetItem(id)
			if err != nil {
				t.Fatal(err)
			}
			events, err := s.AllEvents()
			if err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				if *item.RemainingDuration != tc.remaining || len(events) != 1 {
					t.Fatal("aborted prompt mutated store")
				}
				return
			}
			if result.Log.Completed != tc.done || (item.Status == model.StatusDone) != tc.done {
				t.Fatal(result, item)
			}
		})
	}
}

func TestLogFlagsAndNonTTY(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "items.db")
	id := success(t, db, "add", "--kind", "project", "--title", "Task", "--remaining-duration", "1h")["id"].(string)
	success(t, db, "start", id)
	for _, args := range [][]string{{"log", id, "0h"}, {"log", id, "-1h"}, {"log", id, "--complete", "--no-complete"}, {"log", id}} {
		code, _, _ := invoke(t, db, args...)
		if code != 2 {
			t.Fatal(args, code)
		}
	}
	// No --no-input flag: redirected stdin must still never prompt or consume yes.
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err = in.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err = in.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"--db", db, "--format", "json", "log", id, "1h"}, in, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Mark this item") || !strings.Contains(out.String(), `"completed":false`) {
		t.Fatal(out.String(), stderr.String())
	}
}

func TestLogCostAndForce(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "items.db")
	id := success(t, db, "add", "--kind", "project", "--title", "Build", "--cost", "100", "--min-session-duration", "15m")["id"].(string)
	if code, _, stderr := invoke(t, db, "log", id, "--cost", "30"); code != 2 || !strings.Contains(stderr, "INVALID_STATE") {
		t.Fatal(code, stderr)
	}
	logged := success(t, db, "log", id, "--cost", "30", "--force")["log"].(map[string]any)
	activity := logged["activity"].(map[string]any)
	if activity["duration"] != nil || activity["cost"] != float64(30) || activity["cost_after"] != float64(70) || logged["status_after"] != "not_started" {
		t.Fatal(logged)
	}
	shown := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
	if shown["properties"].(map[string]any)["cost_left"] != float64(70) {
		t.Fatal(shown)
	}
	if code, _, _ := invoke(t, db, "log", id, "--cost", "1", "--complete", "--force"); code != 2 {
		t.Fatal("cost-only --complete succeeded")
	}
	success(t, db, "edit", id, "--remaining-duration", "2h")
	success(t, db, "start", id)
	logged = success(t, db, "log", id, "30m", "--cost", "20")["log"].(map[string]any)
	activity = logged["activity"].(map[string]any)
	if activity["remaining_after"] != 1.5 || activity["cost_after"] != float64(50) {
		t.Fatal(logged)
	}
	success(t, db, "done", id)
	if code, _, _ := invoke(t, db, "log", id, "--cost", "10"); code != 2 {
		t.Fatal("logged done item without --force")
	}
	logged = success(t, db, "log", id, "--cost", "10", "--force")["log"].(map[string]any)
	if logged["status_after"] != "done" || logged["completed"] != false {
		t.Fatal(logged)
	}
	for _, value := range []string{"0", "-1", "NaN", "+Inf"} {
		if code, _, _ := invoke(t, db, "log", id, "--cost", value, "--force"); code != 2 {
			t.Fatal(value, code)
		}
	}
}

func TestForcedCompletionPolicy(t *testing.T) {
	s, err := store.Open(store.OpenWrite, store.Options{DBPath: filepath.Join(t.TempDir(), "items.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.Save(store.ItemSpec{Title: "Task", Kind: model.KindProject, RemainingDuration: model.FloatPtr(.5)}, true)
	if err != nil {
		t.Fatal(err)
	}
	tr, _ := i18n.New("en")
	var prompt bytes.Buffer
	result, err := logActivity(s, LogFlags{ID: id, Duration: model.FloatPtr(.5), Force: true}, true, strings.NewReader("yes\n"), &prompt, tr)
	if err != nil || prompt.Len() != 0 || result.Log.StatusAfter != model.StatusNotStarted {
		t.Fatal(result, err, prompt.String())
	}
	result, err = logActivity(s, LogFlags{ID: id, Duration: model.FloatPtr(.5), Complete: true, Force: true}, true, strings.NewReader(""), &prompt, tr)
	if err != nil || !result.Log.Completed || result.Log.StatusAfter != model.StatusDone {
		t.Fatal(result, err)
	}
	if _, err = logActivity(s, LogFlags{ID: id, Duration: model.FloatPtr(.5), Complete: true, Force: true}, false, strings.NewReader(""), &prompt, tr); err == nil {
		t.Fatal("forced completion accepted for done item")
	}
}
