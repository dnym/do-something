package scoring

import (
	"dosomething/internal/model"
	"testing"
)

func TestRemainingDurationScoring(t *testing.T) {
	for _, kind := range model.AllKinds {
		t.Run(string(kind), func(t *testing.T) {
			var previous float64
			for _, tc := range []struct {
				remaining, desirability float64
			}{{40, 1.0 / 3}, {0.4, 20 / 20.4}, {0, 1}} {
				it := mkItem(kind, func(i *model.Item) {
					i.TotalDuration = fptr(40)
					i.RemainingDuration = fptr(tc.remaining)
				})
				result, err := Score(it, Context{}, DefaultConfig(), testNow)
				if err != nil {
					t.Fatal(err)
				}
				m := memberOf(t, result, model.GroupCommitment, model.PropRemainingDuration)
				want(t, "remaining desirability", m.D, tc.desirability)
				if result.Final <= previous {
					t.Fatalf("less remaining time did not increase score: %v <= %v", result.Final, previous)
				}
				previous = result.Final
				// Total and minimum session are context/filter inputs, never a
				// percentage bonus or a second duration cost.
				it.TotalDuration = fptr(100)
				it.MinSessionDuration = fptr(1)
				changed, err := Score(it, Context{}, DefaultConfig(), testNow)
				if err != nil {
					t.Fatal(err)
				}
				want(t, "unchanged score after revising total/session", changed.Final, result.Final)
			}
			it := mkItem(kind, func(i *model.Item) { i.TotalDuration = fptr(40) })
			result, err := Score(it, Context{}, DefaultConfig(), testNow)
			if err != nil {
				t.Fatal(err)
			}
			m := memberOf(t, result, model.GroupCommitment, model.PropRemainingDuration)
			if m.State != model.StateUnknown || m.D != 0.5 {
				t.Fatalf("total must not stand in for unknown remaining: %+v", m)
			}
		})
	}
}
