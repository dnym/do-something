package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dosomething/internal/model"
)

// openTestStore opens a fresh throwaway store in a temp dir and closes it at
// test end.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(OpenWrite, Options{DBPath: filepath.Join(dir, "list.db"), StateDir: dir})
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// addTestItem creates a minimal item and returns its id.
func addTestItem(t *testing.T, st *Store, title string, kind model.Kind) string {
	t.Helper()
	id, err := st.Save(ItemSpec{Title: title, Kind: kind}, true)
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return id
}

// TestSaveGetRoundTrip verifies create + full-field readback for a project
// with every column populated.
func TestSaveGetRoundTrip(t *testing.T) {
	st := openTestStore(t)
	deadline := time.Date(2026, 11, 1, 18, 30, 0, 0, time.UTC)
	rated := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	tags := []string{"  Home ", "urgent"}
	id, err := st.Save(ItemSpec{
		Kind:               model.KindProject,
		Title:              "Fix the heat pump",
		Category:           model.StrPtr("home"),
		Type:               model.StrPtr("task"),
		Notes:              model.StrPtr("call the service company"),
		URL:                model.StrPtr("https://example.org/heat"),
		LegacyID:           model.IntPtr(42),
		Deadline:           &deadline,
		RemainingDuration:  model.FloatPtr(3.5),
		CostLeft:           model.FloatPtr(120),
		MinSessionDuration: model.FloatPtr(1),
		SetRatings: map[model.Property]model.Rating{
			model.PropCareer:   {Property: model.PropCareer, Value: 0.5},
			model.PropInterest: {Property: model.PropInterest, Value: 1, RatedAt: &rated},
		},
		Tags: &tags,
	}, true)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id == "" {
		t.Fatal("empty id returned")
	}

	it, err := st.GetItem(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.Title != "Fix the heat pump" || it.Kind != model.KindProject || it.Status != model.StatusNotStarted {
		t.Errorf("basic fields: %+v", it)
	}
	if it.Category == nil || *it.Category != "home" {
		t.Errorf("category: %v", it.Category)
	}
	if it.Type == nil || *it.Type != "task" {
		t.Errorf("type: %v", it.Type)
	}
	if it.Notes == nil || *it.Notes != "call the service company" {
		t.Errorf("notes: %v", it.Notes)
	}
	if it.URL == nil || *it.URL != "https://example.org/heat" {
		t.Errorf("url: %v", it.URL)
	}
	if it.LegacyID == nil || *it.LegacyID != 42 {
		t.Errorf("legacy_id: %v", it.LegacyID)
	}
	if it.Deadline == nil || !it.Deadline.Equal(deadline) {
		t.Errorf("deadline: %v", it.Deadline)
	}
	for _, c := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"remaining_duration", it.RemainingDuration, 3.5},
		{"cost_left", it.CostLeft, 120},
		{"min_session_duration", it.MinSessionDuration, 1},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s: got %v want %v", c.name, c.got, c.want)
		}
	}
	if got := it.RatingValue(model.PropCareer); got == nil || *got != 0.5 {
		t.Errorf("career rating: %v", got)
	}
	if r, ok := it.Ratings[model.PropInterest]; !ok || r.Value != 1 || r.RatedAt == nil || !r.RatedAt.Equal(rated) {
		t.Errorf("interest rating: %+v", r)
	}
	if len(it.Tags) != 2 || it.Tags[0] != "  Home " || it.Tags[1] != "urgent" {
		t.Errorf("tags (preserve+sort): %v", it.Tags)
	}
	// Property states: known for set values, unknown for applicable-but-empty,
	// not_applicable for foreign-kind properties.
	if st := it.PropertyStateOf(model.PropRemainingDuration); st != model.StateKnown {
		t.Errorf("remaining_duration state: %s", st)
	}
	if st := it.PropertyStateOf(model.PropMental); st != model.StateUnknown {
		t.Errorf("mental state: %s", st)
	}
	if st := it.PropertyStateOf(model.PropTotalDuration); st != model.StateUnknown {
		t.Errorf("total_duration state: %s", st)
	}

	all, err := st.AllItems()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 1 || all[0].ID != id {
		t.Errorf("all items: %v", all)
	}
}

