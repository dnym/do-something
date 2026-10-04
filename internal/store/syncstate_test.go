package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"dosomething/internal/model"
)

// openTestStoresA opens two independent throwaway stores (A and B) so tests
// can simulate two devices.
func openTwoStores(t *testing.T) (a, b *Store) {
	t.Helper()
	open := func() *Store {
		dir := t.TempDir()
		st, err := Open(OpenWrite, Options{DBPath: filepath.Join(dir, "list.db"), StateDir: dir})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	return open(), open()
}

// writeFakeRemote creates a plain file standing in for a remote snapshot.
func writeFakeRemote(t *testing.T, st *Store, name, content string) string {
	t.Helper()
	path := filepath.Join(st.StateDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake remote: %v", err)
	}
	return path
}

// TestDeviceIDStable verifies the device id is minted once and stable, and
// that a different installation mints a different id.
func TestDeviceIDStable(t *testing.T) {
	st := openTestStore(t)
	id1, err := st.DeviceID()
	if err != nil {
		t.Fatalf("device id: %v", err)
	}
	if id1 == "" {
		t.Fatal("empty device id")
	}
	id2, err := st.DeviceID()
	if err != nil {
		t.Fatalf("device id again: %v", err)
	}
	if id1 != id2 {
		t.Errorf("device id not stable: %q vs %q", id1, id2)
	}
	st2 := openTestStore(t)
	id3, _ := st2.DeviceID()
	if id3 == id1 {
		t.Errorf("two installations share a device id: %q", id1)
	}
}

// TestPeerBaseline verifies per-peer baseline bookkeeping and snapshot copies.
func TestPeerBaseline(t *testing.T) {
	st := openTestStore(t)
	remote := writeFakeRemote(t, st, "remote.bin", "state-A")

	if d, ok, err := st.PeerBaseline("peerX"); err != nil || ok || d != "" {
		t.Fatalf("no baseline yet: %q %v %v", d, ok, err)
	}
	if err := st.SavePeerState("peerX", "digestA", remote); err != nil {
		t.Fatalf("save peer state: %v", err)
	}
	d, ok, err := st.PeerBaseline("peerX")
	if err != nil || !ok || d != "digestA" {
		t.Fatalf("baseline: %q %v %v", d, ok, err)
	}
	// The content-addressed copy must exist and match the source.
	dir, err := st.PeerStateDir("peerX")
	if err != nil {
		t.Fatalf("peer dir: %v", err)
	}
	copyData, err := os.ReadFile(filepath.Join(dir, "digestA.db"))
	if err != nil {
		t.Fatalf("snapshot copy: %v", err)
	}
	if string(copyData) != "state-A" {
		t.Errorf("copy content: %q", copyData)
	}
	peers, err := st.Peers()
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if len(peers) != 1 || peers[0] != "peerX" {
		t.Errorf("peers: %v", peers)
	}
	// A new observation updates the baseline and adds a new copy.
	remoteB := writeFakeRemote(t, st, "remoteB.bin", "state-B")
	if err := st.SavePeerState("peerX", "digestB", remoteB); err != nil {
		t.Fatalf("save again: %v", err)
	}
	if d, _, _ := st.PeerBaseline("peerX"); d != "digestB" {
		t.Errorf("baseline not updated: %q", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "digestA.db")); err != nil {
		t.Errorf("old copy should remain: %v", err)
	}
}

// TestHistoryArchiveAndGC verifies dedup, per-peer retention (last
// historyRetention kept), and baseline-referenced digests surviving GC.
func TestHistoryArchiveAndGC(t *testing.T) {
	st := openTestStore(t)
	remote := writeFakeRemote(t, st, "remote.bin", "state")

	const n = 12
	for i := 0; i < n; i++ {
		digest := fmt.Sprintf("d%02d", i)
		if _, existed, err := st.HistoryArchive(digest, "peerX", remote); err != nil {
			t.Fatalf("archive %s: %v", digest, err)
		} else if i == 0 && existed {
			t.Errorf("first archive reported as existing")
		}
		// Deterministic provenance timestamps (the archive sidecar uses wall
		// clock; pin them so retention ranking is exact).
		meta := filepath.Join(st.HistoryDir(), digest+".meta")
		content := fmt.Sprintf("peer=peerX ts=2026-01-01T00:00:%02dZ\n", i)
		if err := os.WriteFile(meta, []byte(content), 0o644); err != nil {
			t.Fatalf("pin meta: %v", err)
		}
	}
	// The oldest archive (d00) is referenced by a peer baseline → must
	// survive GC even though it is outside peerX's ten most recent.
	remote2 := writeFakeRemote(t, st, "remote2.bin", "other")
	if err := st.SavePeerState("peerY", "d00", remote2); err != nil {
		t.Fatalf("baseline d00: %v", err)
	}

	removed, err := st.HistoryGC()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	// peerX keeps d02..d11 (ten most recent by ts); d00 is baseline-protected;
	// only d01 is reclaimable.
	if len(removed) != 1 || removed[0] != "d01.db" {
		t.Errorf("removed: %v (want [d01.db])", removed)
	}
	for _, digest := range []string{"d00", "d02", "d11"} {
		if _, ok := st.HistoryPath(digest); !ok {
			t.Errorf("%s should survive GC", digest)
		}
	}
	if _, ok := st.HistoryPath("d01"); ok {
		t.Error("d01 should have been removed")
	}
	// GC is idempotent.
	removed, err = st.HistoryGC()
	if err != nil {
		t.Fatalf("gc again: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("second GC removed: %v", removed)
	}
	// Dedup: re-archiving an existing digest is a no-op on the file.
	if _, existed, err := st.HistoryArchive("d02", "peerX", writeFakeRemote(t, st, "different.bin", "different")); err != nil {
		t.Fatalf("re-archive: %v", err)
	} else if !existed {
		t.Error("re-archive of existing digest reported as new")
	}
	data, _ := os.ReadFile(filepath.Join(st.HistoryDir(), "d02.db"))
	if string(data) != "state" {
		t.Errorf("dedup must not clobber the stored copy: %q", data)
	}
}

// TestReplaceStore verifies take-remote store replacement: the working store
// file is swapped, the replacement's database_uuid is adopted, the store
// validates, and a corrupt source leaves the store untouched.
func TestReplaceStore(t *testing.T) {
	a, b := openTwoStores(t)
	uA, _ := a.DatabaseUUID()
	uB, _ := b.DatabaseUUID()
	if uA == uB {
		t.Fatal("two fresh stores share a uuid")
	}
	remoteItem := addTestItem(t, b, "From B", model.KindProject)

	remote := filepath.Join(b.StateDir(), "remote.db")
	if err := b.VacuumInto(remote); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	if err := a.ReplaceStore(remote); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := a.DatabaseUUID()
	if err != nil {
		t.Fatalf("uuid after replace: %v", err)
	}
	if got != uB {
		t.Errorf("adopted uuid = %q, want %q", got, uB)
	}
	it, err := a.GetItem(remoteItem)
	if err != nil || it.Title != "From B" {
		t.Errorf("replaced store content: %v %v", it, err)
	}
	if err := a.Validate(); err != nil {
		t.Errorf("validate after replace: %v", err)
	}

	// A corrupt source must be rejected and leave the store untouched.
	junk := filepath.Join(a.StateDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("not a database"), 0o644); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if err := a.ReplaceStore(junk); err == nil {
		t.Fatal("corrupt source accepted")
	}
	if u, _ := a.DatabaseUUID(); u != uB {
		t.Errorf("store identity changed by a rejected replacement: %q", u)
	}
	if err := a.Validate(); err != nil {
		t.Errorf("store damaged by a rejected replacement: %v", err)
	}
	if _, err := a.GetItem(remoteItem); err != nil {
		t.Errorf("store content damaged: %v", err)
	}
}

// TestBootstrapFrom verifies the fresh-machine story: the validated remote
// file becomes the working store and its database_uuid is adopted — a blank
// store is never minted first.
func TestBootstrapFrom(t *testing.T) {
	src, _ := openTwoStores(t)
	addTestItem(t, src, "Hello", model.KindProject)
	uSrc, _ := src.DatabaseUUID()

	remote := filepath.Join(src.StateDir(), "remote.db")
	if err := src.VacuumInto(remote); err != nil {
		t.Fatalf("vacuum: %v", err)
	}

	freshDir := t.TempDir()
	freshPath := filepath.Join(freshDir, "list.db")
	if err := BootstrapFrom(freshDir, freshPath, remote); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st, err := Open(OpenWrite, Options{DBPath: freshPath, StateDir: freshDir})
	if err != nil {
		t.Fatalf("open bootstrapped store: %v", err)
	}
	defer st.Close()
	u, err := st.DatabaseUUID()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if u != uSrc {
		t.Errorf("bootstrapped store minted a new uuid (%q) instead of adopting %q", u, uSrc)
	}
	all, err := st.AllItems()
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(all) != 1 || all[0].Title != "Hello" {
		t.Errorf("bootstrapped content: %+v", all)
	}
}

// TestBootstrapFromRejectsCorrupt verifies a corrupt source never becomes the
// working store.
func TestBootstrapFromRejectsCorrupt(t *testing.T) {
	st := openTestStore(t)
	junk := writeFakeRemote(t, st, "junk.db", "garbage")
	dir := t.TempDir()
	freshPath := filepath.Join(dir, "list.db")
	if err := BootstrapFrom(dir, freshPath, junk); err == nil {
		t.Fatal("corrupt source accepted")
	}
	if _, err := os.Stat(freshPath); !os.IsNotExist(err) {
		t.Errorf("working store file created from corrupt source: %v", err)
	}
}
