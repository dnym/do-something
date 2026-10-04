package filter

import (
	"dosomething/internal/model"
	"testing"
)

func TestMissingAndSetSemantics(t *testing.T) {
	for _, tt := range []struct {
		name     string
		q        Query
		it       model.Item
		want     bool
		evidence string
	}{{"unknown passes", Query{Unknown: "include", Budget: model.FloatPtr(10)}, model.Item{}, true, "unknown"}, {"unknown strict", Query{Unknown: "exclude", Budget: model.FloatPtr(10)}, model.Item{}, false, "unknown"}, {"known exceeds", Query{Unknown: "include", Budget: model.FloatPtr(10)}, model.Item{CostLeft: model.FloatPtr(11)}, false, "known"}, {"known equal", Query{Unknown: "include", Budget: model.FloatPtr(10)}, model.Item{CostLeft: model.FloatPtr(10)}, true, "known"}, {"missing tag is absence", Query{Tags: []string{"x"}}, model.Item{}, false, ""}, {"not tag matches empty", Query{NotTags: []string{"x"}}, model.Item{}, true, ""}, {"case preserved", Query{Tags: []string{"X"}}, model.Item{Tags: []string{"x"}}, false, ""}} {
		t.Run(tt.name, func(t *testing.T) {
			pass, e := tt.q.Match(&tt.it)
			if pass != tt.want || e["budget"] != tt.evidence {
				t.Fatal(pass, e)
			}
		})
	}
}

// TestModeSoftness: a selected mode is a ranking preference, never a hard
// filter. It must pass every item (matched, unclassified, or not), and it
// must leave no filter evidence behind.
func TestModeSoftness(t *testing.T) {
	q := Query{Mode: "thinking"}
	for _, it := range []*model.Item{
		{ID: "a", Title: "T", Kind: model.KindProject, Modes: []string{"thinking"}},
		{ID: "b", Title: "T", Kind: model.KindProject, Modes: []string{}},
		{ID: "c", Title: "T", Kind: model.KindProject, Modes: []string{"making", "hands_on"}},
	} {
		pass, e := q.Match(it)
		if !pass {
			t.Errorf("mode must never exclude item %s", it.ID)
		}
		if _, ok := e["mode"]; ok {
			t.Errorf("mode must not produce filter evidence, got %v", e)
		}
	}
}

// TestEngagementSoftness: a selected engagement is a ranking preference,
// never a hard filter, and leaves no filter evidence behind.
func TestEngagementSoftness(t *testing.T) {
	q := Query{Engagement: "focused"}
	for _, it := range []*model.Item{
		{ID: "a", Title: "T", Kind: model.KindProject, Engagements: []string{"focused"}},
		{ID: "b", Title: "T", Kind: model.KindProject, Engagements: []string{}},
		{ID: "c", Title: "T", Kind: model.KindProject, Engagements: []string{"loose"}},
	} {
		pass, e := q.Match(it)
		if !pass {
			t.Errorf("engagement must never exclude item %s", it.ID)
		}
		if _, ok := e["engagement"]; ok {
			t.Errorf("engagement must not produce filter evidence, got %v", e)
		}
	}
}

func TestModeValidate(t *testing.T) {
	base := Query{Unknown: "include"}
	for _, m := range []string{"", "movement", "hands_on", "thinking", "making"} {
		q := base
		q.Mode = m
		if e := q.Validate(); e != nil {
			t.Errorf("mode %q: %v", m, e)
		}
	}
	for _, m := range []string{"zen", "THINKING", " thinking"} {
		q := base
		q.Mode = m
		if e := q.Validate(); e == nil {
			t.Errorf("mode %q: want validation error", m)
		}
	}
}

// TestEffortCeilings: --effort LEVEL is an inclusive ceiling on intensity —
// an item passes when intensity ≤ the level's value (or its scoring.effort
// override); unknown intensities pass and are labelled.
func TestEffortCeilings(t *testing.T) {
	withRating := func(v float64) *model.Item {
		return &model.Item{ID: "x", Title: "T", Kind: model.KindProject, Ratings: map[model.Property]model.Rating{model.PropIntensity: {Property: model.PropIntensity, Value: v}}}
	}
	for _, tt := range []struct {
		level string
		max   *float64
		it    *model.Item
		want  bool
		evide string
	}{
		{"high", model.FloatPtr(model.LevelHigh.Value()), withRating(2.0 / 3.0), true, "known"},                    // at the ceiling: inclusive
		{"high", model.FloatPtr(model.LevelHigh.Value()), withRating(4.0 / 5.0), false, "known"},                   // above the ceiling
		{"medium", model.FloatPtr(model.LevelMedium.Value()), withRating(model.LevelHigh.Value()), false, "known"}, // the old leak: high items no longer pass medium
		{"medium", model.FloatPtr(model.LevelMedium.Value()), withRating(1.0 / 2.0), true, "known"},                // exactly at the ceiling passes
		{"max", model.FloatPtr(model.LevelMax.Value()), withRating(1), true, "known"},
		{"none", model.FloatPtr(model.LevelNone.Value()), withRating(0), true, "known"},
		{"none", model.FloatPtr(model.LevelNone.Value()), withRating(1.0 / 3.0), false, "known"},
		{"low", model.FloatPtr(0.1), withRating(0.05), true, "known"}, // tier-2 override lowers the ceiling
		{"low", model.FloatPtr(0.1), withRating(0.15), false, "known"},
		{"high", model.FloatPtr(model.LevelHigh.Value()), &model.Item{ID: "x", Title: "T", Kind: model.KindProject}, true, "unknown"}, // no intensity rating: unknown passes
	} {
		t.Run(tt.level+"/"+tt.evide, func(t *testing.T) {
			q := Query{Unknown: "include", Effort: tt.level, MaxIntensity: tt.max}
			if e := q.Validate(); e != nil {
				t.Fatalf("validate: %v", e)
			}
			pass, e := q.Match(tt.it)
			if pass != tt.want || e["effort"] != tt.evide {
				t.Fatal(pass, e)
			}
		})
	}
}

func TestEffortValidate(t *testing.T) {
	for _, l := range []string{"", "none", "low", "medium", "high", "very_high", "max"} {
		q := Query{Unknown: "include", Effort: l}
		if e := q.Validate(); e != nil {
			t.Errorf("effort %q: %v", l, e)
		}
	}
	for _, l := range []string{"zen", "MAX", " low"} {
		q := Query{Unknown: "include", Effort: l}
		if e := q.Validate(); e == nil {
			t.Errorf("effort %q: want validation error", l)
		}
	}
}

func TestEngagementValidate(t *testing.T) {
	base := Query{Unknown: "include"}
	for _, e := range []string{"", "focused", "loose"} {
		q := base
		q.Engagement = e
		if e2 := q.Validate(); e2 != nil {
			t.Errorf("engagement %q: %v", e, e2)
		}
	}
	for _, e := range []string{"zen", "FOCUSED", " loose"} {
		q := base
		q.Engagement = e
		if e2 := q.Validate(); e2 == nil {
			t.Errorf("engagement %q: want validation error", e)
		}
	}
}
