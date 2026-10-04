package sync

import (
	"dosomething/internal/model"
	"dosomething/internal/store"
	"errors"
	"path/filepath"
	"testing"
	"time"
	"uuid"
)

func fresh(t *testing.T) *store.Store {
	t.Helper()
	s, e := store.Open(store.OpenWrite, store.Options{DBPath: filepath.Join(t.TempDir(), "list.db")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func clone(t *testing.T, s *store.Store) *store.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "list.db")
	if e := s.VacuumInto(path); e != nil {
		t.Fatal(e)
	}
	copy, e := store.Open(store.OpenWrite, store.Options{DBPath: path})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(copy.Close)
	return copy
}
func item(t *testing.T, s *store.Store, title string) string {
	t.Helper()
	id, e := s.Save(store.ItemSpec{Title: title, Kind: model.KindProject}, true)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func content(t *testing.T, s *store.Store) store.Content {
	t.Helper()
	c, e := s.Content()
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func event(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if _, e := s.AppendEvent(model.Event{ID: id, Type: model.EventStarted, At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); e != nil {
		t.Fatal(e)
	}
}
func TestDecisionTable(t *testing.T) {
	for _, test := range []struct {
		name, resolution string
		diverge, logs    bool
		want             string
		err              error
	}{{"identical", "", false, false, "noop", nil}, {"event union", "", false, true, "auto_union", nil}, {"requires choice", "", true, true, "requires_resolution", ErrConflict}, {"keep local", "keep-local", true, true, "keep_local", nil}, {"take remote", "take-remote", true, true, "take_remote", nil}, {"first peer cannot merge", "merge", true, true, "", store.ErrNoPeerBase}} {
		t.Run(test.name, func(t *testing.T) {
			l := fresh(t)
			id := item(t, l, "base")
			r := clone(t, l)
			if test.diverge {
				_, e := r.Save(store.ItemSpec{ID: id, Title: "remote"}, false)
				if e != nil {
					t.Fatal(e)
				}
			}
			if test.logs {
				event(t, l, uuid.NewV7().String())
				event(t, r, uuid.NewV7().String())
			}
			out, rep, e := Resolve(content(t, l), content(t, r), nil, Options{Resolution: test.resolution})
			if !errors.Is(e, test.err) {
				t.Fatalf("%v want %v", e, test.err)
			}
			if rep.Decision != test.want {
				t.Fatalf("%+v", rep)
			}
			if e == nil && test.logs && len(out.Events) != 2 {
				t.Fatal("lost branch events")
			}
		})
	}
}
func TestRealSnapshotsBaselineAndRestoredBackup(t *testing.T) {
	l := fresh(t)
	id := item(t, l, "A")
	r := clone(t, l)
	old := clone(t, r)
	peer := filepath.Join(t.TempDir(), "peer.db")
	if e := r.VacuumInto(peer); e != nil {
		t.Fatal(e)
	}
	if _, e := Run(l, peer, Options{}); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Save(store.ItemSpec{ID: id, Title: "B"}, false); e != nil {
		t.Fatal(e)
	}
	if e := r.VacuumInto(peer); e != nil {
		t.Fatal(e)
	}
	if _, e := Run(l, peer, Options{Resolution: "take-remote"}); e != nil {
		t.Fatal(e)
	}
	item(t, old, "edit after rollback")
	if e := old.VacuumInto(peer); e != nil {
		t.Fatal(e)
	}
	if _, e := Run(l, peer, Options{}); !errors.Is(e, ErrConflict) {
		t.Fatalf("restored backup auto-adopted: %v", e)
	}
	it, e := l.GetItem(id)
	if e != nil || it.Title != "B" {
		t.Fatal(it, e)
	}
}
func TestThreeWayRowDeletionsConflictsAndEventCorruption(t *testing.T) {
	l := fresh(t)
	id := item(t, l, "base")
	rating := map[model.Property]model.Rating{model.PropInterest: {Property: model.PropInterest, Value: 1}}
	if _, e := l.Save(store.ItemSpec{ID: id, SetRatings: rating}, false); e != nil {
		t.Fatal(e)
	}
	if e := l.SetConfig("scoring.beta", "0.3"); e != nil {
		t.Fatal(e)
	}
	r := clone(t, l)
	base := content(t, l)
	_, e := l.Save(store.ItemSpec{ID: id, Title: "local", UnsetRatings: []model.Property{model.PropInterest}}, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Save(store.ItemSpec{ID: id, Title: "remote"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.UnsetConfig("scoring.beta"); e != nil {
		t.Fatal(e)
	}
	eid := uuid.NewV7().String()
	event(t, l, eid)
	if _, e = r.AppendEvent(model.Event{ID: eid, Type: model.EventCompleted}); e != nil {
		t.Fatal(e)
	}
	_, rep, e := Resolve(content(t, l), content(t, r), &base, Options{Resolution: "merge"})
	if !errors.Is(e, ErrConflict) || len(rep.Conflicts) != 1 {
		t.Fatal(rep, e)
	}
	out, rep, e := Resolve(content(t, l), content(t, r), &base, Options{Resolution: "merge", Master: "remote"})
	if e != nil {
		t.Fatal(e)
	}
	if out.Items[0]["title"] != "remote" || len(out.Ratings) != 0 || len(out.Config) != 0 || len(out.Events) != 0 || len(rep.CorruptEvents) != 1 {
		t.Fatal(out, rep)
	}
}
func TestCombinedDependencyCycleRefused(t *testing.T) {
	l := fresh(t)
	a := item(t, l, "a")
	b := item(t, l, "b")
	r := clone(t, l)
	base := content(t, l)
	if e := l.AddDependency(a, b); e != nil {
		t.Fatal(e)
	}
	if e := r.AddDependency(b, a); e != nil {
		t.Fatal(e)
	}
	_, _, e := Resolve(content(t, l), content(t, r), &base, Options{Resolution: "merge", Master: "local"})
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestDryRunAndIdentity(t *testing.T) {
	l := fresh(t)
	item(t, l, "local")
	r := clone(t, l)
	item(t, r, "remote")
	p := filepath.Join(t.TempDir(), "peer.db")
	if e := r.VacuumInto(p); e != nil {
		t.Fatal(e)
	}
	before := content(t, l)
	if _, e := Run(l, p, Options{Resolution: "take-remote", DryRun: true}); e != nil {
		t.Fatal(e)
	}
	a, _ := before.Digest(true)
	b, _ := content(t, l).Digest(true)
	if a != b {
		t.Fatal("dry run mutated")
	}
	peers, e := l.Peers()
	if e != nil || len(peers) != 0 {
		t.Fatal(peers, e)
	}
	other := fresh(t)
	if e := other.VacuumInto(p); e != nil {
		t.Fatal(e)
	}
	if _, e := Run(l, p, Options{Resolution: "take-remote"}); !errors.Is(e, ErrIdentity) {
		t.Fatal(e)
	}
}

func TestLoggedActivitiesAreUnionedNotReplayed(t *testing.T) {
	local := fresh(t)
	id, err := local.Save(store.ItemSpec{Title: "Task", Kind: model.KindProject, RemainingDuration: model.FloatPtr(2)}, true)
	if err != nil {
		t.Fatal(err)
	}
	remote := clone(t, local)
	for _, s := range []*store.Store{local, remote} {
		if _, err := s.LogTime(id, model.FloatPtr(.5), nil, false, true); err != nil {
			t.Fatal(err)
		}
	}
	merged, _, err := Resolve(content(t, local), content(t, remote), nil, Options{Resolution: "keep-local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Events) != 2 || merged.Items[0]["remaining_duration"] != 1.5 {
		t.Fatal(merged)
	}
	if err := merged.Validate(); err != nil {
		t.Fatal(err)
	}
	// Seeing the same event again never consumes time again or duplicates history.
	again, _, err := Resolve(merged, content(t, remote), nil, Options{Resolution: "keep-local"})
	if err != nil || len(again.Events) != 2 || again.Items[0]["remaining_duration"] != 1.5 {
		t.Fatal(again, err)
	}
}
