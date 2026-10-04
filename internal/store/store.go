// Package store is the single gateway to the working SQLite database. All
// item/rating/tag/dependency/event/config reads and writes go through it; the
// rest of the program never opens a side connection to "fix" something.
//
// It also owns the machine-local concerns that travel with the store but are
// never synced: the per-store application lock, the platform-safe file
// replacement primitive, VACUUM INTO snapshotting and publishing, the store
// bootstrap semantics, the device identity, per-peer sync baselines, and the
// history archive with retention GC.
package store

import (
	"database/sql"
	"dosomething/internal/paths"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"uuid"

	_ "modernc.org/sqlite"
)

// Sentinel errors the CLI/output layer maps to stable machine-readable codes.
var (
	ErrNoStore        = errors.New("NO_STORE")
	ErrStoreCorrupt   = errors.New("STORE_CORRUPT")
	ErrSchemaMismatch = errors.New("SCHEMA_MISMATCH")
	ErrCycle          = errors.New("CYCLE")
	ErrInvalidID      = errors.New("INVALID_ID")
	ErrStoreLocked    = errors.New("STORE_LOCKED")
	ErrNoPeerBase     = errors.New("NO_PEER_BASE")
)

// LockedError reports a bounded-wait lock acquisition failure, naming the holder.
type LockedError struct {
	Holder int // pid of the current holder (0 if unknown)
	Detail string
}

func (e *LockedError) Error() string {
	if e.Holder > 0 {
		return fmt.Sprintf("STORE_LOCKED: store is locked by pid %d (%s)", e.Holder, e.Detail)
	}
	return "STORE_LOCKED: store is locked by another process"
}

func (e *LockedError) Unwrap() error { return ErrStoreLocked }

// OpenMode selects store lifecycle semantics.
type OpenMode int

const (
	// OpenRead requires the store to already exist; a missing store yields
	// ErrNoStore. Reads take no lock.
	OpenRead OpenMode = iota
	// OpenWrite creates the store lazily if missing (running migrations and
	// minting a database_uuid).
	OpenWrite
)

// Options configures a Store. DBPath is the working store file; StateDir is the
// machine-local state directory (device-id, sync-state, history). When StateDir
// is empty it defaults to the directory containing DBPath.
type Options struct {
	DBPath   string
	StateDir string
	// Locked means the caller already holds Acquire for the full operation.
	Locked bool
}

// Store wraps the working database and its machine-local state.
type Store struct {
	db       *sql.DB
	dbPath   string
	stateDir string

	mu        sync.Mutex // guards close/reopen of the underlying handle
	lockFile  *os.File
	holdsLock bool // this process holds the per-store application lock
}

