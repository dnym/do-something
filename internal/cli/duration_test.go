package cli

import (
	"bytes"
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurationWorkflow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, kind := range []string{"project", "media"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "items.db")
			added := success(t, db, "add", "--kind", kind, "--title", "Long item",
				"--total-duration", "40h", "--remaining-duration", "40h", "--min-session-duration", "20m")
			id := added["id"].(string)
			show := func(db string) map[string]any {
				return success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
			}
			initial := show(db)
			if initial["estimated_progress_percent"] != float64(0) {
				t.Fatal(initial)
			}
			success(t, db, "edit", id, "--remaining-duration", "24m")
			nearDone := show(db)
			props := nearDone["properties"].(map[string]any)
			if props["total_duration"] != float64(40) || props["remaining_duration"] != 0.4 || props["min_session_duration"] != 1.0/3 || nearDone["estimated_progress_percent"] != float64(99) {
				t.Fatal(nearDone)
			}
			if nearDone["score"].(float64) <= initial["score"].(float64) {
				t.Fatal("progress did not improve score")
			}
			for _, tc := range []struct {
				filter, duration string
				count            int
			}{
				{"--finish-within", "24m", 1}, {"--finish-within", "23m", 0},
				{"--session", "20m", 1}, {"--session", "19m", 0},
			} {
				out := success(t, db, "list", tc.filter, tc.duration, "--unknown", "exclude")
				if len(out["items"].([]any)) != tc.count {
					t.Fatal(tc, out)
				}
			}
			// Both interchange paths preserve all three estimates.
			data, err := json.Marshal(success(t, db, "export", "json"))
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "items.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			importDB := filepath.Join(dir, "import.db")
			success(t, importDB, "import", file)
			snapshot := filepath.Join(dir, "snapshot.db")
			success(t, db, "snapshot", snapshot)
			syncDB := filepath.Join(dir, "sync.db")
			success(t, syncDB, "sync", snapshot, "--take-remote")
			for _, target := range []string{importDB, syncDB} {
				got := show(target)
				if got["estimated_progress_percent"] != float64(99) || got["properties"].(map[string]any)["min_session_duration"] != 1.0/3 {
					t.Fatal(got)
				}
			}
			success(t, db, "edit", id, "--remaining-duration", "0h")
			zero := show(db)
			if zero["status"] != "not_started" || zero["estimated_progress_percent"] != float64(100) {
				t.Fatal(zero)
			}
			success(t, db, "edit", id, "--clear-remaining-duration")
			unknown := show(db)
			if unknown["estimated_progress_percent"] != nil || unknown["property_states"].(map[string]any)["remaining_duration"] != "unknown" {
				t.Fatal(unknown)
			}
			if len(success(t, db, "list", "--finish-within", "1h", "--unknown", "exclude")["items"].([]any)) != 0 {
				t.Fatal("total used as remaining fallback")
			}
			included := success(t, db, "list", "--finish-within", "1h")["items"].([]any)
			if len(included) != 1 || included[0].(map[string]any)["filter_evidence"].(map[string]any)["finish_within"] != "unknown" {
				t.Fatal(included)
			}
			success(t, db, "edit", id, "--remaining-duration", "2h", "--clear-total-duration", "--clear-min-session-duration")
			if got := show(db); got["estimated_progress_percent"] != nil || got["properties"].(map[string]any)["remaining_duration"] != float64(2) {
				t.Fatal(got)
			}
		})
	}
}

func TestDurationFlagValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, flag := range []string{"--total-duration", "--remaining-duration", "--min-session-duration"} {
		for _, value := range []string{"-1h", "NaN", "Inf", "2", "999999999999h"} {
			db := filepath.Join(t.TempDir(), "items.db")
			code, _, stderr := invoke(t, db, "add", "--kind", "media", "--title", "Bad duration", flag, value)
			if code != 2 || !strings.Contains(stderr, "INVALID_ARGUMENT") {
				t.Fatalf("%s %s: %d %s", flag, value, code, stderr)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatal("invalid input created a store")
			}
		}
	}
}

func TestDurationPrompts(t *testing.T) {
	tr, _ := i18n.New("en")
	for _, remaining := range []string{"24m", "1"} {
		entry := completeEntry()
		entry.RemainingDuration, entry.TotalDuration, entry.MinSessionDuration = nil, nil, nil
		var out bytes.Buffer
		_, err := promptEntry(&entry, nil, config.NewConfig(map[string]string{"defaults.min_session_duration.provided": "0.5"}), strings.NewReader(remaining+"\n20m\n40h\n\n\n"), &out, tr)
		if err != nil {
			t.Fatal(err, out.String())
		}
		want := 0.4
		if remaining == "1" {
			want = 0.5
		}
		if *entry.RemainingDuration != want || *entry.TotalDuration != 40 || *entry.MinSessionDuration != 1.0/3 {
			t.Fatal(entry)
		}
		if !strings.Contains(out.String(), "Suggested minimum session for this type: 30m") {
			t.Fatal(out.String())
		}
	}
}
