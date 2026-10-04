package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVacuumIntoCleansLeftoverTemps simulates a kill mid-publish: a previous
// run died between writing the temp file and installing it, leaving a torn
// temp file beside the still-valid published snapshot. The next publish must
// reclaim the leftover, keep the previously published file valid until the
// new one is installed, and end with a validating snapshot.
func TestVacuumIntoCleansLeftoverTemps(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "list.db")
	st, err := Open(OpenWrite, Options{DBPath: dbPath, StateDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	out := filepath.Join(dir, "snap.db")
	if err := st.VacuumInto(out); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := ValidatePath(out); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// Simulate the crash: a torn temp file with the uniqueTempName shape.
	leftover := filepath.Join(dir, "snap.db.tmp-0-deadbeef")
	if err := os.WriteFile(leftover, []byte("torn partial output"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	// Next publish: the new snapshot must install cleanly.
	if err := st.VacuumInto(out); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if err := ValidatePath(out); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	// The torn leftover must be reclaimed so it can never be mistaken for
	// current output.
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover temp not reclaimed: %v", err)
	}
}

// TestPublishedCorruptionDetectedAndSelfHealed simulates a torn replacement
// on the installed (published) file: the consumer-side validation must reject
// it, and the next publish self-heals the file.
func TestPublishedCorruptionDetectedAndSelfHealed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "list.db")
	st, err := Open(OpenWrite, Options{DBPath: dbPath, StateDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	out := filepath.Join(dir, "snap.db")
	if err := st.VacuumInto(out); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Corrupt the installed file in place (torn replacement backstop).
	if err := os.WriteFile(out, []byte("not a database"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if err := ValidatePath(out); err == nil {
		t.Fatal("corrupt published file passed validation")
	}

	// The working store is untouched by the published-file corruption.
	if err := st.Validate(); err != nil {
		t.Errorf("working store damaged: %v", err)
	}
	// The next publish self-heals the file.
	if err := st.VacuumInto(out); err != nil {
		t.Fatalf("re-publish: %v", err)
	}
	if err := ValidatePath(out); err != nil {
		t.Errorf("snapshot not self-healed: %v", err)
	}
}