// TestItemModesAndEngagementsCRUD: modes and engagements behave like tags —
// full replacement when the spec is non-nil, an empty slice clears the set,
// nil keeps the existing set, and invalid tokens are rejected all-or-nothing.
func TestItemModesAndEngagementsCRUD(t *testing.T) {
	st := openTestStore(t)
	empty := []string{}
	id, err := st.Save(ItemSpec{Title: "Modal", Kind: model.KindProject, Modes: &empty, Engagements: &empty}, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	it, err := st.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Modes == nil || len(it.Modes) != 0 {
		t.Fatalf("modes after create = %v, want non-nil empty", it.Modes)
	}
	if it.Engagements == nil || len(it.Engagements) != 0 {
		t.Fatalf("engagements after create = %v, want non-nil empty", it.Engagements)
	}

	two := []string{"movement", "thinking"}
	focused := []string{"focused"}
	if _, err := st.Save(ItemSpec{ID: id, Modes: &two, Engagements: &focused}, false); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id)
	if len(it.Modes) != 2 || it.Modes[0] != "movement" || it.Modes[1] != "thinking" {
		t.Fatalf("modes after set = %v, want sorted [movement thinking]", it.Modes)
	}
	if len(it.Engagements) != 1 || it.Engagements[0] != "focused" {
		t.Fatalf("engagements after set = %v, want [focused]", it.Engagements)
	}

	one := []string{"hands_on"}
	loose := []string{"loose"}
	if _, err := st.Save(ItemSpec{ID: id, Modes: &one, Engagements: &loose}, false); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id)
	if len(it.Modes) != 1 || it.Modes[0] != "hands_on" {
		t.Fatalf("modes after replace = %v, want [hands_on] (full replacement)", it.Modes)
	}
	if len(it.Engagements) != 1 || it.Engagements[0] != "loose" {
		t.Fatalf("engagements after replace = %v, want [loose] (full replacement)", it.Engagements)
	}

	// A nil spec leaves both sets untouched.
	if _, err := st.Save(ItemSpec{ID: id, Title: "Modal v2"}, false); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id)
	if len(it.Modes) != 1 || it.Modes[0] != "hands_on" {
		t.Fatalf("modes after unrelated edit = %v, want [hands_on]", it.Modes)
	}
	if len(it.Engagements) != 1 || it.Engagements[0] != "loose" {
		t.Fatalf("engagements after unrelated edit = %v, want [loose]", it.Engagements)
	}

	if _, err := st.Save(ItemSpec{ID: id, Modes: &empty, Engagements: &empty}, false); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id)
	if it.Modes == nil || len(it.Modes) != 0 {
		t.Fatalf("modes after clear = %v, want non-nil empty", it.Modes)
	}
	if it.Engagements == nil || len(it.Engagements) != 0 {
		t.Fatalf("engagements after clear = %v, want non-nil empty", it.Engagements)
	}

	// An invalid token fails and rolls the whole change back.
	bad := []string{"making", "zen"}
	if _, err := st.Save(ItemSpec{ID: id, Modes: &bad}, false); err == nil {
		t.Fatal("invalid mode accepted")
	}
	badEng := []string{"loose", "zen"}
	if _, err := st.Save(ItemSpec{ID: id, Engagements: &badEng}, false); err == nil {
		t.Fatal("invalid engagement accepted")
	}
	it, _ = st.GetItem(id)
	if it.Modes == nil || len(it.Modes) != 0 {
		t.Fatalf("failed save must not change modes: %v", it.Modes)
	}
	if it.Engagements == nil || len(it.Engagements) != 0 {
		t.Fatalf("failed save must not change engagements: %v", it.Engagements)
	}
}

// TestSaveCreateValidation pins the create-time validation rules.
func TestSaveCreateValidation(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.Save(ItemSpec{Kind: model.KindProject}, true); err == nil {
		t.Error("empty title accepted")
	}
	if _, err := st.Save(ItemSpec{Title: "x", Kind: "bogus"}, true); err == nil {
		t.Error("invalid kind accepted")
	}
	if _, err := st.Save(ItemSpec{Title: "x", Kind: model.KindProject, ID: "fixed"}, true); err == nil {
		t.Error("fixed id on create accepted")
	}
	if _, err := st.Save(ItemSpec{ID: "nope"}, false); !ItemNotFound(err) {
		t.Errorf("edit of unknown id: %v", err)
	}
}

