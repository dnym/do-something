package cli

import (
	"bytes"
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"errors"
	"slices"
	"strings"
	"testing"
)

func completeEntry() Entry {
	text := "provided"
	date := "2026-09-25"
	n := 0.5
	return Entry{Title: "Provided title", Kind: "media", Status: "not_started", Type: &text, Category: &text, Notes: &text, URL: &text, Deadline: &date, Cost: &n, MinSessionDuration: &n, RemainingDuration: &n, TotalDuration: &n, Intensity: &n, Interest: &n, Actuality: &n, Influence: &n, Quality: &n, Tag: []string{"provided"}, DependsOn: []string{}}
}
func TestPromptsPreserveSuppliedFlags(t *testing.T) {
	entry := completeEntry()
	entry.Title = ""
	tr, _ := i18n.New("en")
	var out bytes.Buffer
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("Entered title\n\n\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if entry.Title != "Entered title" || entry.Kind != "media" || *entry.Type != "provided" || *entry.Quality != 0.5 {
		t.Fatal(entry)
	}
}
func TestPromptInvalidKind(t *testing.T) {
	entry := Entry{}
	tr, _ := i18n.New("en")
	var out bytes.Buffer
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("Some title\nbar\n"), &out, tr)
	if err == nil {
		t.Fatal("invalid kind must fail")
	}
	var arg *argumentError
	if !errors.As(err, &arg) {
		t.Fatalf("want argumentError, got %T: %v", err, err)
	}
	if arg.Argument != "kind" || arg.Value != "bar" {
		t.Fatal(arg)
	}
	if strings.Join(arg.Constraint.Allowed, ",") != "project,media" {
		t.Fatalf("allowed = %v", arg.Constraint.Allowed)
	}
	if !strings.Contains(out.String(), "project, media") {
		t.Errorf("kind prompt must list the valid kinds before asking:\n%s", out.String())
	}
}
func TestPromptMissingTitle(t *testing.T) {
	entry := Entry{}
	tr, _ := i18n.New("en")
	var out bytes.Buffer
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("\nproject\n"), &out, tr)
	if err == nil {
		t.Fatal("missing title must fail")
	}
	var arg *argumentError
	if errors.As(err, &arg) {
		t.Fatalf("title-only failure keeps the plain error, got %v", err)
	}
	if !strings.Contains(err.Error(), "title") {
		t.Fatal(err)
	}
}
func TestTagVocabularyPrompt(t *testing.T) {
	entry := completeEntry()
	entry.Tag = nil
	tr, _ := i18n.New("en")
	var out bytes.Buffer
	updates, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("new-tag\ny\n\n\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(updates["vocabulary.tags"], "new-tag") || len(entry.Tag) != 1 || entry.Tag[0] != "new-tag" {
		t.Fatal(updates, entry.Tag)
	}
}
func TestModesPrompt(t *testing.T) {
	tr, _ := i18n.New("en")
	// The prompt shows the built-in tokens with their localized labels.
	var out bytes.Buffer
	entry := completeEntry()
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("thinking,making\nfocused,loose\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if !slices.Equal(entry.Modes, []string{"thinking", "making"}) {
		t.Fatal(entry.Modes)
	}
	if !slices.Equal(entry.Engagements, []string{"focused", "loose"}) {
		t.Fatal(entry.Engagements)
	}
	text := out.String()
	for _, want := range []string{
		"movement — Move my body", "hands_on — Use my hands", "thinking — Use my mind", "making — Make something",
		"focused — Give it your full attention", "loose — Keep it low-key",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q in:\n%s", want, text)
		}
	}
	// "clear" empties the set; blank keeps it.
	entry = completeEntry()
	if _, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("clear\n\n"), &out, tr); err != nil {
		t.Fatal(err, out.String())
	}
	if !entry.ClearModes || entry.Modes != nil || entry.Engagements != nil {
		t.Fatal(entry.ClearModes, entry.Modes, entry.Engagements)
	}
	// Invalid tokens fail with the allowed list.
	entry = completeEntry()
	var errout bytes.Buffer
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("thinking,zen\n"), &errout, tr)
	if err == nil {
		t.Fatal("invalid mode must fail")
	}
	var arg *argumentError
	if !errors.As(err, &arg) || arg.Argument != "mode" {
		t.Fatalf("got %v", err)
	}
	if strings.Join(arg.Constraint.Allowed, ",") != "movement,hands_on,thinking,making" {
		t.Fatal(arg.Constraint.Allowed)
	}
}
func TestRatingPromptLevels(t *testing.T) {
	tr, _ := i18n.New("en")
	// Only the ratings are prompted: the level-anchor line is printed once,
	// and both a level name and a plain fraction are accepted.
	entry := completeEntry()
	entry.Intensity, entry.Actuality, entry.Influence, entry.Quality, entry.Interest = nil, nil, nil, nil, nil
	var out bytes.Buffer
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("high\n0.3\nmedium\nlow\nnone\n\n\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if *entry.Intensity != 2.0/3.0 || *entry.Actuality != 0.3 || *entry.Influence != 1.0/2.0 || *entry.Quality != 1.0/3.0 || *entry.Interest != 0 {
		t.Fatal(*entry.Intensity, *entry.Actuality, *entry.Influence, *entry.Quality, *entry.Interest)
	}
	if got := strings.Count(out.String(), "none=0"); got != 1 {
		t.Fatalf("level-anchor line must be printed exactly once, got %d:\n%s", got, out.String())
	}
	// A value that is not a level anchor stays a plain fraction (no snapping).
	entry = completeEntry()
	entry.Interest = nil
	out.Reset()
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("0.3\n\n\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if *entry.Interest != 0.3 {
		t.Fatal(*entry.Interest)
	}
	// Invalid input fails with the rating-level message.
	entry = completeEntry()
	entry.Interest = nil
	var errout bytes.Buffer
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("bogus\n"), &errout, tr)
	if err == nil || !strings.Contains(err.Error(), "not a rating level") {
		t.Fatalf("want rating-level error, got %v: %s", err, errout.String())
	}
	// Out-of-range numbers are rejected too.
	entry = completeEntry()
	entry.Interest = nil
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("1.5\n"), &errout, tr)
	if err == nil {
		t.Fatal("out-of-range rating must fail")
	}
}

