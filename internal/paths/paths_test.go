package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestXDGIncludesApplication(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG only")
	}
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_CONFIG_HOME", "/config")
	if PlatformDataDir() != filepath.Join("/data", AppName) || PlatformConfigDir() != filepath.Join("/config", AppName) {
		t.Fatal("missing application namespace")
	}
}
func TestCanonicalMissingDescendant(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	alias := filepath.Join(dir, "alias")
	if e := os.Mkdir(real, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(real, alias); e != nil {
		t.Skip(e)
	}
	a, e := Canonical(filepath.Join(alias, "new", "list.db"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := Canonical(filepath.Join(real, "new", "list.db"))
	if e != nil || a != b {
		t.Fatal(a, b, e)
	}
}
