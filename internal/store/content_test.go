package store

import (
	"bytes"
	"dosomething/internal/model"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestCanonicalRoundTripsAndNull(t *testing.T) {
	s := openTestStore(t)
	id := addTestItem(t, s, "Unicode å\n\t|:", model.KindProject)
	empty := ""
	if _, e := s.Save(ItemSpec{ID: id, Notes: &empty, CostLeft: model.FloatPtr(0)}, false); e != nil {
		t.Fatal(e)
	}
	c, e := s.Content()
	if e != nil {
		t.Fatal(e)
	}
	b, e := c.Canonical(true)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := ParseCanonical(b)
	if e != nil {
		t.Fatal(e)
	}
	b2, e := decoded.Canonical(true)
	if e != nil || !bytes.Equal(b, b2) {
		t.Fatal(e)
	}
	j, _ := json.Marshal(c)
	decoded, e = DecodeContent(j)
	if e != nil {
		t.Fatal(e)
	}
	b2, e = decoded.Canonical(true)
	if e != nil || !bytes.Equal(b, b2) {
		t.Fatal("JSON changed SQL types", e)
	}
	c.Items[0]["notes"] = nil
	b2, _ = c.Canonical(true)
	if bytes.Equal(b, b2) {
		t.Fatal("NULL conflated with empty string")
	}
}

// TestContentModesAndEngagementsRoundTrip: the item_modes and
// item_engagements tables are part of the canonical interchange and the JSON
// content, and Validate enforces the built-in token sets plus item references
// on them.
func TestContentModesAndEngagementsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id := addTestItem(t, s, "modal", model.KindProject)
	two := []string{"hands_on", "thinking"}
	both := []string{"focused", "loose"}
	if _, e := s.Save(ItemSpec{ID: id, Modes: &two, Engagements: &both}, false); e != nil {
		t.Fatal(e)
	}
	c, e := s.Content()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Modes) != 2 {
		t.Fatalf("content modes = %v, want 2 rows", c.Modes)
	}
	if len(c.Engagements) != 2 {
		t.Fatalf("content engagements = %v, want 2 rows", c.Engagements)
	}
	baseline, e := c.Canonical(true)
	if e != nil {
		t.Fatal(e)
	}
	for _, pair := range []struct {
		name string
		enc  func() ([]byte, error)
		dec  func([]byte) (Content, error)
	}{
		{
			name: "canonical",
			enc:  func() ([]byte, error) { return c.Canonical(true) },
			dec:  ParseCanonical,
		},
		{
			name: "json",
			enc:  func() ([]byte, error) { return json.Marshal(c) },
			dec:  DecodeContent,
		},
	} {
		b, e := pair.enc()
		if e != nil {
			t.Fatal(e)
		}
		decoded, e := pair.dec(b)
		if e != nil {
			t.Fatal(e)
		}
		if len(decoded.Modes) != 2 {
			t.Fatalf("%s: decoded modes = %v", pair.name, decoded.Modes)
		}
		if len(decoded.Engagements) != 2 {
			t.Fatalf("%s: decoded engagements = %v", pair.name, decoded.Engagements)
		}
		b2, e := decoded.Canonical(true)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(b2, baseline) {
			t.Fatalf("%s round trip changed the canonical form", pair.name)
		}
	}
	// Apply a changed mode and engagement set through the content path.
	other := EmptyContent()
	other.Items = c.Items
	other.Modes = []Row{{"item_id": id, "mode": "movement"}}
	other.Engagements = []Row{{"item_id": id, "engagement": "loose"}}
	if e := other.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := s.ApplyContent(other); e != nil {
		t.Fatal(e)
	}
	it, e := s.GetItem(id)
	if e != nil {
		t.Fatal(e)
	}
	if len(it.Modes) != 1 || it.Modes[0] != "movement" {
		t.Fatalf("modes after apply = %v", it.Modes)
	}
	if len(it.Engagements) != 1 || it.Engagements[0] != "loose" {
		t.Fatalf("engagements after apply = %v", it.Engagements)
	}

	// An invalid token is rejected.
	bad := EmptyContent()
	bad.Items = c.Items
	bad.Modes = []Row{{"item_id": id, "mode": "zen"}}
	if e := bad.Validate(); e == nil {
		t.Fatal("invalid mode token accepted in content")
	}
	bad.Engagements = []Row{{"item_id": id, "engagement": "zen"}}
	if e := bad.Validate(); e == nil {
		t.Fatal("invalid engagement token accepted in content")
	}
	// A row referencing a missing item is rejected.
	orphan := EmptyContent()
	orphan.Modes = []Row{{"item_id": "00000000-0000-0000-0000-000000000000", "mode": "making"}}
	if e := orphan.Validate(); e == nil {
		t.Fatal("orphan mode row accepted in content")
	}
	orphan.Engagements = []Row{{"item_id": "00000000-0000-0000-0000-000000000000", "engagement": "focused"}}
	if e := orphan.Validate(); e == nil {
		t.Fatal("orphan engagement row accepted in content")
	}
}
func TestIdempotentEditAndReadPathEscaping(t *testing.T) {
	s, e := Open(OpenWrite, Options{DBPath: filepath.Join(t.TempDir(), "list?#.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	id := addTestItem(t, s, "same", model.KindProject)
	before, _ := s.Content()
	if _, e = s.Save(ItemSpec{ID: id, Title: "same"}, false); e != nil {
		t.Fatal(e)
	}
	after, _ := s.Content()
	a, _ := before.Digest(true)
	b, _ := after.Digest(true)
	if a != b {
		t.Fatal("idempotent edit changed timestamp")
	}
	read, e := Open(OpenRead, Options{DBPath: s.Path()})
	if e != nil {
		t.Fatal(e)
	}
	read.Close()
}
func FuzzCanonical(f *testing.F) {
	b, _ := EmptyContent().Canonical(true)
	f.Add(b)
	f.Add([]byte("invalid"))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, e := ParseCanonical(b)
		if e == nil {
			out, e := c.Canonical(true)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ParseCanonical(out); e != nil {
				t.Fatal(e)
			}
		}
	})
}

func FuzzJSONContent(f *testing.F) {
	b, _ := json.Marshal(EmptyContent())
	f.Add(b)
	f.Add([]byte(`[{"title":"unsupported flat row"}]`))
	f.Add([]byte("title\tkind\nExample\tproject\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := DecodeContent(b)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := DecodeContent(encoded)
		if err != nil {
			t.Fatal(err)
		}
		before, err := c.Digest(true)
		if err != nil {
			t.Fatal(err)
		}
		after, err := roundTrip.Digest(true)
		if err != nil || before != after {
			t.Fatalf("JSON round trip changed content: %s %s (%v)", before, after, err)
		}
	})
}

func TestMigrationAtomicityAndSchemaGate(t *testing.T) {
	dir := t.TempDir()
	db, e := openDB(filepath.Join(dir, "migration.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	saved := migrations
	migrations = []migration{{version: 1, sql: "CREATE TABLE should_rollback(id TEXT); INVALID SQL"}}
	e = applyMigrations(db)
	migrations = saved
	if e == nil {
		t.Fatal("invalid migration succeeded")
	}
	if tableExists(db, "should_rollback") {
		t.Fatal("migration partially applied")
	}
	if e = applyMigrations(db); e != nil {
		t.Fatal(e)
	}
	s := &Store{db: db}
	if e = s.ensureMeta(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE meta SET value='999' WHERE key='schema_version'"); e != nil {
		t.Fatal(e)
	}
	if e = ValidateDB(db); !errors.Is(e, ErrSchemaMismatch) {
		t.Fatal(e)
	}
	if e = applyMigrations(db); e != nil {
		t.Fatal(e)
	}
	var version string
	if e = db.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&version); e != nil || version != "999" {
		t.Fatal("downgraded schema", version, e)
	}
}

// TestActivityTablesBaseSchema: item_modes and item_engagements are part of
// the base schema — the app has never shipped a store, so there is no
// migration to them. A fresh store is recorded at the base version, and the
// CHECKs pin the built-in tokens at the database level, below the
// application-level validation.
func TestActivityTablesBaseSchema(t *testing.T) {
	s := openTestStore(t)
	var version string
	if e := s.db.QueryRow("SELECT value FROM meta WHERE key='schema_version'").Scan(&version); e != nil || version != "1" {
		t.Fatalf("fresh store schema_version = %q (%v)", version, e)
	}
	id := addTestItem(t, s, "check", model.KindProject)
	for _, table := range []struct {
		name   string
		insert string
		tokens []string
	}{
		{"item_modes", "INSERT INTO item_modes(item_id,mode) VALUES(?,?)", []string{"movement", "hands_on", "thinking", "making"}},
		{"item_engagements", "INSERT INTO item_engagements(item_id,engagement) VALUES(?,?)", []string{"focused", "loose"}},
	} {
		for _, tok := range table.tokens {
			if _, e := s.db.Exec(table.insert, id, tok); e != nil {
				t.Fatalf("%s: built-in token %q rejected by the database: %v", table.name, tok, e)
			}
		}
		if _, e := s.db.Exec(table.insert, id, "zen"); e == nil {
			t.Fatalf("%s: unknown token accepted at the database level", table.name)
		}
	}
}

func TestReplacementPreservesReaderSnapshot(t *testing.T) {
	a := openTestStore(t)
	id := addTestItem(t, a, "before", model.KindProject)
	b := openTestStore(t)
	addTestItem(t, b, "after", model.KindProject)
	source := filepath.Join(t.TempDir(), "remote.db")
	if e := b.VacuumInto(source); e != nil {
		t.Fatal(e)
	}
	tx, e := a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	var title string
	if e = tx.QueryRow("SELECT title FROM items WHERE id=?", id).Scan(&title); e != nil {
		t.Fatal(e)
	}
	if e = a.ReplaceStore(source); e != nil {
		t.Fatal(e)
	}
	if e = tx.QueryRow("SELECT title FROM items WHERE id=?", id).Scan(&title); e != nil || title != "before" {
		t.Fatal("reader snapshot disrupted", title, e)
	}
	items, e := a.AllItems()
	if e != nil || len(items) != 1 || items[0].Title != "after" {
		t.Fatal(items, e)
	}
}

func TestSnapshotMissingColumnIsRejected(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE items DROP COLUMN notes"); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatalf("missing column accepted: %v", err)
	}
}
