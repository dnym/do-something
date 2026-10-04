package store

import "testing"

func TestGeneratedIDCollisionRetry(t *testing.T) {
	s, err := Open(OpenWrite, Options{DBPath: t.TempDir() + "/list.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE identity_fixture(id TEXT PRIMARY KEY, name TEXT UNIQUE)"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO identity_fixture VALUES('collision','existing')"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	generate := func() string {
		calls++
		if calls == 1 {
			return "collision"
		}
		return "fresh"
	}
	id, err := insertGenerated(tx, generate, "INSERT INTO identity_fixture(id,name) VALUES(?,?)", "new")
	if err != nil || id != "fresh" || calls != 2 {
		t.Fatalf("%s %d %v", id, calls, err)
	}
	calls = 0
	if _, err = insertGenerated(tx, func() string { calls++; return "another" }, "INSERT INTO identity_fixture(id,name) VALUES(?,?)", "existing"); err == nil || calls != 1 {
		t.Fatalf("retried unrelated constraint: %d %v", calls, err)
	}
}
