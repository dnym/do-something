package store

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"dosomething/internal/model"
)

func startItem(t *testing.T, st *Store, remaining float64) string {
	t.Helper()
	id, err := st.Save(ItemSpec{Title: "Task", Kind: model.KindProject, TotalDuration: model.FloatPtr(remaining), RemainingDuration: model.FloatPtr(remaining), CostLeft: model.FloatPtr(10)}, true)
	if err != nil {
		t.Fatal(err)
	}
	verb, _ := TransitionOf("start")
	if _, err := st.TransitionStatus(verb, []string{id}); err != nil {
		t.Fatal(err)
	}
	return id
}

func undoReason(err error) string {
	var ue *UndoError
	if errors.As(err, &ue) {
		return ue.Reason
	}
	return ""
}

func TestRevertLogRestoresSessionAndCompletion(t *testing.T) {
	st := openTestStore(t)
	id := startItem(t, st, 1)
	if _, err := st.LogTime(id, model.FloatPtr(0.25), model.FloatPtr(4), false, false); err != nil {
		t.Fatal(err)
	}
	done, err := st.LogTime(id, model.FloatPtr(0.75), nil, true, false)
	if err != nil || !done.Completed {
		t.Fatal(done, err)
	}

	// The latest session completed the item; undo restores both.
	target, err := st.LastUndoableLog(id)
	if err != nil || target != done.EventID {
		t.Fatal(target, err)
	}
	preview, err := st.RevertLog(target, true)
	if err != nil || !preview.Uncompleted || preview.StatusAfter != model.StatusInProgress {
		t.Fatal(preview, err)
	}
	if it, _ := st.GetItem(id); it.Status != model.StatusDone {
		t.Fatal("dry run wrote")
	}
	res, err := st.RevertLog(target, false)
	if err != nil || len(res.RevertEventIDs) != 2 {
		t.Fatal(res, err)
	}
	it, _ := st.GetItem(id)
	if it.Status != model.StatusInProgress || *it.RemainingDuration != 0.75 {
		t.Fatalf("after undo: %s %v", it.Status, *it.RemainingDuration)
	}
	if _, err := st.RevertLog(target, false); undoReason(err) != "already_undone" {
		t.Fatal(err)
	}

	// The next undo reaches the earlier session, restoring time and cost.
	target, _ = st.LastUndoableLog("")
	if _, err := st.RevertLog(target, false); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id)
	if *it.RemainingDuration != 1 || *it.CostLeft != 10 {
		t.Fatalf("after second undo: %v %v", *it.RemainingDuration, *it.CostLeft)
	}
	if _, err := st.LastUndoableLog(""); undoReason(err) != "nothing_to_undo" {
		t.Fatal(err)
	}
	reverted, _ := st.RevertedIDs()
	if len(reverted) != 3 {
		t.Fatalf("reverted %v", reverted)
	}
}

func TestRevertLogRefusesAfterLaterChange(t *testing.T) {
	st := openTestStore(t)
	id := startItem(t, st, 2)
	logged, err := st.LogTime(id, model.FloatPtr(0.5), nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(ItemSpec{ID: id, RemainingDuration: model.FloatPtr(3)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevertLog(logged.EventID, false); undoReason(err) != "changed_since" {
		t.Fatal(err)
	}
	if it, _ := st.GetItem(id); *it.RemainingDuration != 3 {
		t.Fatal("refused undo wrote")
	}
}

func TestRevertedEventsRoundTripCanonicalAndJSON(t *testing.T) {
	st := openTestStore(t)
	id := startItem(t, st, 1)
	logged, _ := st.LogTime(id, model.FloatPtr(0.5), nil, false, false)
	if _, err := st.RevertLog(logged.EventID, false); err != nil {
		t.Fatal(err)
	}
	c, err := st.Content()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Canonical(true)
	decoded, err := ParseCanonical(b)
	if err != nil {
		t.Fatal(err)
	}
	if b2, _ := decoded.Canonical(true); !bytes.Equal(b, b2) || !bytes.Contains(b, []byte("reverts")) {
		t.Fatal("canonical round trip lost reverts")
	}
	// A reverted event needs its target, and only reverted events have one.
	for _, ev := range c.Events {
		if ev["type"] == "reverted" {
			ev["reverts"] = nil
		}
	}
	if err := c.Validate(); err == nil {
		t.Fatal("reverted event without target validated")
	}
}

// v1Store creates a store at schema v1 with one item and a logged session,
// as written by v0.1.x.
func v1Store(t *testing.T, path string) {
	t.Helper()
	saved := migrations
	migrations = migrations[:1]
	defer func() { migrations = saved }()
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := applyMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := (&Store{db: db}).ensureMeta(); err != nil {
		t.Fatal(err)
	}
	id := "01a1dbd3-0000-7000-8000-0000000000aa"
	for _, q := range []string{
		`INSERT INTO items(id,title,kind,status,created_at,updated_at,remaining_duration) VALUES('` + id + `','Old','project','in_progress','2026-11-01T09:00:00Z','2026-11-01T10:00:00Z',2)`,
		`INSERT INTO events(id,item_id,type,at,activity) VALUES('01a1dbd3-0000-7000-8000-000000000001','` + id + `','logged','2026-11-01T10:00:00Z','{"ongoing":false,"duration":1,"remaining_before":3,"remaining_after":2}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestV1StoreMigratesWithBackupAndStableDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.db")
	v1Store(t, path)

	// Frozen v1 snapshots (sync bases, archives) stay readable.
	old, _, err := ReadSnapshot(path)
	if err != nil || old.SchemaVersion != SchemaVersion {
		t.Fatal(old.SchemaVersion, err)
	}
	digest, _ := old.Digest(true)

	// The first open, even a read, backs up and migrates.
	st, err := Open(OpenRead, Options{DBPath: path, StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", fmt.Sprintf("pre-schema-v%d-*.db", SchemaVersion)))
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	if b, _, err := ReadSnapshot(backups[0]); err != nil || len(b.Events) != 1 {
		t.Fatal(err)
	}
	c, err := st.Content()
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Digest(true); d != digest {
		t.Fatal("migration changed the content digest; v1 sync bases would no longer verify")
	}
	if id, err := st.LastUndoableLog(""); err != nil || id == "" {
		t.Fatal(id, err)
	}

	// v1 JSON exports import as current content.
	j := []byte(`{"schema_version":1,"items":[],"ratings":[],"item_tags":[],"item_modes":[],"item_engagements":[],"dependencies":[],"config":[],"events":[{"id":"01a1dbd3-0000-7000-8000-000000000002","item_id":null,"item_ids":"[]","type":"suggested","at":"2026-11-01T10:00:00Z","query":"{}","activity":null}]}`)
	imported, err := DecodeContent(j)
	if err != nil || imported.SchemaVersion != SchemaVersion {
		t.Fatal(err)
	}
	if _, ok := imported.Events[0]["reverts"]; !ok {
		t.Fatal("imported v1 event lacks the reverts column")
	}
}