// TestSaveEditLeavesUnsetFields verifies three-state field semantics on edit:
// explicit value → set, clear flag → NULL, nil → unchanged.
func TestSaveEditLeavesUnsetFields(t *testing.T) {
	st := openTestStore(t)
	id := addTestItem(t, st, "Read Dune", model.KindMedia)
	wh := 2.5
	if _, err := st.Save(ItemSpec{ID: id, TotalDuration: model.FloatPtr(10), RemainingDuration: &wh}, false); err != nil {
		t.Fatalf("set: %v", err)
	}
	it, _ := st.GetItem(id)
	if it.TotalDuration == nil || *it.TotalDuration != 10 {
		t.Fatalf("total_duration: %v", it.TotalDuration)
	}

	// Edit only the title: everything else is preserved.
	if _, err := st.Save(ItemSpec{ID: id, Title: "Dune"}, false); err != nil {
		t.Fatalf("edit title: %v", err)
	}
	it, _ = st.GetItem(id)
	if it.Title != "Dune" {
		t.Errorf("title: %q", it.Title)
	}
	if it.TotalDuration == nil || *it.TotalDuration != 10 {
		t.Errorf("total_duration lost on unrelated edit: %v", it.TotalDuration)
	}

	// Clear an explicit value back to unknown.
	if _, err := st.Save(ItemSpec{ID: id, ClearTotalDuration: true}, false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	it, _ = st.GetItem(id)
	if it.TotalDuration != nil {
		t.Errorf("clear-total-duration left value: %v", *it.TotalDuration)
	}
	if st := it.PropertyStateOf(model.PropTotalDuration); st != model.StateUnknown {
		t.Errorf("state after clear: %s", st)
	}

	// Tags: non-nil replaces the full set (empty clears), nil leaves untouched.
	one := []string{"sci-fi"}
	if _, err := st.Save(ItemSpec{ID: id, Tags: &one}, false); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	it, _ = st.GetItem(id)
	if len(it.Tags) != 1 || it.Tags[0] != "sci-fi" {
		t.Errorf("tags: %v", it.Tags)
	}
	none := []string{}
	if _, err := st.Save(ItemSpec{ID: id, Tags: &none}, false); err != nil {
		t.Fatalf("clear tags: %v", err)
	}
	it, _ = st.GetItem(id)
	if len(it.Tags) != 0 {
		t.Errorf("cleared tags: %v", it.Tags)
	}
	if _, err := st.Save(ItemSpec{ID: id}, false); err != nil {
		t.Fatalf("no-op edit: %v", err)
	}
	it, _ = st.GetItem(id)
	if it.Tags == nil || len(it.Tags) != 0 {
		t.Errorf("nil tags must leave the (empty) set alone: %v", it.Tags)
	}
}

// TestRatingsUpsertUnset verifies rating row upsert, explicit rated_at
// round-trip, and first-class unset.
func TestRatingsUpsertUnset(t *testing.T) {
	st := openTestStore(t)
	id := addTestItem(t, st, "Film", model.KindMedia)
	rated := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	if _, err := st.Save(ItemSpec{ID: id, SetRatings: map[model.Property]model.Rating{
		model.PropQuality: {Property: model.PropQuality, Value: 0.2, RatedAt: &rated},
	}}, false); err != nil {
		t.Fatalf("set rating: %v", err)
	}
	// Upsert the same property: value replaced, still one row.
	if _, err := st.Save(ItemSpec{ID: id, SetRatings: map[model.Property]model.Rating{
		model.PropQuality: {Property: model.PropQuality, Value: 0.8},
	}}, false); err != nil {
		t.Fatalf("upsert rating: %v", err)
	}
	it, _ := st.GetItem(id)
	r, ok := it.Ratings[model.PropQuality]
	if !ok || r.Value != 0.8 {
		t.Fatalf("upserted rating: %+v", r)
	}
	// An undated upsert stores NULL rated_at (e.g. imported values).
	if r.RatedAt != nil {
		t.Errorf("rated_at not reset: %v", *r.RatedAt)
	}
	// Unset: the row is deleted, the property becomes unknown again.
	if _, err := st.Save(ItemSpec{ID: id, UnsetRatings: []model.Property{model.PropQuality}}, false); err != nil {
		t.Fatalf("unset rating: %v", err)
	}
	it, _ = st.GetItem(id)
	if len(it.Ratings) != 0 {
		t.Errorf("unset left rows: %v", it.Ratings)
	}
	if st := it.PropertyStateOf(model.PropQuality); st != model.StateUnknown {
		t.Errorf("state after unset: %s", st)
	}
}

// TestDependencyAddRemoveCycle verifies edge CRUD, endpoint validation, and
// cycle rejection.
func TestDependencyAddRemoveCycle(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "A", model.KindProject)
	b := addTestItem(t, st, "B", model.KindProject)
	c := addTestItem(t, st, "C", model.KindProject)

	if err := st.AddDependency(c, a); err != nil { // c depends on a
		t.Fatalf("c->a: %v", err)
	}
	if err := st.AddDependency(b, c); err != nil { // b depends on c
		t.Fatalf("b->c: %v", err)
	}
	// a -> b would close the loop a -> b -> c -> a.
	if err := st.AddDependency(a, b); !errors.Is(err, ErrCycle) {
		t.Errorf("expected cycle error, got %v", err)
	}
	if err := st.AddDependency(a, a); !errors.Is(err, ErrCycle) {
		t.Errorf("expected self-edge error, got %v", err)
	}
	if _, err := st.GetItem("missing"); err == nil {
		t.Error("sanity")
	}
	if err := st.AddDependency(a, "missing"); !ItemNotFound(err) {
		t.Errorf("missing endpoint: %v", err)
	}

	deps, err := st.Dependencies(b)
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if len(deps) != 1 || deps[0] != c {
		t.Errorf("dependencies(b): %v", deps)
	}
	dependents, err := st.Dependents(c)
	if err != nil {
		t.Fatalf("dependents: %v", err)
	}
	if len(dependents) != 1 || dependents[0] != b {
		t.Errorf("dependents(c): %v", dependents)
	}

	// Remove is idempotent.
	if err := st.RemoveDependency(c, a); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := st.RemoveDependency(c, a); err != nil {
		t.Fatalf("remove again: %v", err)
	}
	deps, _ = st.Dependencies(c)
	if len(deps) != 0 {
		t.Errorf("deps after remove: %v", deps)
	}
}

