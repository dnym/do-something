package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// lockTimeout is the bounded wait before a lock acquisition fails
// STORE_LOCKED (decision 20). It is a variable so tests can shorten it.
var lockTimeout = 5 * time.Second

// lockPoll is the acquisition poll interval.
const lockPoll = 100 * time.Millisecond

// lockPath is the advisory lockfile for this store, in the machine-local state
// dir (never in the synced folder).
func (s *Store) lockPath() string {
	return s.dbPath + ".lock"
}

// Lock acquires the per-store application lock with a bounded wait (~5 s),
// then fails with *LockedError (STORE_LOCKED, naming the holder). Mutating
// commands, sync, and publish serialize on it; read commands take no lock (the
// WAL store handles read concurrency). Lock is idempotent: a Store that already
// holds the lock re-acquires without blocking.
func (s *Store) Lock() error {
	if s.holdsLock {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.dbPath), 0o755); err != nil {
		return fmt.Errorf("store: create state dir: %w", err)
	}
	path := s.lockPath()
	deadline := time.Now().Add(lockTimeout)
	for {
		if s.tryAcquire(path) {
			s.holdsLock = true
			return nil
		}
		if time.Now().After(deadline) {
			holder, since := 0, ""
			if info, err := readLockInfo(path); err == nil {
				holder, since = info.pid, info.since
			}
			if holder > 0 {
				return &LockedError{Holder: holder, Detail: fmt.Sprintf("pid %d%s", holder, sinceDetail(since))}
			}
			return &LockedError{Detail: "lockfile present but unparseable"}
		}
		time.Sleep(lockPoll)
	}
}

// sinceDetail renders " (held since <ts>)" for the locked error detail.
func sinceDetail(since string) string {
	if since == "" {
		return ""
	}
	return " (held since " + since + ")"
}

// tryAcquire attempts one acquisition: create the lockfile exclusively, or
// reclaim it if the recorded holder pid is dead (or the file is corrupt).
// Returns true only on a successful exclusive create.
func (s *Store) tryAcquire(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	if err = lockNative(f); err != nil {
		f.Close()
		return false
	}
	// The persistent inode is never unlinked: two stale-lock reclaimers cannot
	// accidentally lock different files. The OS releases the lock on process death.
	if err = f.Truncate(0); err != nil {
		unlockNative(f)
		f.Close()
		return false
	}
	if _, err = fmt.Fprintf(f, "pid=%d\ntime=%s\n", os.Getpid(), nowRFC3339()); err != nil {
		unlockNative(f)
		f.Close()
		return false
	}
	if err = f.Sync(); err != nil {
		unlockNative(f)
		f.Close()
		return false
	}
	s.lockFile = f
	return true
}
func (s *Store) Unlock() {
	if !s.holdsLock {
		return
	}
	s.holdsLock = false
	if s.lockFile != nil {
		_ = s.lockFile.Truncate(0)
		_ = s.lockFile.Sync()
		unlockNative(s.lockFile)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}

// LockInfo reports the lock's current state without acquiring it (doctor).
// held=false means the lock is free, or a stale lockfile that the next
// acquisition would reclaim.
func (s *Store) LockInfo() (held bool, holder int, since string) {
	info, err := readLockInfo(s.lockPath())
	if err != nil || info.pid <= 0 || !pidAlive(info.pid) {
		return false, 0, ""
	}
	return true, info.pid, info.since
}

type lockInfo struct {
	pid   int
	since string
}

// readLockInfo parses the lockfile's "pid=" / "time=" lines.
func readLockInfo(path string) (lockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockInfo{}, err
	}
	var info lockInfo
	for _, line := range strings.Split(string(data), "\n") {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "pid":
			info.pid, _ = strconv.Atoi(kv[1])
		case "time":
			info.since = kv[1]
		}
	}
	return info, nil
}
