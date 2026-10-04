package config

import (
	"dosomething/internal/model"
	"github.com/BurntSushi/toml"
	"os"
	"path/filepath"
	"testing"
)

func TestDevicePrecedence(t *testing.T) {
	t.Setenv("DO_SOMETHING_DB", "env.db")
	c, e := LoadDevice(DeviceOptions{DBPath: "flag.db"}, &LocalFile{DB: "local.db"})
	if e != nil || c.DBPath != "flag.db" {
		t.Fatal(c, e)
	}
	c, e = LoadDevice(DeviceOptions{}, &LocalFile{DB: "local.db"})
	if e != nil || c.DBPath != "env.db" {
		t.Fatal(c, e)
	}
}
func TestConfigValidation(t *testing.T) {
	for _, tt := range []struct {
		k, v  string
		valid bool
	}{{"scoring.beta", "1.2", false}, {"scoring.k.cost_left", "0", false}, {"scoring.k.cost_left", "250", true}, {"scoring.weights.project.career", "NaN", false}, {"scoring.duration_anchors", "1,2", false}, {"vocabulary.types", "game,film,custom", true}} {
		e := ValidateValue(tt.k, tt.v)
		if (e == nil) != tt.valid {
			t.Fatal(tt, e)
		}
	}
}
func FuzzLocalTOML(f *testing.F) {
	f.Add([]byte("db = 'list.db'\nlang = 'sv'"))
	f.Fuzz(func(t *testing.T, data []byte) { var lf LocalFile; _ = toml.Unmarshal(data, &lf) })
}
func TestMalformedLocalFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "local.toml")
	if e := os.WriteFile(p, []byte("db = ["), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadLocalFile(p); e == nil {
		t.Fatal("accepted malformed TOML")
	}
}

// TestEffortThresholds: tier-1 effort ceilings are the level's canonical
// values; a tier-2 override (scoring.effort.<level>) wins.
func TestEffortThresholds(t *testing.T) {
	base := NewConfig(nil)
	for _, l := range model.AllLevels {
		if got := base.EffortThreshold(l); got != l.Value() {
			t.Errorf("default ceiling for %s = %v, want %v", l, got, l.Value())
		}
	}
	ov := NewConfig(map[string]string{"scoring.effort.medium": "0.2", "scoring.effort.none": "0.1"})
	if got := ov.EffortThreshold(model.LevelMedium); got != 0.2 {
		t.Errorf("override medium = %v, want 0.2", got)
	}
	if got := ov.EffortThreshold(model.LevelNone); got != 0.1 {
		t.Errorf("override none = %v, want 0.1", got)
	}
	if got := ov.EffortThreshold(model.LevelHigh); got != model.LevelHigh.Value() {
		t.Errorf("unoverridden high = %v, want %v", got, model.LevelHigh.Value())
	}
}

func TestCustomVocabularyRetainsListType(t *testing.T) {
	c := NewConfig(map[string]string{"vocabulary.tags": "one,two", "vocabulary.categories": "123"})
	tags, ok := c.StringSlice("vocabulary.tags")
	if !ok || len(tags) != 2 || tags[0] != "one" {
		t.Fatal(tags, ok)
	}
	categories, ok := c.StringSlice("vocabulary.categories")
	if !ok || len(categories) != 1 || categories[0] != "123" {
		t.Fatal(categories, ok)
	}
}
