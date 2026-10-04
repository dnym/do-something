// Package importp provides the LWW upsert kernel for canonical JSON imports.
package importp

import (
	"dosomething/internal/output"
	"dosomething/internal/store"
	"fmt"
	"time"
)

type Issue struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}
type Report struct {
	SchemaVersion int     `json:"schema_version"`
	Added         int     `json:"added"`
	Updated       int     `json:"updated"`
	Skipped       int     `json:"skipped"`
	Invalid       int     `json:"invalid"`
	Issues        []Issue `json:"issues"`
	DryRun        bool    `json:"dry_run"`
	Changed       bool    `json:"changed"`
}

func Upsert(local, in store.Content) (store.Content, Report, error) {
	rep := Report{SchemaVersion: output.SchemaVersion, Issues: []Issue{}}
	out := local
	items := map[string]store.Row{}
	legacy := map[string]string{}
	for _, it := range local.Items {
		id := it["id"].(string)
		items[id] = it
		if it["legacy_id"] != nil {
			legacy[fmt.Sprint(it["legacy_id"])] = id
		}
	}
	remap := map[string]string{}
	for _, it := range in.Items {
		id := it["id"].(string)
		if old := legacy[fmt.Sprint(it["legacy_id"])]; old != "" && it["legacy_id"] != nil {
			remap[id] = old
			it["id"] = old
		}
	}
	for _, t := range in.Tables() {
		for _, r := range t.Rows {
			for _, k := range []string{"item_id", "depends_on"} {
				if v, ok := r[k].(string); ok && remap[v] != "" {
					r[k] = remap[v]
				}
			}
		}
	}
	accepted := map[string]bool{}
	for _, it := range in.Items {
		id := it["id"].(string)
		old := items[id]
		if old != nil {
			if it["legacy_id"] != nil && sameItem(local, in, old, it) {
				rep.Skipped++
				continue
			}
			a, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(old["updated_at"]))
			b, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(it["updated_at"]))
			if !b.After(a) {
				rep.Skipped++
				continue
			}
			rep.Updated++
		} else {
			rep.Added++
		}
		items[id] = it
		accepted[id] = true
	}
	out.Items = []store.Row{}
	for _, it := range items {
		out.Items = append(out.Items, it)
	}
	lt := local.Tables()
	for i, t := range in.Tables() {
		if t.Name == "items" || t.Name == "events" {
			continue
		}
		rows := []store.Row{}
		if t.Name == "config" {
			byKey := map[string]store.Row{}
			for _, r := range lt[i].Rows {
				byKey[r["key"].(string)] = r
			}
			for _, r := range t.Rows {
				k := r["key"].(string)
				old := byKey[k]
				a, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(r["updated_at"]))
				b, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(old["updated_at"]))
				if old == nil || a.After(b) {
					byKey[k] = r
				}
			}
			for _, r := range byKey {
				rows = append(rows, r)
			}
		} else {
			for _, r := range lt[i].Rows {
				if !accepted[r["item_id"].(string)] {
					rows = append(rows, r)
				}
			}
			for _, r := range t.Rows {
				if accepted[r["item_id"].(string)] {
					rows = append(rows, r)
				}
			}
		}
		out.SetTable(t.Name, rows)
	}
	ev := map[string]store.Row{}
	for _, r := range local.Events {
		ev[r["id"].(string)] = r
	}
	for _, r := range in.Events {
		id := r["id"].(string)
		if old := ev[id]; old != nil && !store.EqualRows(old, r) {
			return local, rep, fmt.Errorf("event UUID payload conflict %s", id)
		}
		ev[id] = r
	}
	out.Events = []store.Row{}
	for _, r := range ev {
		out.Events = append(out.Events, r)
	}
	if e := out.Validate(); e != nil {
		return local, rep, e
	}
	a, _ := local.Digest(true)
	b, _ := out.Digest(true)
	rep.Changed = a != b
	return out, rep, nil
}
func sameItem(local, in store.Content, old, it store.Row) bool {
	t := local.Tables()[0]
	a, b := store.Row{}, store.Row{}
	for _, k := range t.Columns {
		if k == "updated_at" || k == "created_at" {
			continue
		}
		a[k] = old[k]
		b[k] = it[k]
	}
	if !store.EqualRows(a, b) {
		return false
	}
	lt, rt := local.Tables(), in.Tables()
	for i := 1; i <= 4; i++ {
		a, b := map[string]store.Row{}, map[string]store.Row{}
		for _, r := range lt[i].Rows {
			if r["item_id"] == old["id"] {
				a[store.RowKey(lt[i], r)] = r
			}
		}
		for _, r := range rt[i].Rows {
			if r["item_id"] == it["id"] {
				b[store.RowKey(rt[i], r)] = r
			}
		}
		if len(a) != len(b) {
			return false
		}
		for k, v := range a {
			if !store.EqualRows(v, b[k]) {
				return false
			}
		}
	}
	return true
}
