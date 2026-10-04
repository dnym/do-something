package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// snapshotHook is a deterministic subprocess crash-test seam.
var snapshotHook func(string)

func snapshotPoint(phase string) {
	if snapshotHook != nil {
		snapshotHook(phase)
	}
}

// openReadOnly opens a standalone (non-WAL) database file read-only. It is used
// to validate inbound files and VACUUM INTO output without ever forcing WAL onto
// them (which would create -wal/-shm side files beside a clean snapshot).
func openReadOnly(path string) (*sql.DB, error) {
	dsn := sqliteFileURI(path) + "?mode=ro&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open read-only %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, &StoreOpenError{Err: ErrStoreCorrupt, Path: path, Detail: err.Error()}
	}
	return db, nil
}

// ValidatePath validates a standalone database file on disk (quick_check,
// foreign_key_check, schema version, expected tables) without forcing WAL.
func ValidatePath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	return ValidateDB(db)
}

// vacuumIntoPath runs VACUUM INTO against the store's working database, writing a
// consistent single-file, self-contained snapshot to path. path must not already
// exist (SQLite requirement). The output is a rollback-journal database, not WAL.
func (s *Store) vacuumIntoPath(path string) error {
	// VACUUM cannot run inside a transaction and must not be shared with other
	// statements on the connection; the pool handle is quiesced under the store
	// lock (see the publish/sync pipelines) so this is safe here.
	literal := strings.NewReplacer("'", "''").Replace(path)
	if _, err := s.db.Exec("VACUUM INTO '" + literal + "'"); err != nil {
		return fmt.Errorf("store: VACUUM INTO %s: %w", path, err)
	}
	return nil
}

// VacuumInto produces a consistent single-file snapshot installed at target via
// the platform-safe replacement primitive. The intermediate file is written to a
// fresh unique temp name in the same directory, validated, then swapped into
// place, so an interrupted VACUUM INTO can never leave a torn file at target.
func (s *Store) VacuumInto(target string) error {
	if s.dbPath != "" {
		source, err := os.Stat(s.dbPath)
		if err != nil {
			return err
		}
		if dest, err := os.Stat(target); err == nil && os.SameFile(source, dest) {
			return fmt.Errorf("snapshot target is the working store")
		}
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("store: create snapshot dir: %w", err)
	}
	if err := removeLeftoverTemps(dir, filepath.Base(target)); err != nil {
		return err
	}
	tmp := uniqueTempName(dir, filepath.Base(target))
	if err := s.vacuumIntoPath(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	snapshotPoint("vacuum_complete")
	if err := fsyncFile(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	// Validate the output as an independent store before it can be trusted.
	if err := ValidatePath(tmp); err != nil {
		os.Remove(tmp)
		return &StoreOpenError{Err: ErrStoreCorrupt, Path: tmp, Detail: "snapshot failed validation: " + err.Error()}
	}
	snapshotPoint("before_replace")
	if err := replaceFile(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: install snapshot %s: %w", target, err)
	}
	snapshotPoint("after_replace")
	return fsyncDir(dir)
}

// removeLeftoverTemps best-effort removes snapshot temp files left behind by a
// previously crashed run, so a fresh unique target name is guaranteed free.
func removeLeftoverTemps(dir, base string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prefix := base + ".tmp-"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Publish writes the store's snapshot to the per-device publish location
// <syncDir>/snapshots/<deviceID>.db. deviceName is recorded in the snapshot's
// meta (display-only). This is the "push": publishing your own file; the peer
// pulls by syncing it.
func (s *Store) Publish(syncDir, deviceID, deviceName string) (string, error) {
	if syncDir == "" || deviceID == "" {
		return "", fmt.Errorf("store: publish requires a sync dir and device id")
	}
	if deviceName != "" {
		if err := s.SetDeviceName(deviceName); err != nil {
			return "", err
		}
	}
	target := filepath.Join(syncDir, "snapshots", deviceID+".db")
	if err := s.VacuumInto(target); err != nil {
		return "", err
	}
	return target, nil
}
