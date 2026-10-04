package store

import (
	"database/sql"
	"fmt"
)

// SchemaVersion is the current schema version. Snapshots carry it in meta and are
// gated on it: a snapshot with a version newer than this binary understands is
// rejected with SCHEMA_MISMATCH (forward-only migrations; see the README policy).
// The app has never shipped a store, so the schema is still edited in place and
// the first real change will land as v2.
const SchemaVersion = 1

// AppID identifies the application that wrote the store; it is stored in meta and
// travels with the file.
const AppID = "do-something"

// metaKeys are the recognized meta keys.
const (
	MetaDatabaseUUID = "database_uuid"
	MetaSchemaVer    = "schema_version"
	MetaAppID        = "app_id"
	MetaDeviceName   = "device_name"
)

// migration is a single forward-only step.
type migration struct {
	version int
	sql     string
}

// migrations are applied in version order on first write-open. All content tables
// from the plan §4 are created here so that a fresh store is fully formed before
// any scoring, sync, or filter code runs. The app has never shipped a store, so
// there are no migration steps yet: the base schema below is edited in place
// (as it was when the `idea` kind was removed), and item_modes /
// item_engagements are part of it — both built-in sets are interface
// vocabulary pinned by CHECKs (like kind/status), not user data. The
// machinery stays for the first real schema change.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE items(
  id TEXT PRIMARY KEY,
  legacy_id INTEGER UNIQUE,
  title TEXT NOT NULL,
  ongoing INTEGER NOT NULL DEFAULT 0 CHECK(ongoing IN (0,1)),
  kind TEXT NOT NULL CHECK(kind IN ('project','media')),
  status TEXT NOT NULL DEFAULT 'not_started'
       CHECK(status IN ('not_started','in_progress','done','dropped')),
  category TEXT,
  type TEXT,
  notes TEXT,
  url TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deadline TEXT,
  remaining_duration REAL CHECK(remaining_duration IS NULL OR remaining_duration >= 0),
  cost_left REAL CHECK(cost_left IS NULL OR cost_left >= 0),
  min_session_duration REAL CHECK(min_session_duration IS NULL OR min_session_duration >= 0),
  total_duration REAL CHECK(total_duration IS NULL OR total_duration >= 0),
  CHECK(ongoing = 0 OR (total_duration IS NULL AND remaining_duration IS NULL))
);
CREATE INDEX idx_items_status ON items(status);
CREATE INDEX idx_items_kind ON items(kind);
CREATE INDEX idx_items_type ON items(type);

CREATE TABLE item_tags(
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tag TEXT NOT NULL CHECK(length(tag) > 0),
  PRIMARY KEY(item_id, tag)
);

CREATE TABLE item_modes(
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK(mode IN ('movement','hands_on','thinking','making')),
  PRIMARY KEY(item_id, mode)
);

CREATE TABLE item_engagements(
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  engagement TEXT NOT NULL CHECK(engagement IN ('focused','loose')),
  PRIMARY KEY(item_id, engagement)
);

CREATE TABLE dependencies(
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  depends_on TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  CHECK(item_id != depends_on),
  PRIMARY KEY(item_id, depends_on)
);

CREATE TABLE ratings(
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  property TEXT NOT NULL,
  value REAL NOT NULL CHECK(value >= 0 AND value <= 1),
  rated_at TEXT,
  PRIMARY KEY(item_id, property)
);

CREATE TABLE events(
  id TEXT PRIMARY KEY,
  item_id TEXT,
  item_ids TEXT,
  type TEXT NOT NULL CHECK(type IN ('suggested','started','completed','dropped','logged')),
  at TEXT NOT NULL,
  query TEXT,
  activity TEXT
);
CREATE INDEX idx_events_type ON events(type);

CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE config(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT);
`,
	},
}

// applyMigrations runs any migrations newer than the recorded schema_version.
// The store must already be open (and created if missing).
func applyMigrations(db *sql.DB) error {
	cur, err := currentSchemaVersion(db)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version > cur {
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(m.sql); err == nil {
				_, err = tx.Exec("INSERT INTO meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", fmt.Sprint(m.version))
			}
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("store: migration v%d: %w", m.version, err)
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}

// currentSchemaVersion reads the recorded version, or 0 for a brand-new store
// (no meta row yet).
func currentSchemaVersion(db *sql.DB) (int, error) {
	// A brand-new store has no meta table yet; its version is 0.
	var n string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&n)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	v := ""
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaSchemaVer).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	num, perr := parseInt(v)
	if perr != nil {
		return 0, fmt.Errorf("store: bad schema_version %q: %w", v, perr)
	}
	return num, nil
}
