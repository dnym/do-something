package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// safeName allows a token to be used as a single filesystem path component
// (device ids, digest names). Filenames are always these immutable tokens; a
// renamed pretty name can never spawn a phantom peer (plan §3.1).
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// historyRetention is the number of most-recent archives kept per peer by GC.
const historyRetention = 10

// DeviceID returns this installation's device id, minting (and persisting) it
// on first use. The id is machine-local: it names this device's published
// snapshot file and travels in nothing we publish.
func (s *Store) DeviceID() (string, error) {
	path := filepath.Join(s.stateDir, "device-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if safeName.MatchString(id) {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return "", fmt.Errorf("store: create state dir: %w", err)
	}
	id := newDatabaseUUID()
	tmp := uniqueTempName(s.stateDir, "device-id")
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := fsyncFile(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := replaceFile(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	_ = fsyncDir(s.stateDir)
	return id, nil
}

// SyncStateDir is the machine-local per-peer state directory.
func (s *Store) SyncStateDir() string { return filepath.Join(s.stateDir, "sync-state") }

// PeerStateDir is the per-peer state directory (baseline + snapshot copies).
func (s *Store) PeerStateDir(peer string) (string, error) {
	if !safeName.MatchString(peer) {
		return "", fmt.Errorf("store: unsafe peer name %q", peer)
	}
	return filepath.Join(s.SyncStateDir(), peer), nil
}

// Peers lists peers with recorded state (directory names under sync-state/).
func (s *Store) Peers() ([]string, error) {
	entries, err := os.ReadDir(s.SyncStateDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// PeerBaseline returns the digest of the peer's last-observed state, or
// ("", false) if none has been recorded. The baseline is "the last state we
// observed this peer at" — deliberately not claimed to be a common ancestor.
func (s *Store) PeerBaseline(peer string) (string, bool, error) {
	dir, err := s.PeerStateDir(peer)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "baseline"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	digest := strings.TrimSpace(string(data))
	if digest != "" && !safeName.MatchString(digest) {
		return "", false, ErrStoreCorrupt
	}
	return digest, digest != "", nil
}

// SavePeerState records the peer's observed state: a content-addressed copy at
// sync-state/<peer>/<digest>.db plus the baseline digest. digest is the peer
// file's typed content digest (computed by the sync layer) and srcFile is the
// validated snapshot to copy.
func (s *Store) SavePeerState(peer, digest, srcFile string) error {
	unlock, err := s.maintenanceLock()
	if err != nil {
		return err
	}
	defer unlock()
	if !safeName.MatchString(digest) {
		return fmt.Errorf("store: unsafe digest %q", digest)
	}
	dir, err := s.PeerStateDir(peer)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := copyFile(srcFile, filepath.Join(dir, digest+".db")); err != nil {
		return err
	}
	if err := writeSmallFile(filepath.Join(dir, "baseline"), digest+"\n"); err != nil {
		return err
	}
	return fsyncDir(dir)
}

// HistoryDir is the machine-local archive directory for sync inputs.
func (s *Store) HistoryDir() string { return filepath.Join(s.stateDir, "history") }

// HistoryPath is the archive path for a digest ("", false if not archived).
func (s *Store) HistoryPath(digest string) (string, bool) {
	if !safeName.MatchString(digest) {
		return "", false
	}
	p := filepath.Join(s.HistoryDir(), digest+".db")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// HistoryArchive content-addresses srcFile (a validated sync input) into
// history/ under digest, recording which peer it came from. Identical digests
// are stored once (dedup). The digest is caller-supplied — the typed content
// digest on the sync path — so history names and peer baselines share one
// namespace, which the retention GC relies on. Returns the stored path and
// whether it was already present.
func (s *Store) HistoryArchive(digest, peer, srcFile string) (string, bool, error) {
	unlock, err := s.maintenanceLock()
	if err != nil {
		return "", false, err
	}
	defer unlock()
	if !safeName.MatchString(digest) {
		return "", false, fmt.Errorf("store: unsafe digest %q", digest)
	}
	if peer != "" && !safeName.MatchString(peer) {
		return "", false, fmt.Errorf("store: unsafe peer name %q", peer)
	}
	dir := s.HistoryDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("store: create history dir: %w", err)
	}
	target := filepath.Join(dir, digest+".db")
	existed := fileExists(target)
	if !existed {
		if err := copyFile(srcFile, target); err != nil {
			return "", false, err
		}
	}
	if peer != "" {
		if err := appendPeerMeta(dir, digest, peer); err != nil {
			return "", false, err
		}
	}
	return target, existed, nil
}

// HistoryGC applies the retention policy (plan §3.1): keep every digest
// referenced by a peer baseline plus, per peer, the historyRetention most
// recently archived entries; delete the rest. Entries without provenance are
// kept (conservative). Returns the removed archive paths.
func (s *Store) HistoryGC() ([]string, error) {
	unlock, err := s.maintenanceLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	dir := s.HistoryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	digests := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".db") {
			continue
		}
		digest := strings.TrimSuffix(name, ".db")
		if safeName.MatchString(digest) {
			digests[digest] = true
		}
	}
	if len(digests) == 0 {
		return nil, nil
	}

	// Always kept: digests referenced by a peer baseline.
	kept := map[string]bool{}
	if peers, perr := s.Peers(); perr == nil {
		for _, p := range peers {
			if d, ok, _ := s.PeerBaseline(p); ok {
				kept[d] = true
			}
		}
	}

	// Per-peer provenance: peer → digest → archived-at. Digests without a meta
	// file are kept (conservative).
	peerHist := map[string]map[string]string{}
	for d := range digests {
		m, ok := readPeerMeta(dir, d)
		if !ok {
			kept[d] = true
			continue
		}
		for p, ts := range m {
			if peerHist[p] == nil {
				peerHist[p] = map[string]string{}
			}
			peerHist[p][d] = ts
		}
	}
	for _, hist := range peerHist {
		type rec struct {
			digest string
			ts     string
		}
		recs := make([]rec, 0, len(hist))
		for d, ts := range hist {
			recs = append(recs, rec{d, ts})
		}
		sort.Slice(recs, func(i, j int) bool {
			if recs[i].ts != recs[j].ts {
				a, _ := time.Parse(time.RFC3339Nano, recs[i].ts)
				b, _ := time.Parse(time.RFC3339Nano, recs[j].ts)
				return a.After(b)
			}
			return recs[i].digest < recs[j].digest
		})
		for i, r := range recs {
			if i >= historyRetention {
				break
			}
			kept[r.digest] = true
		}
	}

	var removed []string
	for d := range digests {
		if kept[d] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, d+".db")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		_ = os.Remove(filepath.Join(dir, d+".meta"))
		removed = append(removed, d+".db")
	}
	// Baseline copies share the same retention set as history. Current peer
	// baselines are always protected; orphan copies without provenance stay.
	if peers, e := s.Peers(); e == nil {
		for _, peer := range peers {
			dir, e := s.PeerStateDir(peer)
			if e != nil {
				return nil, e
			}
			entries, e := os.ReadDir(dir)
			if e != nil {
				return nil, e
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
					continue
				}
				digest := strings.TrimSuffix(entry.Name(), ".db")
				if digests[digest] && !kept[digest] {
					if e := os.Remove(filepath.Join(dir, entry.Name())); e != nil && !os.IsNotExist(e) {
						return nil, e
					}
					removed = append(removed, filepath.Join("sync-state", peer, entry.Name()))
				}
			}
		}
	}
	if len(removed) > 0 {
		sort.Strings(removed)
		_ = fsyncDir(dir)
	}
	return removed, nil
}

