package store

import (
	"dosomething/internal/model"
	"math"
	"reflect"
	"testing"
)

func TestPrepareLog(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		remaining, session, duration *float64
		status                       model.Status
		want                         float64
		reason                       string
	}{
		{"explicit", model.FloatPtr(1), nil, model.FloatPtr(.4), model.StatusInProgress, .6, ""},
		{"fallback", model.FloatPtr(.8), model.FloatPtr(.4), nil, model.StatusInProgress, .4, ""},
		{"overrun", model.FloatPtr(.2), nil, model.FloatPtr(.4), model.StatusInProgress, 0, ""},
		{"already zero", model.FloatPtr(0), nil, model.FloatPtr(.4), model.StatusInProgress, 0, ""},
		{"floating zero", model.FloatPtr(.10000000000000003), nil, model.FloatPtr(.1), model.StatusInProgress, 0, ""},
		{"unknown remaining", nil, model.FloatPtr(1), nil, model.StatusInProgress, 0, "remaining_unknown"},
		{"unknown session", model.FloatPtr(1), nil, nil, model.StatusInProgress, 0, "session_unknown"},
		{"zero session", model.FloatPtr(1), model.FloatPtr(0), nil, model.StatusInProgress, 0, "session_unknown"},
		{"zero explicit", model.FloatPtr(1), nil, model.FloatPtr(0), model.StatusInProgress, 0, "duration_invalid"},
		{"negative", model.FloatPtr(1), nil, model.FloatPtr(-1), model.StatusInProgress, 0, "duration_invalid"},
		{"nan", model.FloatPtr(1), nil, model.FloatPtr(math.NaN()), model.StatusInProgress, 0, "duration_invalid"},
		{"infinite", model.FloatPtr(1), nil, model.FloatPtr(math.Inf(1)), model.StatusInProgress, 0, "duration_invalid"},
		{"not started", model.FloatPtr(1), nil, model.FloatPtr(1), model.StatusNotStarted, 0, "not_in_progress"},
		{"done", model.FloatPtr(1), nil, model.FloatPtr(1), model.StatusDone, 0, "not_in_progress"},
		{"dropped", model.FloatPtr(1), nil, model.FloatPtr(1), model.StatusDropped, 0, "not_in_progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := PrepareLog(&model.Item{RemainingDuration: tc.remaining, MinSessionDuration: tc.session, Status: tc.status}, tc.duration, nil, false)
			if tc.reason != "" {
				e, ok := err.(*LogError)
				if !ok || e.Reason != tc.reason {
					t.Fatalf("%v, want %s", err, tc.reason)
				}
				return
			}
			if err != nil || math.Abs(*a.RemainingAfter-tc.want) > 1e-12 {
				t.Fatal(a, err)
			}
		})
	}
}

func TestLogTransaction(t *testing.T) {
	for _, failCompletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[failCompletion], func(t *testing.T) {
			s := openTestStore(t)
			id, err := s.Save(ItemSpec{Title: "Series", Kind: model.KindMedia, TotalDuration: model.FloatPtr(40), RemainingDuration: model.FloatPtr(.4), MinSessionDuration: model.FloatPtr(.4)}, true)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.GetItem(id)
			if err != nil {
				t.Fatal(err)
			}
			if failCompletion {
				if _, err = s.db.Exec(`CREATE TRIGGER fail_completion BEFORE INSERT ON events WHEN NEW.type='completed' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.LogTime(id, nil, nil, true, true)
			after, getErr := s.GetItem(id)
			if getErr != nil {
				t.Fatal(getErr)
			}
			events, getErr := s.AllEvents()
			if getErr != nil {
				t.Fatal(getErr)
			}
			if failCompletion {
				if err == nil || !reflect.DeepEqual(before, after) || len(events) != 0 {
					t.Fatal("partial log transaction", err, after, events)
				}
				return
			}
			if err != nil || after.Status != model.StatusDone || *after.RemainingDuration != 0 || *after.TotalDuration != 40 || !result.Completed || len(events) != 2 {
				t.Fatal(result, err, after, events)
			}
			logged, err := s.EventsByType(model.EventLogged)
			if err != nil || len(logged) != 1 || logged[0].Activity == nil || logged[0].Activity.Duration == nil || *logged[0].Activity.Duration != .4 || *logged[0].Activity.RemainingBefore != .4 || *logged[0].Activity.RemainingAfter != 0 {
				t.Fatal(logged, err)
			}
			c, err := s.Content()
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareCostLog(t *testing.T) {
	item := &model.Item{Status: model.StatusInProgress, MinSessionDuration: model.FloatPtr(.5), CostLeft: model.FloatPtr(100)}
	a, err := PrepareLog(item, nil, model.FloatPtr(30), false)
	if err != nil || a.Duration != nil || a.Cost == nil || *a.Cost != 30 || *a.CostBefore != 100 || *a.CostAfter != 70 {
		t.Fatal(a, err)
	}
	item.CostLeft = nil
	if _, err = PrepareLog(item, nil, model.FloatPtr(30), false); err == nil || err.(*LogError).Reason != "cost_unknown" {
		t.Fatal(err)
	}
	item.Ongoing = true
	a, err = PrepareLog(item, nil, model.FloatPtr(30), false)
	if err != nil || a.Duration != nil || a.CostBefore != nil || a.CostAfter != nil {
		t.Fatal(a, err)
	}
	item.Status = model.StatusNotStarted
	if _, err = PrepareLog(item, nil, model.FloatPtr(30), false); err == nil || err.(*LogError).Reason != "not_in_progress" {
		t.Fatal(err)
	}
	if _, err = PrepareLog(item, nil, model.FloatPtr(30), true); err != nil {
		t.Fatal(err)
	}
}

func TestCombinedLogUpdatesTimeAndCostAtomically(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Save(ItemSpec{Title: "Build", Kind: model.KindProject, RemainingDuration: model.FloatPtr(2), CostLeft: model.FloatPtr(100)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionStatus(Start, []string{id}); err != nil {
		t.Fatal(err)
	}
	result, err := s.LogTime(id, model.FloatPtr(.5), model.FloatPtr(30), false, false)
	if err != nil {
		t.Fatal(err)
	}
	it, err := s.GetItem(id)
	if err != nil || *it.RemainingDuration != 1.5 || *it.CostLeft != 70 || result.Activity.Duration == nil || result.Activity.Cost == nil {
		t.Fatal(result, it, err)
	}
	events, err := s.EventsByType(model.EventLogged)
	if err != nil || len(events) != 1 || events[0].Activity == nil || *events[0].Activity.CostAfter != 70 {
		t.Fatal(events, err)
	}
}
