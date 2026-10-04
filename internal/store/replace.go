package store

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
)

// tempCounter makes temp filenames unique within a process.
var tempCounter atomic.Int64

// replaceFile installs src over dst using the platform-safe replacement
// primitive (see replace_unix.go / replace_windows.go). src must already have
// been written and fsynced by the caller and must live in the same directory as
// dst. On success src is consumed (moved/renamed over dst); on error the caller
// is responsible for cleaning up src.
//
// A torn replacement can only ever leave dst in a state that the *consumer*
// detects: after a replacement the new file is validated before it is trusted,
// and a corrupt published file is rejected and self-heals on the next publish.
func replaceFile(src, dst string) error {
	return platformSwap(src, dst)
}

// fsyncFile flushes a file's contents and metadata to durable storage.
func fsyncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// fsyncDir flushes a directory entry (so a rename/creation is durable).
func fsyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// uniqueTempName returns a fresh, unique filename in dir with the given prefix.
// The combination of a monotonic process counter and random bytes makes a name
// collision with a leftover from a crashed prior run essentially impossible.
func uniqueTempName(dir, prefix string) string {
	n := tempCounter.Add(1)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return filepath.Join(dir, fmt.Sprintf("%s.tmp-%d-%08x", prefix, n, b))
}