// Open opens (and for OpenWrite, lazily creates) the store at opts.DBPath.
func Open(mode OpenMode, opts Options) (*Store, error) {
	if opts.DBPath == "" {
		return nil, errors.New("store: empty DBPath")
	}
	canonical, e := paths.Canonical(opts.DBPath)
	if e != nil {
		return nil, e
	}
	opts.DBPath = canonical
	if mode == OpenWrite && !opts.Locked {
		unlock, err := Acquire(opts)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = filepath.Dir(opts.DBPath)
	}
	if mode == OpenRead {
		if _, err := os.Stat(opts.DBPath); err != nil {
			if os.IsNotExist(err) {
				return nil, &StoreOpenError{Err: ErrNoStore, Path: opts.DBPath}
			}
			return nil, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o755); err != nil {
			return nil, fmt.Errorf("store: create db dir: %w", err)
		}
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			return nil, fmt.Errorf("store: create state dir: %w", err)
		}
	}

	var db *sql.DB
	var err error
	if mode == OpenRead {
		db, err = openReadOnly(opts.DBPath)
	} else {
		db, err = openDB(opts.DBPath)
	}
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, dbPath: opts.DBPath, stateDir: stateDir}

	if mode == OpenWrite {
		// Validate file-level integrity before doing anything else: a corrupt or
		// foreign store must fail fast rather than be "repaired" by a migration.
		// (Schema completeness is checked only after migrations create the tables.)
		if err := checkFileIntegrity(db); err != nil {
			s.close()
			return nil, err
		}
		if err := checkSchemaGate(db); err != nil {
			s.close()
			return nil, err
		}
		if !tableExists(db, "meta") {
			var count int
			if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); err != nil || count > 0 {
				s.close()
				return nil, ErrStoreCorrupt
			}
		}
		if err := applyMigrations(db); err != nil {
			s.close()
			return nil, err
		}
		if err := s.ensureMeta(); err != nil {
			s.close()
			return nil, err
		}
	}
	if err := s.Validate(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// StoreOpenError wraps a store-open failure with the path and an optional detail,
// so callers can render a helpful NO_STORE / STORE_CORRUPT message.
type StoreOpenError struct {
	Err    error
	Path   string
	Detail string
}

func (e *StoreOpenError) Error() string {
	base := e.Err.Error()
	if e.Detail != "" {
		base = e.Detail
	}
	if e.Path != "" {
		return base + " (" + e.Path + ")"
	}
	return base
}
func (e *StoreOpenError) Unwrap() error { return e.Err }

// IsNoStore reports whether err is a missing-store failure.
func IsNoStore(err error) bool {
	var oe *StoreOpenError
	if errors.As(err, &oe) {
		return errors.Is(oe.Err, ErrNoStore)
	}
	return errors.Is(err, ErrNoStore)
}

// openDB opens the SQLite file with WAL and foreign keys enabled on every
// connection via the DSN, so pool connections are always consistent.
func openDB(path string) (*sql.DB, error) {
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// A single writer connection avoids WAL write-queue surprises; reads still
	// proceed lock-free on the read snapshot via additional pool connections.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}
	return db, nil
}

// close releases the underlying handle.
func (s *Store) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

// Close releases the per-store application lock (if held) and the database
// handle. A process that is killed with the lock still held leaves a stale
// lockfile, which the next acquisition reclaims (decision 20).
func (s *Store) Close() {
	s.close()
	s.Unlock()
}

// Path is the working store file path.
func (s *Store) Path() string { return s.dbPath }

// StateDir is the machine-local state directory.
func (s *Store) StateDir() string { return s.stateDir }

// Validate runs integrity checks (quick_check, foreign_key_check, schema version,
// expected tables) and returns a descriptive error if the store is not usable.
func (s *Store) Validate() error {
	return ValidateDB(s.db)
}

// ValidateDB validates an already-open database handle: file integrity, the
// forward-only schema gate, and the presence of every expected table.
//
// The recorded version must equal SchemaVersion before any table is inspected:
// an older live store is migrated by the next write-open (which runs
// migrations first), so read paths on it — and every frozen snapshot, which
// is never migrated — report SCHEMA_MISMATCH instead of STORE_CORRUPT on the
// tables the new schema added.
func ValidateDB(db *sql.DB) error {
	if err := checkFileIntegrity(db); err != nil {
		return err
	}
	if err := checkSchemaGate(db); err != nil {
		return err
	}
	v, err := currentSchemaVersion(db)
	if err != nil {
		return &StoreOpenError{Err: ErrSchemaMismatch, Detail: err.Error()}
	}
	if v != SchemaVersion {
		return &StoreOpenError{Err: ErrSchemaMismatch, Detail: fmt.Sprintf("store schema v%d is older than required v%d", v, SchemaVersion)}
	}
	for _, t := range []string{"items", "ratings", "item_tags", "item_modes", "item_engagements", "dependencies", "events", "meta", "config"} {
		if !tableExists(db, t) {
			return &StoreOpenError{Err: ErrStoreCorrupt, Detail: "missing table " + t}
		}
	}
	for _, t := range EmptyContent().Tables() {
		rows, err := db.Query(`SELECT source."` + strings.Join(t.Columns, `",source."`) + `" FROM "` + t.Name + `" AS source LIMIT 0`)
		if err != nil {
			return &StoreOpenError{Err: ErrStoreCorrupt, Detail: err.Error()}
		}
		rows.Close()
	}
	var id, app string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='database_uuid'").Scan(&id); err != nil {
		return ErrStoreCorrupt
	}
	if _, err := uuid.Parse(id); err != nil {
		return ErrStoreCorrupt
	}
	if err := db.QueryRow("SELECT value FROM meta WHERE key='app_id'").Scan(&app); err != nil || app != AppID {
		return ErrStoreCorrupt
	}
	return nil
}

