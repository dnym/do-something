package store

import (
	"errors"
	"testing"
	"time"

	"dosomething/internal/model"
)

func TestSnoozeSetClearAndPreservedByEdit(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "A", model.KindProject)
	done := addTestItem(t, st, "Done", model.KindProject)
	verb, _ := TransitionOf("done")
	if _, err := st.TransitionStatus(verb, []string{done}); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	// All-or-nothing: a finished item aborts the whole command.
	var se *SnoozeError
	if _, err := st.Snooze([]string{a, done}, &until); !errors.As(err, &se) || se.Problems[0].Code != "INVALID_STATE" {
		t.Fatal(err)
	}
	if it, _ := st.GetItem(a); it.SnoozedUntil != nil {
		t.Fatal("failed snooze wrote")
	}
	res, err := st.Snooze([]string{a}, &until)
	if err != nil || !res[0].Changed {
		t.Fatal(res, err)
	}
	notes := "edited"
	if _, err := st.Save(ItemSpec{ID: a, Notes: &notes}, false); err != nil {
		t.Fatal(err)
	}
	it, _ := st.GetItem(a)
	if it.SnoozedUntil == nil || !it.SnoozedUntil.Equal(until) || !it.Snoozed(until.Add(-time.Second)) || it.Snoozed(until) {
		t.Fatalf("snooze after edit: %v", it.SnoozedUntil)
	}
	if res, _ := st.Snooze([]string{a}, &until); res[0].Changed {
		t.Fatal("same snooze reported a change")
	}
	// Clearing works for any status, and a cleared item is a no-op.
	if res, err := st.Snooze([]string{a, done}, nil); err != nil || !res[0].Changed || res[1].Changed {
		t.Fatal(res, err)
	}
	c, _ := st.Content()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
