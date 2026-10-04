package store

import (
	"path/filepath"
	"testing"
)

// TestOpenWriteRoundTrip verifies the modernc driver, WAL, migrations, and meta
// all work end to end on a fresh store.
func TestOpenWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "list.db")
	st, err := Open(OpenWrite, Options{DBPath: dbPath, StateDir: dir})
	if err != nil {
		t.Fatalf("open write: %v", err)
	}
	defer st.close()

	u, err := st.DatabaseUUID()
	if err != nil || u == "" {
		t.Fatalf("database_uuid: %v %q", err, u)
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestOpenReadMissing verifies reads on a missing store fail NO_STORE.
func TestOpenReadMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(OpenRead, Options{DBPath: filepath.Join(dir, "nope.db"), StateDir: dir})
	if !IsNoStore(err) {
		t.Fatalf("expected NO_STORE, got %v", err)
	}
}

// TestVacuumInto verifies the snapshot mechanism produces a consistent file.
func TestVacuumInto(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "list.db")
	st, err := Open(OpenWrite, Options{DBPath: dbPath, StateDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.close()

	out := filepath.Join(dir, "snap.db")
	if err := st.VacuumInto(out); err != nil {
		t.Fatalf("vacuum into: %v", err)
	}
	// The output must be openable and validate as an independent store.
	ro, err := Open(OpenRead, Options{DBPath: out, StateDir: dir})
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer ro.close()
	if err := ro.Validate(); err != nil {
		t.Fatalf("snapshot validate: %v", err)
	}
	// Snapshot must carry the same database_uuid.
	au, _ := st.DatabaseUUID()
	bu, _ := ro.DatabaseUUID()
	if au != bu {
		t.Fatalf("uuid mismatch: %s vs %s", au, bu)
	}
}