// TestSaveDependsOnReplaceAllOrNothing verifies full-set replacement through
// Save and that a failing spec leaves the item completely untouched.
func TestSaveDependsOnReplaceAllOrNothing(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "A", model.KindProject)
	b := addTestItem(t, st, "B", model.KindProject)

	ok := []string{b}
	if _, err := st.Save(ItemSpec{ID: a, DependsOn: &ok}, false); err != nil {
		t.Fatalf("set deps: %v", err)
	}
	it, _ := st.GetItem(a)
	if len(it.Dependencies) != 1 || it.Dependencies[0] != b {
		t.Fatalf("deps: %v", it.Dependencies)
	}
	// A failing spec (unknown target, and a self-edge) rolls everything back.
	bad := []string{b, "missing"}
	if _, err := st.Save(ItemSpec{ID: a, DependsOn: &bad, Title: "CHANGED"}, false); err == nil {
		t.Fatal("expected error for unknown dependency target")
	}
	self := []string{a}
	if _, err := st.Save(ItemSpec{ID: a, DependsOn: &self}, false); !errors.Is(err, ErrCycle) {
		t.Errorf("expected self-edge cycle error, got %v", err)
	}
	it, _ = st.GetItem(a)
	if it.Title != "A" {
		t.Errorf("rollback failed: title %q", it.Title)
	}
	if len(it.Dependencies) != 1 || it.Dependencies[0] != b {
		t.Errorf("rollback failed: deps %v", it.Dependencies)
	}
}