// checkFileIntegrity runs the file-level integrity pragmas that are valid even on
// an empty (pre-migration) database.
func checkFileIntegrity(db *sql.DB) error {
	if err := checkQuickCheck(db); err != nil {
		return &StoreOpenError{Err: ErrStoreCorrupt, Detail: err.Error()}
	}
	if err := checkForeignKey(db); err != nil {
		return &StoreOpenError{Err: ErrStoreCorrupt, Detail: err.Error()}
	}
	return nil
}

// checkSchemaGate rejects a store whose recorded schema version is newer than
// this binary understands (forward-only migrations). A fresh store (version 0)
// passes.
func checkSchemaGate(db *sql.DB) error {
	v, err := currentSchemaVersion(db)
	if err != nil {
		return &StoreOpenError{Err: ErrSchemaMismatch, Detail: err.Error()}
	}
	if v > SchemaVersion {
		return &StoreOpenError{Err: ErrSchemaMismatch, Detail: fmt.Sprintf("store schema v%d is newer than supported v%d", v, SchemaVersion)}
	}
	return nil
}

func checkQuickCheck(db *sql.DB) error {
	var line string
	rows, err := db.Query(`PRAGMA quick_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			return fmt.Errorf("quick_check: %s", line)
		}
	}
	return rows.Err()
}

func checkForeignKey(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var a, b, c, d sql.NullString
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			return err
		}
		count++
	}
	if count > 0 {
		return fmt.Errorf("foreign_key_check: %d violations", count)
	}
	return rows.Err()
}

func tableExists(db *sql.DB, name string) bool {
	var n string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return err == nil
}

// ensureMeta fills in any missing meta rows: database_uuid (minted once),
// app_id, and schema_version. database_uuid is immutable after the first mint.
func (s *Store) ensureMeta() error {
	if !s.hasDatabaseUUID() {
		u := newDatabaseUUID()
		if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, MetaDatabaseUUID, u); err != nil {
			return fmt.Errorf("store: set database_uuid: %w", err)
		}
	}
	if !s.metaExists(MetaAppID) {
		if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, MetaAppID, AppID); err != nil {
			return err
		}
	}
	if !s.metaExists(MetaSchemaVer) {
		if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, MetaSchemaVer, fmt.Sprint(SchemaVersion)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) hasDatabaseUUID() bool {
	return s.metaExists(MetaDatabaseUUID)
}

func (s *Store) metaExists(key string) bool {
	var n string
	return s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&n) == nil
}

// DatabaseUUID returns the immutable store identity.
func (s *Store) DatabaseUUID() (string, error) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, MetaDatabaseUUID).Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}

// SetDeviceName records the publishing device's display label in the working
// store's meta so published snapshots carry it. It is display-only and never used
// for identity or filenames.
func (s *Store) SetDeviceName(name string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, MetaDeviceName, name)
	return err
}

// DeviceNameMeta returns the stored device_name display label (may be empty).
func (s *Store) DeviceNameMeta() string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, MetaDeviceName).Scan(&v); err != nil {
		return ""
	}
	return v
}

// NewNow returns a timestamp used for created_at/updated_at/rated_at bookkeeping.
// Callers pass in their own clock in tests for determinism; this default uses the
// wall clock.
func NewNow() string { return nowRFC3339() }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }

// Diagnostics reports each check separately without opening another connection.
func (s *Store) Diagnostics() map[string]string {
	checks := map[string]string{"openability": "ok"}
	for name, check := range map[string]func() error{
		"quick_check":       func() error { return checkQuickCheck(s.db) },
		"foreign_key_check": func() error { return checkForeignKey(s.db) },
		"schema":            func() error { return s.Validate() },
	} {
		checks[name] = "ok"
		if err := check(); err != nil {
			checks[name] = err.Error()
		}
	}
	return checks
}
