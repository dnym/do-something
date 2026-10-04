package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// helpOutput runs the CLI with the given arguments (no --db, non-interactive)
// and returns the rendered help. Help never touches a store.
func helpOutput(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errout bytes.Buffer
	in, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	code := Run(args, in, &out, &errout)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, errout.String())
	}
	if strings.Contains(out.String(), "i-ds") {
		t.Fatalf("reflected field name leaked into help:\n%s", out.String())
	}
	return out.String()
}
func assertContains(t *testing.T, out string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("help output missing %q in:\n%s", w, out)
		}
	}
}
func TestHelpRoot(t *testing.T) {
	out := helpOutput(t, "--help")
	assertContains(t, out, []string{
		"Usage: do-something [command] [flags]",
		"With no command, suggest is the default.",
		// usage summaries keep positionals visible
		"start <id|title...>",
		"deps <action> <id|title> [id|title...]",
		"import <file>",
		"config [action] [key] [value]",
		"completion <shell>",
		// global flags with hints
		"--format=text|json (default: text)",
		"--lang=en|sv",
		// Values section (canonical, constraint-derived)
		"project, media",
		"not_started, in_progress, done, dropped",
		"none, low, medium, high, very_high, max",
		"intensity, career, physical, mental, social, interest, actuality, influence, quality",
		"0..1",
		">= 0",
		"YYYY-MM-DD",
		"Ratings (0..1) by kind",
		"Measurements by kind",
		"Exit codes",
		// new root-page lines: default kind + help topics
		"suggest ranks projects by default",
		"Help topics (do-something help <topic>):",
		"kind, type, category, intensity, career, physical, mental, social, interest, actuality, influence, quality, remaining_duration, cost_left, min_session_duration, total_duration, deadline, dependencies, status, effort, ongoing, modes, engagement, properties, scoring, filters",
		"mode              movement, hands_on, thinking, making",
		"engagement        focused, loose",
		"kind-filter",
	})
}
func TestHelpRootAlignsCommandColumn(t *testing.T) {
	out := helpOutput(t, "--help")
	commands := map[string]bool{}
	for _, n := range []string{"suggest", "list", "show", "add", "edit", "start", "done", "drop", "reopen", "deps", "import", "export", "snapshot", "sync", "config", "doctor", "schema", "version", "completion", "help"} {
		commands[n] = true
	}
	// Usage strings are lowercase/punctuation only, so the first uppercase
	// character of a command line marks the description column.
	descCol, checked := -1, 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "  --") {
			continue
		}
		first := strings.Fields(line)
		if len(first) == 0 || !commands[first[0]] {
			continue
		}
		col := -1
		for i := 0; i < len(line); i++ {
			if c := line[i]; c >= 'A' && c <= 'Z' {
				col = i
				break
			}
		}
		checked++
		if descCol < 0 {
			descCol = col
		} else if col != descCol {
			t.Fatalf("descriptions misaligned (want %d, got %d):\n%s", descCol, col, line)
		}
	}
	if checked != len(commands) {
		t.Fatalf("checked %d of %d command lines", checked, len(commands))
	}
}
func TestHelpCommandPages(t *testing.T) {
	out := helpOutput(t, "add", "--help")
	assertContains(t, out, []string{
		"Usage: do-something add [flags]",
		"--title=TITLE",
		"--kind=project|media",
		"--deadline=YYYY-MM-DD",
		"--remaining-duration=DURATION",
		"--intensity=LEVEL|0..1",
		"--tag=TAG,...",
		"--clear-deadline",
		"Global flags",
	})
	out = helpOutput(t, "suggest", "--help")
	assertContains(t, out, []string{
		"Usage: do-something suggest [flags]",
		"--effort=none|low|medium|high|very_high|max",
		"--session=DURATION alias: --time",
		"--mode=movement|hands_on|thinking|making",
		"--engagement=focused|loose",
		"--min-score=0..1",
		"--unknown=include|exclude",
		"--fail-empty",
		"--kind=project|media|both",
		"defaults to project",
		"never excludes items",
		"Notes",
	})
	out = helpOutput(t, "list", "--help")
	assertContains(t, out, []string{
		"Usage: do-something list [flags]",
		"--status=not_started|in_progress|done|dropped",
		"--mode=movement|hands_on|thinking|making",
		"--engagement=focused|loose",
		"never excludes items",
		"Notes",
	})
	out = helpOutput(t, "deps", "--help")
	assertContains(t, out, []string{
		"Usage: do-something deps <action> <id|title> [id|title...]",
		"Positionals",
		"list | add | remove",
	})
	out = helpOutput(t, "config", "-h")
	assertContains(t, out, []string{
		"Usage: do-something config [action] [key] [value]",
		"get | set | unset",
	})
}
func TestHelpDispatch(t *testing.T) {
	// Value-taking global flags consume their value before the command, so a
	// value can never be mistaken for a command name.
	out := helpOutput(t, "--db", "/nonexistent", "list", "--help")
	assertContains(t, out, []string{"Usage: do-something list [flags]"})
	out = helpOutput(t, "--db", "list", "--help")
	if !strings.Contains(out, "Usage: do-something [command] [flags]") || strings.Contains(out, "Usage: do-something list") {
		t.Fatalf("--db value must not select a command page:\n%s", out)
	}
	// An unknown first token is not a command: the root page is shown.
	out = helpOutput(t, "frobnicate", "--help")
	assertContains(t, out, []string{"Usage: do-something [command] [flags]"})
	// Help after command flags still selects the command page.
	out = helpOutput(t, "list", "--effort", "low", "--help")
	assertContains(t, out, []string{"Usage: do-something list [flags]"})
	out = helpOutput(t, "-h")
	assertContains(t, out, []string{"Usage: do-something [command] [flags]"})
}
func TestHelpTopics(t *testing.T) {
	out := helpOutput(t, "help")
	assertContains(t, out, []string{
		"Usage: do-something [command] [flags]",
		"Help topics (do-something help <topic>):",
		"kind, type, category, intensity, career, physical, mental, social, interest, actuality, influence, quality, remaining_duration, cost_left, min_session_duration, total_duration, deadline, dependencies, status, effort, ongoing, modes, engagement, properties, scoring, filters",
	})
	out = helpOutput(t, "help", "kind")
	assertContains(t, out, []string{
		"project — something you do for benefit or necessity",
		"media — something you consume for relaxation, fun, or interest",
		"Make the decision itself a project",
	})
	out = helpOutput(t, "help", "type")
	assertContains(t, out, []string{"vocabulary.types", "No types are built in"})
	out = helpOutput(t, "help", "category")
	assertContains(t, out, []string{"vocabulary.categories"})
	out = helpOutput(t, "help", "properties")
	assertContains(t, out, []string{"intensity, career, physical, mental, social, interest"})
	out = helpOutput(t, "help", "scoring")
	assertContains(t, out, []string{"(1 - beta) * base + beta * right-now", "scoring.prior"})
	out = helpOutput(t, "help", "filters")
	assertContains(t, out, []string{"--kind project|media|both", "combine with AND", "--mode movement|hands_on|thinking|making", "--engagement focused|loose"})
	out = helpOutput(t, "help", "modes")
	assertContains(t, out, []string{
		"movement — move my body",
		"thinking — use my mind",
		"making — make something",
		"The making line",
		"birdhouse is hands_on + making",
		"unclassified",
		"mode_match",
		"never excludes",
		"--clear-modes",
	})
	out = helpOutput(t, "help", "engagement")
	assertContains(t, out, []string{
		"focused — give it your full attention",
		"loose — keep it low-key",
		"not an intensity dial",
		"engagement_match",
		"absorb something hard with my mind",
		"--clear-engagements",
	})
	out = helpOutput(t, "help", "intensity")
	assertContains(t, out, []string{
		"never enters the score",
		"--effort filter",
		"at or below the level's ceiling",
		"scoring.half_life.intensity",
	})
	out = helpOutput(t, "help", "career")
	assertContains(t, out, []string{
		"professional development",
		"scoring.weights.project.career",
		"36-month half-life",
	})
	out = helpOutput(t, "help", "physical")
	assertContains(t, out, []string{"physical health and fitness", "scoring.half_life.physical"})
	out = helpOutput(t, "help", "mental")
	assertContains(t, out, []string{"mental health, cognitive function", "scoring.half_life.mental"})
	out = helpOutput(t, "help", "social")
	assertContains(t, out, []string{"relationships and community", "scoring.half_life.social"})
	out = helpOutput(t, "help", "interest")
	assertContains(t, out, []string{
		"fastest-decaying rating",
		"18-month half-life",
		"scoring.half_life.interest",
	})
	out = helpOutput(t, "help", "actuality")
	assertContains(t, out, []string{"current media and debate", "12-month half-life", "scoring.half_life.actuality"})
	out = helpOutput(t, "help", "influence")
	assertContains(t, out, []string{"shaped its genre or culture", "never decays", "scoring.half_life.influence"})
	out = helpOutput(t, "help", "quality")
	assertContains(t, out, []string{"how good the work is", "never decays", "scoring.half_life.quality"})
	out = helpOutput(t, "help", "remaining_duration")
	assertContains(t, out, []string{
		"scoring.k.remaining_duration",
		"scoring.k_pace",
		"--finish-within",
		"--clear-remaining-duration",
	})
	out = helpOutput(t, "help", "cost_left")
	assertContains(t, out, []string{
		"ships without a default",
		"scoring.k.cost_left",
		"scoring.cost_unit",
		"--budget N",
	})
	out = helpOutput(t, "help", "min_session_duration")
	assertContains(t, out, []string{
		"smallest worthwhile session",
		"--session DURATION",
		"defaults.min_session_duration",
		"--clear-min-session-duration",
	})
	out = helpOutput(t, "help", "total_duration")
	assertContains(t, out, []string{
		"estimated_progress_percent",
		"scoring.duration_anchors",
		"--finish-within",
		"--clear-total-duration",
	})
	out = helpOutput(t, "help", "deadline")
	assertContains(t, out, []string{
		"scoring.deadline_horizon_days",
		"scoring.k_pace",
		"scoring.no_deadline_horizon",
		"--clear-deadline",
	})
	out = helpOutput(t, "help", "dependencies")
	assertContains(t, out, []string{
		"blocked",
		"--include-unready",
		"deps remove",
		"CYCLE",
		"--depends-on",
	})
	out = helpOutput(t, "help", "status")
	assertContains(t, out, []string{
		"no hard delete",
		"changed: false",
		"all-or-nothing",
		"reopen writes none",
		"not_started and in_progress items",
	})
	out = helpOutput(t, "help", "effort")
	assertContains(t, out, []string{
		"keeps items with intensity ≤ 1/3",
		"keeps items with intensity ≤ 2/3",
		"applies no limit",
		"hard filter",
		"scoring.effort.max",
	})
	// A command name resolves to the command page.
	out = helpOutput(t, "help", "add")
	assertContains(t, out, []string{"Usage: do-something add [flags]"})
	out = helpOutput(t, "help", "help")
	assertContains(t, out, []string{"Usage: do-something help [topic]"})
	// The kind note is suggest-only: the list page must not carry it.
	out = helpOutput(t, "list", "--help")
	if strings.Contains(out, "defaults to project") {
		t.Fatalf("kind note must be suggest-only:\n%s", out)
	}
}
func TestHelpUnknownTopic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errout bytes.Buffer
	in, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	code := Run([]string{"--format", "json", "help", "nope"}, in, &out, &errout)
	if code != 2 {
		t.Fatalf("exit %d: %s", code, errout.String())
	}
	var v struct {
		Error struct {
			Code     string
			Argument string
			Allowed  []string
		}
	}
	if e := json.Unmarshal(errout.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.Error.Code != "INVALID_ARGUMENT" || v.Error.Argument != "topic" {
		t.Fatal(v.Error)
	}
	for _, want := range []string{"kind", "scoring", "suggest", "add"} {
		if !slices.Contains(v.Error.Allowed, want) {
			t.Fatalf("allowed = %v", v.Error.Allowed)
		}
	}
}
func TestHelpSwedish(t *testing.T) {
	out := helpOutput(t, "--lang", "sv", "--help")
	// Prose is localized; value lists and flag names stay canonical.
	assertContains(t, out, []string{
		"Användning: do-something [kommando] [flaggor]",
		"Kommandon",
		"Flaggor",
		"(standard: text)",
		"Värden",
		"project, media",
		"start <id|title...>",
		"Hjälpämnen (do-something help <topic>):",
	})
	// Topic pages are localized too (sv draft pending proofread).
	out = helpOutput(t, "--lang", "sv", "help", "effort")
	assertContains(t, out, []string{
		"effort är värdet --effort LEVEL",
		"hårt filter",
		"scoring.effort.max",
	})
	out = helpOutput(t, "--lang", "sv", "help", "intensity")
	assertContains(t, out, []string{
		"ingår aldrig i poängen",
		"scoring.half_life.intensity",
	})
	out = helpOutput(t, "--lang", "sv", "help", "modes")
	assertContains(t, out, []string{
		"movement — rör på mig",
		"making — skapa något",
		"scoring.term.mode_match",
	})
	out = helpOutput(t, "--lang", "sv", "help", "engagement")
	assertContains(t, out, []string{
		"focused — lägg upp fullt fokus",
		"loose — håll det slapat",
		"scoring.term.engagement_match",
	})
}
