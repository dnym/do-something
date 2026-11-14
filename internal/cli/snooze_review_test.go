package cli

import (
	"bytes"
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLISnoozeHidesFromSuggest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	id := success(t, db, "add", "--title", "Bike", "--kind", "project")["id"].(string)
	success(t, db, "add", "--title", "Other", "--kind", "project")
	r := success(t, db, "snooze", "Bike", "--for", "2d")
	after, _ := time.Parse(time.RFC3339Nano, r["items"].([]any)[0].(map[string]any)["after"].(string))
	if d := time.Until(after); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatalf("snoozed for %v", d)
	}
	titles := func(args ...string) []string {
		out := []string{}
		for _, it := range success(t, db, args...)["items"].([]any) {
			out = append(out, it.(map[string]any)["title"].(string))
		}
		return out
	}
	if got := titles("suggest", "--peek"); len(got) != 1 || got[0] != "Other" {
		t.Fatal(got)
	}
	if got := titles("suggest", "--peek", "--include-snoozed"); len(got) != 2 {
		t.Fatal(got)
	}
	shown := success(t, db, "show", id)["items"].([]any)[0].(map[string]any)
	if shown["snoozed_until"] == nil {
		t.Fatal("show hides the snooze")
	}
	for _, bad := range [][]string{{"snooze", "Bike", "--until", "2020-01-01"}, {"snooze", "Bike", "--for", "-1d"}, {"snooze", "Bike", "--for", "soon"}, {"snooze", "Bike", "--for", "1d", "--until", "2099-01-01"}} {
		if code, _, _ := invoke(t, db, bad...); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
	success(t, db, "unsnooze", "Bike")
	if got := titles("suggest", "--peek"); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestParseSnoozeDuration(t *testing.T) {
	for raw, want := range map[string]time.Duration{"3d": 72 * time.Hour, "2w": 14 * 24 * time.Hour, "1.5d": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := parseSnoozeDuration(raw); err != nil || got != want {
			t.Errorf("%s: %v %v", raw, got, err)
		}
	}
	if _, err := parseSnoozeDuration("xd"); err == nil {
		t.Error("xd accepted")
	}
}

func TestReviewCandidatesAndSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	success(t, db, "add", "--title", "Fresh", "--kind", "project", "--intensity", "low", "--career", "1", "--physical", "1", "--mental", "1", "--social", "1", "--interest", "1")
	blank := success(t, db, "add", "--title", "Blank", "--kind", "media")["id"].(string)
	success(t, db, "add", "--title", "Finished", "--kind", "media", "--status", "done")

	s, err := store.Open(store.OpenRead, store.Options{DBPath: db})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := effectiveConfig(s)
	r, err := reviewCandidates(s, c, ReviewFlags{}, time.Now())
	if err != nil || len(r.Items) != 1 || r.Items[0].ID != blank || r.Items[0].Staleness != 1 {
		t.Fatalf("%+v %v", r.Items, err)
	}
	// A rating 3 years past its 18-month half-life is stale; fresh ones are not.
	later, _ := reviewCandidates(s, c, ReviewFlags{}, time.Now().AddDate(3, 0, 0))
	if len(later.Items) != 2 {
		t.Fatalf("later %+v", later.Items)
	}
	one, _ := reviewCandidates(s, c, ReviewFlags{ID: "Fresh"}, time.Now())
	if len(one.Items) != 1 || len(one.Items[0].Ratings) != len(model.RatingProperties(model.KindProject)) {
		t.Fatal(one)
	}
	s.Close()

	tr, _ := i18n.New("en")
	// Blank (media): intensity high, actuality kept unknown, an invalid entry
	// re-asks influence, which becomes 0.5, then quit at quality.
	in := strings.NewReader("high\n\nbogus\n0.5\nq\n")
	var prompts bytes.Buffer
	res, err := reviewSession(r, false, store.Options{DBPath: db}, &config.DeviceConfig{}, in, &prompts, tr)
	if err != nil || res.Updated != 2 || res.Reviewed != 1 {
		t.Fatalf("%+v %v\n%s", res, err, prompts.String())
	}
	if !strings.Contains(prompts.String(), "not a rating level") {
		t.Fatal("invalid input not reported")
	}
	props := success(t, db, "show", blank)["items"].([]any)[0].(map[string]any)["properties"].(map[string]any)
	if props["intensity"] != 2.0/3 || props["influence"] != 0.5 || props["actuality"] != nil {
		t.Fatal(props)
	}
}
