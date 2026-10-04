//go:build windows

package store

import (
	"golang.org/x/sys/windows"
)

// platformSwap performs the file replacement on Windows via
// MoveFileEx(MOVEFILE_REPLACE_EXISTING) — the best swap Windows offers. Windows
// provides no guaranteed-atomic rename, so a torn replacement is possible; the
// consumer-side validation (rejecting a corrupt published file) is the backstop,
// and the next publish self-heals the file.
func platformSwap(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING)
}
