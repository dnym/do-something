//go:build windows

package store

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// th32CS_SNAPPROCESS is the CreateToolhelp32Snapshot flag for a process list.
const th32CS_SNAPPROCESS = 0x00000002

// pidAlive reports whether a process with the given pid exists on Windows
// (Toolhelp32 process snapshot).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	snap, err := windows.CreateToolhelp32Snapshot(th32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snap, &pe); err != nil {
		return false
	}
	for {
		if int(pe.ProcessID) == pid {
			return true
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return false
}

func lockNative(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{OffsetHigh: 1})
}
func unlockNative(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{OffsetHigh: 1})
}
