//go:build !windows

package store

import "os"

// platformSwap performs the file replacement on Unix: a rename within the same
// directory is atomic, so dst is never observed in a torn state.
func platformSwap(src, dst string) error {
	return os.Rename(src, dst)
}