// TestStatusTransitions verifies the verb table, idempotency (current ==
// target → changed=false, no event), and event emission on change.
func TestStatusTransitions(t *testing.T) {
	st := openTestStore(t)
	id := addTestItem(t, st, "Task", model.KindProject)

	count := func(et model.EventType) int {
		evs, err := st.EventsByType(et)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		return len(evs)
	}

	res, err := st.TransitionStatus(Start, []string{id})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !res[0].Changed || res[0].From != model.StatusNotStarted || res[0].To != model.StatusInProgress {
		t.Errorf("start result: %+v", res[0])
	}
	if got := count(model.EventStarted); got != 1 {
		t.Errorf("started events: %d", got)
	}

	// start on in_progress: no-op, no new event.
	res, err = st.TransitionStatus(Start, []string{id})
	if err != nil {
		t.Fatalf("start again: %v", err)
	}
	if res[0].Changed {
		t.Errorf("start must be idempotent: %+v", res[0])
	}
	if got := count(model.EventStarted); got != 1 {
		t.Errorf("started events after no-op: %d", got)
	}

	// done from in_progress.
	if _, err = st.TransitionStatus(Done, []string{id}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if got := count(model.EventCompleted); got != 1 {
		t.Errorf("completed events: %d", got)
	}
	// done twice: no-op.
	res, _ = st.TransitionStatus(Done, []string{id})
	if res[0].Changed {
		t.Errorf("done must be idempotent: %+v", res[0])
	}
	// drop from done.
	if _, err = st.TransitionStatus(Drop, []string{id}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got := count(model.EventDropped); got != 1 {
		t.Errorf("dropped events: %d", got)
	}
	// reopen from dropped → not_started, and reopen emits no event.
	res, err = st.TransitionStatus(Reopen, []string{id})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !res[0].Changed || res[0].To != model.StatusNotStarted {
		t.Errorf("reopen result: %+v", res[0])
	}
	total := count(model.EventSuggested) + count(model.EventStarted) + count(model.EventCompleted) + count(model.EventDropped)
	if total != 3 {
		t.Errorf("reopen must not emit an event (total %d, want 3)", total)
	}
	// reopen on not_started: no-op.
	res, _ = st.TransitionStatus(Reopen, []string{id})
	if res[0].Changed {
		t.Errorf("reopen on not_started must be a no-op: %+v", res[0])
	}
}

// TestTransitionMultiIDAllOrNothing verifies the all-or-nothing transaction
// and that the failure lists every offending id with its code.
func TestTransitionMultiIDAllOrNothing(t *testing.T) {
	st := openTestStore(t)
	good := addTestItem(t, st, "Good", model.KindProject)
	other := addTestItem(t, st, "Other", model.KindProject)

	_, err := st.TransitionStatus(Done, []string{good, "missing-id"})
	var te *TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("expected TransitionError, got %v", err)
	}
	if len(te.Problems) != 1 || te.Problems[0].ID != "missing-id" || te.Problems[0].Code != "INVALID_ID" {
		t.Errorf("problems: %+v", te.Problems)
	}
	// All-or-nothing: the good id was rolled back.
	it, _ := st.GetItem(good)
	if it.Status != model.StatusNotStarted {
		t.Errorf("rollback failed: status %s", it.Status)
	}
	it, _ = st.GetItem(other)
	if it.Status != model.StatusNotStarted {
		t.Errorf("rollback failed: status %s", it.Status)
	}
	// And the same batch succeeds once the id is valid.
	res, err := st.TransitionStatus(Done, []string{good, other})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	for _, r := range res {
		if !r.Changed {
			t.Errorf("expected change: %+v", r)
		}
	}
}

// TestResolveID verifies exact, unique-prefix, ambiguous-prefix, and
// unknown-prefix resolution.
func TestResolveID(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "Alpha", model.KindProject)
	b := addTestItem(t, st, "Beta", model.KindMedia)

	if got, err := st.ResolveID(a); err != nil || got != a {
		t.Errorf("exact: %v %v", got, err)
	}
	// UUIDv7 ids created milliseconds apart share their timestamp prefix
	// (top 16 bits of the ms field → a ~65 s window), so a 4-char prefix
	// deterministically matches both.
	prefix := a[:4]
	_, perr := st.ResolveID(prefix)
	if !IsAmbiguousID(perr) {
		t.Fatalf("prefix %q: expected ambiguous, got %v", prefix, perr)
	}
	var ae *AmbiguousIDError
	errors.As(perr, &ae)
	if len(ae.Candidates) != 2 || ae.Candidates[0] != a || ae.Candidates[1] != b {
		t.Errorf("candidates: %v", ae.Candidates)
	}
	if !errors.Is(ae, ErrInvalidID) {
		t.Errorf("ambiguous error must be an INVALID_ID condition")
	}
	// Same-millisecond UUIDv7s share the 12 timestamp chars and may share
	// further random chars, so find the shortest prefix of a that resolves
	// uniquely (one always exists: full ids differ).
	n := 8
	for n < len(a) {
		if got, err := st.ResolveID(a[:n]); err == nil && got == a {
			break
		}
		n++
	}
	if got, err := st.ResolveID(a[:n]); err != nil || got != a {
		t.Errorf("unique prefix %q: %v %v", a[:n], got, err)
	}
	if _, err := st.ResolveID("ffffffff"); !ItemNotFound(err) {
		t.Errorf("unknown prefix: %v", err)
	}
	if _, err := st.ResolveID(""); !ItemNotFound(err) {
		t.Errorf("empty prefix: %v", err)
	}
}

