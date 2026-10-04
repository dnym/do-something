package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReplaceStore installs the snapshot's content and identity in one WAL
// transaction. It never unlinks live WAL/SHM files under concurrent readers.
// Normal sync checks identity first; this low-level API also supports cloning.
// The caller must hold the per-store application lock.
func (s *Store) ReplaceStore(srcFile string) error {
	dir, e := os.MkdirTemp("", "do-something-replace-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	captured := filepath.Join(dir, "source.db")
	if e = CaptureSnapshot(srcFile, captured); e != nil {
		return e
	}
	content, id, e := ReadSnapshot(captured)
	if e != nil {
		return e
	}
	return s.applyContent(content, id)
}

// BootstrapFrom installs a validated remote snapshot as the working store at
// dbPath (creating the file) without ever minting a blank store first: a
// subsequent Open(OpenWrite) on the now-existing file adopts the file's
// database_uuid instead of forking the store's identity (fresh-machine story,
// plan §3.1). stateDir is the machine-local state directory.
func BootstrapFrom(stateDir, dbPath, srcFile string) error {
	if dbPath == "" {
		return fmt.Errorf("store: empty DBPath")
	}
	if _, err := os.Stat(dbPath); err == nil {
		return fmt.Errorf("store: bootstrap requires a missing working store")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("store: create db dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("store: create state dir: %w", err)
	}
	if err := ValidatePath(srcFile); err != nil {
		return &StoreOpenError{Err: ErrStoreCorrupt, Path: srcFile, Detail: "bootstrap source failed validation: " + err.Error()}
	}
	tmp := uniqueTempName(filepath.Dir(dbPath), filepath.Base(dbPath))
	if err := copyFile(srcFile, tmp); err != nil {
		return err
	}
	if _, _, err := ReadSnapshot(tmp); err != nil {
		os.Remove(tmp)
		return &StoreOpenError{Err: ErrStoreCorrupt, Path: srcFile, Detail: "bootstrap copy failed validation: " + err.Error()}
	}
	if err := replaceFile(tmp, dbPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: bootstrap working store: %w", err)
	}
	return fsyncDir(filepath.Dir(dbPath))
}
