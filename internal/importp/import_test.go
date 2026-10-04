package importp

import (
	"dosomething/internal/store"
	"encoding/json"
	"testing"
	"uuid"
)

func importItem(id, title, updated string) store.Row {
	return store.Row{"id": id, "title": title, "kind": "project", "status": "not_started",
		"created_at": "2026-01-01T00:00:00Z", "updated_at": updated}
}

func TestJSONUpsertLWW(t *testing.T) {
	id := uuid.NewV7().String()
	local := store.EmptyContent()
	local.Items = []store.Row{importItem(id, "Original", "2026-02-01T00:00:00Z")}
	local.Tags = []store.Row{{"item_id": id, "tag": "original"}}
	local.Ratings = []store.Row{{"item_id": id, "property": "interest", "value": 0.5, "rated_at": nil}}
	for _, tc := range []struct {
		name, updated string
		changed       bool
	}{
		{"older", "2026-01-01T00:00:00Z", false},
		{"equal", "2026-02-01T00:00:00Z", false},
		{"newer", "2026-03-01T00:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := store.EmptyContent()
			incoming.Items = []store.Row{importItem(id, "Replacement", tc.updated)}
			incoming.Tags = []store.Row{{"item_id": id, "tag": "replacement"}}
			data, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			incoming, err = store.DecodeContent(data)
			if err != nil {
				t.Fatal(err)
			}
			merged, rep, err := Upsert(local, incoming)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Changed != tc.changed || rep.Added != 0 {
				t.Fatal(rep)
			}
			if tc.changed {
				if rep.Updated != 1 || merged.Items[0]["title"] != "Replacement" || merged.Tags[0]["tag"] != "replacement" || len(merged.Ratings) != 0 {
					t.Fatal(merged, rep)
				}
			} else {
				if rep.Skipped != 1 || merged.Items[0]["title"] != "Original" || merged.Tags[0]["tag"] != "original" || len(merged.Ratings) != 1 {
					t.Fatal(merged, rep)
				}
			}
			_, rep, err = Upsert(merged, incoming)
			if err != nil || rep.Changed || rep.Skipped != 1 {
				t.Fatal(rep, err)
			}
		})
	}
}

func TestJSONLegacyIdentityAndEdges(t *testing.T) {
	oldID, newID, dependentID := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	local := store.EmptyContent()
	local.Items = []store.Row{importItem(oldID, "Existing", "2026-01-01T00:00:00Z")}
	local.Items[0]["legacy_id"] = int64(1)
	incoming := store.EmptyContent()
	incoming.Items = []store.Row{importItem(newID, "Updated", "2026-02-01T00:00:00Z"), importItem(dependentID, "Dependent", "2026-02-01T00:00:00Z")}
	incoming.Items[0]["legacy_id"] = int64(1)
	incoming.Dependencies = []store.Row{{"item_id": dependentID, "depends_on": newID}}
	incoming.Ratings = []store.Row{{"item_id": newID, "property": "interest", "value": 1.0, "rated_at": nil}}
	data, err := json.Marshal(incoming)
	if err != nil {
		t.Fatal(err)
	}
	incoming, err = store.DecodeContent(data)
	if err != nil {
		t.Fatal(err)
	}
	merged, rep, err := Upsert(local, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 1 || rep.Updated != 1 || !rep.Changed || len(merged.Items) != 2 {
		t.Fatal(merged, rep)
	}
	if merged.Dependencies[0]["depends_on"] != oldID || merged.Ratings[0]["item_id"] != oldID {
		t.Fatal(merged)
	}
	_, rep, err = Upsert(merged, incoming)
	if err != nil || rep.Changed || rep.Skipped != 2 {
		t.Fatal(rep, err)
	}
}