// TestResolveItemRef verifies reference resolution: id and unique id prefix
// take priority over titles, a unique exact (case-sensitive) title resolves,
// and an ambiguous title fails listing its candidates.
func TestResolveItemRef(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "Alpha", model.KindProject)
	addTestItem(t, st, "Beta", model.KindProject)

	// Id and prefix forms behave exactly as ResolveID.
	if got, err := st.ResolveItemRef(a); err != nil || got != a {
		t.Errorf("exact id: %v %v", got, err)
	}
	n := 8
	for n < len(a) {
		if got, err := st.ResolveItemRef(a[:n]); err == nil && got == a {
			break
		}
		n++
	}
	if got, err := st.ResolveItemRef(a[:n]); err != nil || got != a {
		t.Errorf("unique prefix %q: %v %v", a[:n], got, err)
	}

	// A unique exact title resolves, including on non-not_started items (done
	// prerequisites are the common case).
	done := addTestItem(t, st, "Alpha done", model.KindProject)
	if _, err := st.TransitionStatus(Done, []string{done}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if got, err := st.ResolveItemRef("Alpha done"); err != nil || got != done {
		t.Errorf("title on done item: %v %v", got, err)
	}
	// Title matching is case-sensitive.
	if _, err := st.ResolveItemRef("alpha"); !ItemNotFound(err) {
		t.Errorf("case-sensitive title: %v", err)
	}

	// A title that is also an existing item's id resolves as the id.
	lookalike, err := st.Save(ItemSpec{Title: a, Kind: model.KindProject}, true)
	if err != nil {
		t.Fatalf("add lookalike: %v", err)
	}
	if got, err := st.ResolveItemRef(a); err != nil || got != a {
		t.Errorf("id must win over a title of another item: %v %v", got, err)
	}
	_ = lookalike
	// A valid UUID string that is no item's id falls through to the title.
	ghost := "01234567-89ab-7def-8901-234567890abc"
	addTestItem(t, st, ghost, model.KindProject)
	if got, err := st.ResolveItemRef(ghost); err != nil || got == a {
		t.Errorf("uuid-shaped title: %v %v", got, err)
	}

	// An ambiguous title fails INVALID_ID and lists both candidates.
	t1 := addTestItem(t, st, "Twin", model.KindProject)
	t2 := addTestItem(t, st, "Twin", model.KindProject)
	_, err = st.ResolveItemRef("Twin")
	var ae *AmbiguousTitleError
	if !errors.As(err, &ae) {
		t.Fatalf("ambiguous title: expected AmbiguousTitleError, got %v", err)
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("ambiguous title must be an INVALID_ID condition")
	}
	want := []string{t1, t2}
	if t1 > t2 {
		want = []string{t2, t1}
	}
	if !slices.Equal(ae.Candidates, want) || ae.Title != "Twin" {
		t.Errorf("candidates: %v (want %v)", ae.Candidates, want)
	}

	// No id form and no title: the id-form failure is reported.
	if _, err := st.ResolveItemRef("no-such-thing"); !ItemNotFound(err) {
		t.Errorf("unknown reference: %v", err)
	}
	if _, err := st.ResolveItemRef(""); !ItemNotFound(err) {
		t.Errorf("empty reference: %v", err)
	}
}