// Peer archive provenance sidecar: one line per peer, "peer=<p> ts=<ts>",
// sorted by peer for deterministic content.
func readPeerMeta(dir, digest string) (map[string]string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, digest+".meta"))
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "peer=") {
			continue
		}
		peer := strings.TrimPrefix(fields[0], "peer=")
		ts := ""
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "ts=") {
				ts = strings.TrimPrefix(f, "ts=")
			}
		}
		out[peer] = ts
	}
	return out, len(out) > 0
}

func appendPeerMeta(dir, digest, peer string) error {
	m := map[string]string{}
	if prev, ok := readPeerMeta(dir, digest); ok {
		m = prev
	}
	m[peer] = nowRFC3339()
	var lines []string
	for p := range m {
		lines = append(lines, fmt.Sprintf("peer=%s ts=%s", p, m[p]))
	}
	sort.Strings(lines)
	return writeSmallFile(filepath.Join(dir, digest+".meta"), strings.Join(lines, "\n")+"\n")
}

// copyFile installs src at dst via the platform-safe replacement primitive
// (tmp + fsync + swap in the same directory).
func copyFile(src, dst string) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := uniqueTempName(dir, filepath.Base(dst))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := fsyncFile(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := replaceFile(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return fsyncDir(dir)
}

// writeSmallFile writes content to path via the platform-safe replacement
// primitive.
func writeSmallFile(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := uniqueTempName(dir, filepath.Base(path))
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := fsyncFile(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := replaceFile(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return fsyncDir(dir)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Metadata cleanup has its own lock so doctor remains lock-free on the working
// database while baseline installation and archive GC cannot race one another.
func (s *Store) maintenanceLock() (func(), error) {
	return Acquire(Options{DBPath: filepath.Join(s.stateDir, "maintenance"), StateDir: s.stateDir})
}