func TestStatusPrompt(t *testing.T) {
	tr, _ := i18n.New("en")
	// The prompt shows the four statuses with their localized labels; a
	// name or a single digit is accepted, and blank keeps the current value.
	var out bytes.Buffer
	entry := completeEntry()
	entry.Status = ""
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("done\n\n\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if entry.Status != "done" {
		t.Fatal(entry.Status)
	}
	text := out.String()
	for _, want := range []string{
		"not_started — Not started", "in_progress — In progress", "done — Done", "dropped — Dropped",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q in:\n%s", want, text)
		}
	}
	// A single digit selects the status in the order shown.
	entry = completeEntry()
	entry.Status = ""
	if _, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("2\n\n\n"), &bytes.Buffer{}, tr); err != nil {
		t.Fatal(err)
	}
	if entry.Status != "in_progress" {
		t.Fatal(entry.Status)
	}
	// Blank keeps the stored status: the entry stays unset.
	existing := &model.Item{Title: "T", Kind: model.KindProject, Status: model.StatusInProgress}
	entry = completeEntry()
	entry.Status = ""
	if _, err := promptEntry(&entry, existing, config.NewConfig(nil), strings.NewReader("\n\n\n"), &bytes.Buffer{}, tr); err != nil {
		t.Fatal(err)
	}
	if entry.Status != "" {
		t.Fatal(entry.Status)
	}
	// "clear" is rejected: status always has a value.
	entry = completeEntry()
	entry.Status = ""
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("clear\n"), &bytes.Buffer{}, tr)
	if err == nil {
		t.Fatal("clear must fail for status")
	}
	var arg *argumentError
	if !errors.As(err, &arg) || arg.Argument != "status" {
		t.Fatalf("got %v", err)
	}
	// An unknown name fails with the allowed list.
	entry = completeEntry()
	entry.Status = ""
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("bogus\n"), &bytes.Buffer{}, tr)
	if err == nil {
		t.Fatal("invalid status must fail")
	}
	if !errors.As(err, &arg) || arg.Argument != "status" {
		t.Fatalf("got %v", err)
	}
	if strings.Join(arg.Constraint.Allowed, ",") != "not_started,in_progress,done,dropped" {
		t.Fatal(arg.Constraint.Allowed)
	}
}

func TestFormatRatingValue(t *testing.T) {
	cases := map[float64]string{
		0:         "none",
		1.0 / 3.0: "low",
		1.0 / 2.0: "medium",
		2.0 / 3.0: "high",
		4.0 / 5.0: "very_high",
		1:         "max",
		0.3:       "0.30", // non-anchor values render as the fraction
	}
	for v, want := range cases {
		if got := formatRatingValue(v); got != want {
			t.Errorf("formatRatingValue(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestEngagementsPrompt(t *testing.T) {
	tr, _ := i18n.New("en")
	// The prompt shows the built-in tokens with their localized labels.
	var out bytes.Buffer
	entry := completeEntry()
	entry.Modes = []string{"thinking"} // skip the modes prompt
	_, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("focused,loose\n"), &out, tr)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if !slices.Equal(entry.Engagements, []string{"focused", "loose"}) {
		t.Fatal(entry.Engagements)
	}
	text := out.String()
	for _, want := range []string{"focused — Give it your full attention", "loose — Keep it low-key"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q in:\n%s", want, text)
		}
	}
	// "clear" empties the set; blank keeps it.
	entry = completeEntry()
	entry.Modes = []string{"thinking"}
	if _, err := promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("clear\n"), &out, tr); err != nil {
		t.Fatal(err, out.String())
	}
	if !entry.ClearEngagements || entry.Engagements != nil {
		t.Fatal(entry.ClearEngagements, entry.Engagements)
	}
	// Invalid tokens fail with the allowed list.
	entry = completeEntry()
	entry.Modes = []string{"thinking"}
	var errout bytes.Buffer
	_, err = promptEntry(&entry, nil, config.NewConfig(nil), strings.NewReader("focused,zen\n"), &errout, tr)
	if err == nil {
		t.Fatal("invalid engagement must fail")
	}
	var arg *argumentError
	if !errors.As(err, &arg) || arg.Argument != "engagement" {
		t.Fatalf("got %v", err)
	}
	if strings.Join(arg.Constraint.Allowed, ",") != "focused,loose" {
		t.Fatal(arg.Constraint.Allowed)
	}
}
