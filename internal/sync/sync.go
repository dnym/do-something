// Package sync implements conservative content resolution, with event union in
// every outcome. A peer baseline is an observation, never proof of ancestry.
package sync

import (
	"dosomething/internal/output"
	"dosomething/internal/store"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrConflict = errors.New("SYNC_CONFLICT")
var ErrIdentity = errors.New("SYNC_IDENTITY_MISMATCH")

type Options struct {
	Resolution string
	Master     string
	Choices    map[string]string
	DryRun     bool
}
type Conflict struct {
	Table  string    `json:"table"`
	Key    string    `json:"key"`
	Base   store.Row `json:"base"`
	Local  store.Row `json:"local"`
	Remote store.Row `json:"remote"`
}
type Report struct {
	SchemaVersion   int        `json:"schema_version"`
	Decision        string     `json:"decision"`
	Changed         bool       `json:"changed"`
	DryRun          bool       `json:"dry_run"`
	Conflicts       []Conflict `json:"conflicts"`
	CorruptEvents   []string   `json:"corrupt_events"`
	Archives        []string   `json:"archives"`
	Baseline        string     `json:"baseline"`
	RollbackWarning bool       `json:"rollback_warning"`
}

func index(t store.Table) map[string]store.Row {
	m := map[string]store.Row{}
	for _, r := range t.Rows {
		m[store.RowKey(t, r)] = r
	}
	return m
}
func keys(maps ...map[string]store.Row) []string {
	m := map[string]bool{}
	for _, a := range maps {
		for k := range a {
			m[k] = true
		}
	}
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func Reconcile(l, r []store.Row) ([]store.Row, []string) {
	var t store.Table
	for _, c := range store.EmptyContent().Tables() {
		if c.Name == "events" {
			t = c
			break
		}
	}
	t.Rows = l
	a := index(t)
	t.Rows = r
	b := index(t)
	out := []store.Row{}
	bad := []string{}
	for _, k := range keys(a, b) {
		x, y := a[k], b[k]
		if x != nil && y != nil && !store.EqualRows(x, y) {
			bad = append(bad, x["id"].(string))
			continue
		}
		if x == nil {
			x = y
		}
		out = append(out, x)
	}
	return out, bad
}
func Merge(base, local, remote store.Content, master string, choices ...map[string]string) (store.Content, []Conflict, bool) {
	out := store.EmptyContent()
	conflicts := []Conflict{}
	deletes, changes := 0, 0
	bt, lt, rt := base.Tables(), local.Tables(), remote.Tables()
	for i, t := range lt {
		if t.Name == "events" {
			continue
		}
		b, l, r := index(bt[i]), index(t), index(rt[i])
		rows := []store.Row{}
		for _, k := range keys(b, l, r) {
			bv, lv, rv := b[k], l[k], r[k]
			if !store.EqualRows(bv, rv) {
				changes++
				if rv == nil {
					deletes++
				}
			}
			var v store.Row
			switch {
			case store.EqualRows(lv, rv):
				v = lv
			case store.EqualRows(lv, bv):
				v = rv
			case store.EqualRows(rv, bv):
				v = lv
			default:
				conflicts = append(conflicts, Conflict{t.Name, k, bv, lv, rv})
				v = lv
				choice := master
				if len(choices) > 0 && choices[0][t.Name+"/"+k] != "" {
					choice = choices[0][t.Name+"/"+k]
				}
				if choice == "remote" {
					v = rv
				}
			}
			if v != nil {
				rows = append(rows, v)
			}
		}
		out.SetTable(t.Name, rows)
	}
	return out, conflicts, deletes > 0 && deletes*2 >= changes
}
func Resolve(l, r store.Content, base *store.Content, o Options) (store.Content, Report, error) {
	rep := Report{SchemaVersion: output.SchemaVersion, DryRun: o.DryRun, Conflicts: []Conflict{}, CorruptEvents: []string{}, Archives: []string{}}
	ld, e := l.Digest(true)
	if e != nil {
		return l, rep, e
	}
	rd, e := r.Digest(true)
	if e != nil {
		return l, rep, e
	}
	ln, _ := l.Digest(false)
	rn, _ := r.Digest(false)
	out := l
	switch {
	case ld == rd:
		rep.Decision = "noop"
	case ln == rn:
		rep.Decision = "auto_union"
	default:
		switch o.Resolution {
		case "keep-local":
			rep.Decision = "keep_local"
		case "take-remote":
			rep.Decision = "take_remote"
			out = r
		case "merge":
			if base == nil {
				return l, rep, store.ErrNoPeerBase
			}
			rep.Decision = "merged"
			out, rep.Conflicts, rep.RollbackWarning = Merge(*base, l, r, o.Master, o.Choices)
			if len(rep.Conflicts) > 0 && o.Master == "" {
				for _, conflict := range rep.Conflicts {
					choice := o.Choices[conflict.Table+"/"+conflict.Key]
					if choice != "local" && choice != "remote" {
						return l, rep, ErrConflict
					}
				}
			}
		default:
			rep.Decision = "requires_resolution"
			return l, rep, ErrConflict
		}
	}
	out.Events, rep.CorruptEvents = Reconcile(l.Events, r.Events)
	if e = out.Validate(); e != nil {
		return l, rep, fmt.Errorf("%w: resolved state is invalid: %v", ErrConflict, e)
	}
	od, _ := out.Digest(true)
	rep.Changed = od != ld
	rep.Baseline = rd
	return out, rep, nil
}

// Run requires the caller's application lock. The input is first captured to a
// private snapshot, so validation, archives, resolution and baseline agree even
// when a folder-sync process replaces the peer file during this command.
func Run(s *store.Store, remote string, o Options) (Report, error) {
	tmp, e := os.MkdirTemp("", "do-something-sync-")
	if e != nil {
		return Report{}, e
	}
	defer os.RemoveAll(tmp)
	captured := filepath.Join(tmp, "remote.db")
	if e = store.CaptureSnapshot(remote, captured); e != nil {
		return Report{}, e
	}
	r, rid, e := store.ReadSnapshot(captured)
	if e != nil {
		return Report{}, e
	}
	lid, e := s.DatabaseUUID()
	if e != nil {
		return Report{}, e
	}
	if lid != rid {
		return Report{}, ErrIdentity
	}
	l, e := s.Content()
	if e != nil {
		return Report{}, e
	}
	peer := strings.TrimSuffix(filepath.Base(remote), filepath.Ext(remote))
	var base *store.Content
	digest, ok, e := s.PeerBaseline(peer)
	if e != nil {
		return Report{}, e
	}
	if ok {
		dir, e := s.PeerStateDir(peer)
		if e != nil {
			return Report{}, e
		}
		b, id, e := store.ReadSnapshot(filepath.Join(dir, digest+".db"))
		if e != nil {
			return Report{}, e
		}
		actual, _ := b.Digest(true)
		if actual != digest || id != lid {
			return Report{}, store.ErrStoreCorrupt
		}
		base = &b
	}
	out, rep, resolveErr := Resolve(l, r, base, o)
	if o.DryRun {
		return rep, resolveErr
	}
	// Archive even when conflict resolution needs further input.
	localFile := filepath.Join(tmp, "local.db")
	if e = s.VacuumInto(localFile); e != nil {
		return rep, e
	}
	ld, _ := l.Digest(true)
	rd, _ := r.Digest(true)
	for _, a := range []struct{ digest, path string }{{ld, localFile}, {rd, captured}} {
		path, _, e := s.HistoryArchive(a.digest, peer, a.path)
		if e != nil {
			return rep, e
		}
		rep.Archives = append(rep.Archives, path)
	}
	if resolveErr != nil {
		return rep, resolveErr
	}
	if rep.Changed {
		if e = s.ApplyContent(out); e != nil {
			return rep, e
		}
	}
	if e = s.SavePeerState(peer, rd, captured); e != nil {
		return rep, e
	}
	_, e = s.HistoryGC()
	return rep, e
}