// TestBlockedBy verifies readiness: a dependency unblocks only when done;
// dropped prerequisites stay blockers and are reported with their status.
func TestBlockedBy(t *testing.T) {
	st := openTestStore(t)
	main := addTestItem(t, st, "Main", model.KindProject)
	btodo := addTestItem(t, st, "BlockerTodo", model.KindProject)
	bdone := addTestItem(t, st, "BlockerDone", model.KindProject)
	bdrop := addTestItem(t, st, "BlockerDropped", model.KindProject)

	if _, err := st.TransitionStatus(Done, []string{bdone}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, err := st.TransitionStatus(Drop, []string{bdrop}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	deps := []string{btodo, bdone, bdrop}
	if _, err := st.Save(ItemSpec{ID: main, DependsOn: &deps}, false); err != nil {
		t.Fatalf("deps: %v", err)
	}

	blocked, err := st.BlockedBy(main)
	if err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if len(blocked) != 2 {
		t.Fatalf("blocked: %+v", blocked)
	}
	if blocked[0].ID != btodo || blocked[0].Status != model.StatusNotStarted {
		t.Errorf("blocker 0: %+v", blocked[0])
	}
	if blocked[1].ID != bdrop || blocked[1].Status != model.StatusDropped {
		t.Errorf("blocker 1 (dropped must stay a blocker): %+v", blocked[1])
	}

	// No dependencies → ready.
	ready := addTestItem(t, st, "Ready", model.KindProject)
	b, _ := st.BlockedBy(ready)
	if len(b) != 0 {
		t.Errorf("ready item blocked: %+v", b)
	}
	// All dependencies done → ready.
	ok := []string{bdone}
	if _, err := st.Save(ItemSpec{ID: ready, DependsOn: &ok}, false); err != nil {
		t.Fatalf("deps: %v", err)
	}
	b, _ = st.BlockedBy(ready)
	if len(b) != 0 {
		t.Errorf("all-done deps blocked: %+v", b)
	}
}

// TestConfigTier2 verifies the in-database config table: set/get/upsert/unset.
func TestConfigTier2(t *testing.T) {
	st := openTestStore(t)
	if _, ok, err := st.GetConfig("scoring.k.cost_left"); err != nil || ok {
		t.Fatalf("absent key: %v %v", ok, err)
	}
	if err := st.SetConfig("scoring.k.cost_left", "500"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, ok, _ := st.GetConfig("scoring.k.cost_left")
	if !ok || v != "500" {
		t.Errorf("get: %q %v", v, ok)
	}
	if err := st.SetConfig("scoring.k.cost_left", "600"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m, err := st.ConfigMap()
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if m["scoring.k.cost_left"] != "600" {
		t.Errorf("map: %v", m)
	}
	if removed, _ := st.UnsetConfig("scoring.k.cost_left"); !removed {
		t.Errorf("unset existing: %v", removed)
	}
	if _, ok, _ := st.GetConfig("scoring.k.cost_left"); ok {
		t.Error("key still present after unset")
	}
	if removed, _ := st.UnsetConfig("scoring.k.cost_left"); removed {
		t.Error("unset absent reported true")
	}
}

// TestLastSuggestedFor verifies the cooldown lookup: most recent suggested
// event per id, suggested events only, never-suggested items absent.
func TestLastSuggestedFor(t *testing.T) {
	st := openTestStore(t)
	x := addTestItem(t, st, "X", model.KindProject)
	y := addTestItem(t, st, "Y", model.KindMedia)
	z := addTestItem(t, st, "Z", model.KindProject)

	t1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	query := `{}`
	if _, err := st.AppendEvent(model.Event{Type: model.EventSuggested, ItemID: &x, ItemIDs: []string{x, y}, Query: &query, At: t1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := st.AppendEvent(model.Event{Type: model.EventSuggested, ItemID: &y, ItemIDs: []string{y}, Query: &query, At: t2}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// A non-suggested event must not count as a suggestion.
	if _, err := st.AppendEvent(model.Event{Type: model.EventStarted, ItemID: &z, At: t3}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := st.LastSuggestedFor(map[string]bool{x: true, y: true, z: true})
	if err != nil {
		t.Fatalf("last suggested: %v", err)
	}
	if !got[x].Equal(t1) {
		t.Errorf("x: %v want %v", got[x], t1)
	}
	if !got[y].Equal(t2) { // the later event wins
		t.Errorf("y: %v want %v", got[y], t2)
	}
	if _, ok := got[z]; ok {
		t.Errorf("z must be absent (only started, never suggested)")
	}

	// Event fields round-trip (item_id, item_ids, query, most-recent-first order).
	evs, err := st.EventsByType(model.EventSuggested)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("suggested events: %d", len(evs))
	}
	if !evs[0].At.Equal(t2) || evs[0].ItemID == nil || *evs[0].ItemID != y {
		t.Errorf("most recent first: %+v", evs[0])
	}
	if evs[1].ItemID == nil || *evs[1].ItemID != x {
		t.Errorf("item_id: %+v", evs[1])
	}
	if len(evs[1].ItemIDs) != 2 || evs[1].ItemIDs[0] != x || evs[1].ItemIDs[1] != y {
		t.Errorf("item_ids: %v", evs[1].ItemIDs)
	}
	if evs[1].Query == nil || *evs[1].Query != `{}` {
		t.Errorf("query: %v", evs[1].Query)
	}
}

// TestSaveStatusEvents verifies that creating or updating an item with a
// status records the same event the matching transition verb would have
// written (started / completed / dropped); not_started records none, and an
// unchanged status never writes a duplicate event.
func TestSaveStatusEvents(t *testing.T) {
	st := openTestStore(t)

	for status, ev := range map[model.Status]model.EventType{
		model.StatusInProgress: model.EventStarted,
		model.StatusDone:       model.EventCompleted,
		model.StatusDropped:    model.EventDropped,
	} {
		id, err := st.Save(ItemSpec{Title: string(status), Kind: model.KindProject, Status: &status}, true)
		if err != nil {
			t.Fatalf("create %s: %v", status, err)
		}
		count := 0
		for _, e := range mustEventsByType(t, st, ev) {
			if e.ItemID != nil && *e.ItemID == id {
				count++
			}
		}
		if count != 1 {
			t.Errorf("create %s: %s events %d, want 1", status, ev, count)
		}
	}

	// The default (not_started) create records no event at all.
	fresh, err := st.Save(ItemSpec{Title: "Fresh", Kind: model.KindProject}, true)
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	for _, ev := range []model.EventType{model.EventStarted, model.EventCompleted, model.EventDropped} {
		for _, e := range mustEventsByType(t, st, ev) {
			if e.ItemID != nil && *e.ItemID == fresh {
				t.Errorf("fresh must not carry a %s event", ev)
			}
		}
	}

	// Re-saving the same status is a no-op: no duplicate event.
	doneID, _ := st.Save(ItemSpec{Title: "DoneItem", Kind: model.KindProject, Status: strPtr(model.StatusDone)}, true)
	before := len(mustEventsByType(t, st, model.EventCompleted))
	if _, err := st.Save(ItemSpec{ID: doneID, Status: strPtr(model.StatusDone)}, false); err != nil {
		t.Fatalf("no-op edit: %v", err)
	}
	if after := len(mustEventsByType(t, st, model.EventCompleted)); after != before {
		t.Errorf("no-op edit wrote a duplicate event: %d -> %d", before, after)
	}

	// Editing to a new status appends the matching event.
	wip, _ := st.Save(ItemSpec{Title: "Wip", Kind: model.KindProject, Status: strPtr(model.StatusInProgress)}, true)
	before = len(mustEventsByType(t, st, model.EventCompleted))
	if _, err := st.Save(ItemSpec{ID: wip, Status: strPtr(model.StatusDone)}, false); err != nil {
		t.Fatalf("edit to done: %v", err)
	}
	if after := len(mustEventsByType(t, st, model.EventCompleted)); after != before+1 {
		t.Errorf("edit to done must append one completed event: %d -> %d", before, after)
	}

	// Back to not_started changes the status but appends no event, like reopen.
	before = len(mustEventsByType(t, st, model.EventStarted))
	if _, err := st.Save(ItemSpec{ID: wip, Status: strPtr(model.StatusNotStarted)}, false); err != nil {
		t.Fatalf("edit to not_started: %v", err)
	}
	if after := len(mustEventsByType(t, st, model.EventStarted)); after != before {
		t.Errorf("not_started must append no started event: %d -> %d", before, after)
	}
	item, err := st.GetItem(wip)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if item.Status != model.StatusNotStarted {
		t.Errorf("status: %s", item.Status)
	}
}

// mustEventsByType is the test-local helper around Store.EventsByType.
func mustEventsByType(t *testing.T, st *Store, et model.EventType) []model.Event {
	t.Helper()
	evs, err := st.EventsByType(et)
	if err != nil {
		t.Fatalf("events %s: %v", et, err)
	}
	return evs
}

func strPtr(s model.Status) *model.Status { return &s }
