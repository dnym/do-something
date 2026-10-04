// Package paths provides platform-native directory defaults for do-something.
//
// The defaults are the only machine-level concern that lives outside the
// database; everything else (working-DB path, sync folder, colour, language
// pin, device name) can be overridden by flags, environment variables, or the
// tier-3 per-device file (see the config package).
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// AppName is the application directory/file basename used on every platform.
const AppName = "do-something"

// Environment variable names that override the tier-3 defaults.
const (
	EnvDB      = "DO_SOMETHING_DB"      // working-store path
	EnvSyncDir = "DO_SOMETHING_SYNCDIR" // sync folder path
	EnvLang    = "DO_SOMETHING_LANG"    // display-language pin
)

// PlatformDataDir returns the platform-native application data directory.
//
//   - Linux:   $XDG_DATA_HOME (default ~/.local/share) + AppName
//   - macOS:   ~/Library/Application Support/AppName
//   - Windows: %LOCALAPPDATA%/AppName
//
// This is where the working store, device-id, sync-state, and history live. It
// is never a synced folder.
func PlatformDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, AppName)
		}
		if up := os.Getenv("USERPROFILE"); up != "" {
			return filepath.Join(up, AppName)
		}
		return "."
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, "Library", "Application Support", AppName)
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, AppName)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, ".local", "share", AppName)
	}
}

// PlatformConfigDir returns the platform-native user configuration directory
// (the parent of the tier-3 local.toml file).
//
//   - Linux:   $XDG_CONFIG_HOME (default ~/.config) + AppName
//   - macOS:   ~/Library/Application Support/AppName
//   - Windows: %APPDATA%/AppName
func PlatformConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, AppName)
		}
		return PlatformDataDir()
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, "Library", "Application Support", AppName)
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, AppName)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, ".config", AppName)
	}
}

// LocalConfigFile returns the tier-3 per-device TOML file path.
func LocalConfigFile() string { return filepath.Join(PlatformConfigDir(), "local.toml") }

// DefaultDBFile returns the default working-store path (overridable).
func DefaultDBFile() string { return filepath.Join(PlatformDataDir(), "list.db") }

// ExpandHome expands a leading "〜"/"~" to the user's home directory. Relative
// paths are returned unchanged; absolute paths are returned as-is.
func ExpandHome(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
		return p
	}
	for _, c := range []string{"~/", "~\\"} {
		if len(p) >= len(c) && p[:len(c)] == c {
			if h, err := os.UserHomeDir(); err == nil {
				return filepath.Join(h, p[len(c):])
			}
		}
	}
	return p
}

// Canonical resolves existing symlinked ancestors even before the final path
// exists. It gives aliases of one store the same lock and sync-folder checks.
func Canonical(path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	if resolved, e := filepath.EvalSymlinks(abs); e == nil {
		return resolved, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}
	resolved, e := Canonical(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(resolved, filepath.Base(abs)), nil
}
