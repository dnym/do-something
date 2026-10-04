//go:build !windows

package store

import (
	"os"
	"syscall"
)

// pidAlive reports whether a process with the given pid exists on Unix
// (signal 0: the existence probe). EPERM means the process exists but belongs
// to another user and still counts as alive.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	}
	return err == syscall.EPERM
}

func lockNative(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlockNative(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
