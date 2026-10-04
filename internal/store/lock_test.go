package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func shortenLockTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := lockTimeout
	lockTimeout = d
	t.Cleanup(func() { lockTimeout = old })
}

// TestLockAcquireUnlock verifies acquire, idempotent re-acquire, and release.
func TestLockAcquireUnlock(t *testing.T) {
	st := openTestStore(t)
	if err := st.Lock(); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if err := st.Lock(); err != nil {
		t.Fatalf("re-lock must be idempotent: %v", err)
	}
	held, holder, _ := st.LockInfo()
	if !held || holder != os.Getpid() {
		t.Errorf("lock info: held=%v holder=%d", held, holder)
	}
	st.Unlock()
	if held, _, _ := st.LockInfo(); held {
		t.Error("lock still held after unlock")
	}
}

// TestLockBoundedWait verifies that a second process waiting on a live holder
// gives up after the bounded wait with STORE_LOCKED naming the holder.
func TestLockBoundedWait(t *testing.T) {
	dir := t.TempDir()
	opts := Options{DBPath: filepath.Join(dir, "list.db"), StateDir: dir}
	st1, err := Open(OpenWrite, opts)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	t.Cleanup(func() { st1.Close() })
	st2, err := Open(OpenWrite, opts)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	t.Cleanup(func() { st2.Close() })

	if err := st1.Lock(); err != nil {
		t.Fatalf("lock 1: %v", err)
	}
	shortenLockTimeout(t, 400*time.Millisecond)

	start := time.Now()
	err = st2.Lock()
	elapsed := time.Since(start)
	var le *LockedError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LockedError, got %v", err)
	}
	if !errors.Is(err, ErrStoreLocked) {
		t.Errorf("LOCKED error must unwrap to the STORE_LOCKED sentinel")
	}
	if le.Holder != os.Getpid() {
		t.Errorf("holder = %d, want %d", le.Holder, os.Getpid())
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("gave up too early (%v); the bounded wait must actually wait", elapsed)
	}

	// Once the holder releases, the waiter acquires.
	st1.Unlock()
	shortenLockTimeout(t, 2*time.Second)
	if err := st2.Lock(); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	st2.Unlock()
}

// TestLockStaleReclaim verifies a dead holder's lockfile is reclaimed without
// waiting out the full timeout.
func TestLockStaleReclaim(t *testing.T) {
	st := openTestStore(t)

	// Obtain a guaranteed-dead pid: run a short-lived process.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn process: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	dead := cmd.Process.Pid

	lockPath := st.lockPath()
	content := fmt.Sprintf("pid=%d\nhost=test\ntime=%s\n", dead, nowRFC3339())
	if err := os.WriteFile(lockPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	if held, _, _ := st.LockInfo(); held {
		t.Error("stale lockfile reported as held")
	}

	shortenLockTimeout(t, 2*time.Second)
	start := time.Now()
	if err := st.Lock(); err != nil {
		t.Fatalf("stale lock not reclaimed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("reclaim took too long (%v); dead pids should not wait out the timeout", elapsed)
	}
	if held, holder, _ := st.LockInfo(); !held || holder != os.Getpid() {
		t.Errorf("after reclaim: held=%v holder=%d", held, holder)
	}
	st.Unlock()
}

// TestLockInfoFree verifies the free-lock state.
func TestLockInfoFree(t *testing.T) {
	st := openTestStore(t)
	if held, holder, since := st.LockInfo(); held || holder != 0 || since != "" {
		t.Errorf("free lock reported: held=%v holder=%d since=%q", held, holder, since)
	}
}
