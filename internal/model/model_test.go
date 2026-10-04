package model

import "testing"

func TestRatingLevelValues(t *testing.T) {
	want := map[RatingLevel]float64{
		LevelNone:     0,
		LevelLow:      1.0 / 3.0,
		LevelMedium:   1.0 / 2.0,
		LevelHigh:     2.0 / 3.0,
		LevelVeryHigh: 4.0 / 5.0,
		LevelMax:      1,
	}
	for _, l := range AllLevels {
		if !l.Valid() {
			t.Errorf("%s must be valid", l)
		}
		if v := l.Value(); v != want[l] {
			t.Errorf("%s value = %v, want %v", l, v, want[l])
		}
	}
	for _, bad := range []string{"", "zen", "LOW", "maximum", "very-high"} {
		if _, ok := ParseRatingLevel(bad); ok {
			t.Errorf("%q must not parse as a level", bad)
		}
	}
}

// The level anchors are ascending, so effort ceilings are monotone: a higher
// level never admits less than a lower one.
func TestRatingLevelOrdering(t *testing.T) {
	for i := 1; i < len(AllLevels); i++ {
		if AllLevels[i].Value() <= AllLevels[i-1].Value() {
			t.Fatalf("levels not strictly ascending at %s", AllLevels[i])
		}
	}
}

func TestLevelForValueRoundTrip(t *testing.T) {
	for _, l := range AllLevels {
		if got := LevelForValue(l.Value()); got != l {
			t.Errorf("LevelForValue(%v) = %q, want %q", l.Value(), got, l)
		}
	}
	for _, v := range []float64{0.1, 0.2, 0.3, 0.4, 0.6, 0.7, 0.9} {
		if got := LevelForValue(v); got != "" {
			t.Errorf("LevelForValue(%v) = %q, want empty (non-anchor)", v, got)
		}
	}
}
