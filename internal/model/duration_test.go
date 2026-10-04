package model

import (
	"math"
	"testing"
)

func TestEstimatedProgress(t *testing.T) {
	for _, tc := range []struct {
		name             string
		total, remaining *float64
		want             *float64
	}{
		{"unknown", nil, nil, nil},
		{"remaining only", nil, FloatPtr(2), nil},
		{"total only", FloatPtr(40), nil, nil},
		{"unstarted", FloatPtr(40), FloatPtr(40), FloatPtr(0)},
		{"99 episodes", FloatPtr(40), FloatPtr(0.4), FloatPtr(99)},
		{"finished estimate", FloatPtr(40), FloatPtr(0), FloatPtr(100)},
		{"zero total", FloatPtr(0), FloatPtr(0), nil},
		{"scope exceeded", FloatPtr(10), FloatPtr(12), nil},
		{"revised total", FloatPtr(50), FloatPtr(20), FloatPtr(60)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := &Item{TotalDuration: tc.total, RemainingDuration: tc.remaining, Status: StatusNotStarted}
			got := it.EstimatedProgress()
			if (got == nil) != (tc.want == nil) || got != nil && math.Abs(*got-*tc.want) > 1e-9 {
				t.Fatalf("progress = %v, want %v", got, tc.want)
			}
			if it.Status != StatusNotStarted {
				t.Fatal("progress changed status")
			}
		})
	}
}
