package store

import (
	"errors"
	"testing"

	"dosomething/internal/model"
)

func TestResolveItemRefSuggestsCloseTitles(t *testing.T) {
	st := openTestStore(t)
	bike := addTestItem(t, st, "Fix bike", model.KindProject)
	dune := addTestItem(t, st, "Read Dune", model.KindMedia)
	addTestItem(t, st, "Unrelated chore", model.KindProject)
	for _, tt := range []struct {
		ref  string
		want []string
	}{
		{"fix BIKE", []string{bike}}, // case only
		{"bike", []string{bike}},     // partial
		{"Fx bike", []string{bike}},  // whole-title typo
		{"Dnue", []string{dune}},     // swapped letters
		{"zzz", nil},
	} {
		_, err := st.ResolveItemRef(tt.ref)
		var nf *ItemNotFoundError
		if !errors.As(err, &nf) || !errors.Is(err, ErrInvalidID) {
			t.Fatalf("%q: %v", tt.ref, err)
		}
		got := []string{}
		for _, s := range nf.Suggestions {
			got = append(got, s.ID)
		}
		if len(got) != len(tt.want) || len(got) > 0 && got[0] != tt.want[0] {
			t.Errorf("%q: suggestions %v, want %v", tt.ref, nf.Suggestions, tt.want)
		}
	}
	// Suggestions never resolve: lookup stays case-sensitive.
	if _, err := st.ResolveItemRef("fix bike"); err == nil {
		t.Fatal("case-insensitive title resolved")
	}
}

func TestSimilarTitlesIgnoresCaseAndSpacing(t *testing.T) {
	st := openTestStore(t)
	a := addTestItem(t, st, "Fix bike", model.KindProject)
	b := addTestItem(t, st, "fix  BIKE ", model.KindProject)
	addTestItem(t, st, "Fix bikes", model.KindProject)
	got, err := st.SimilarTitles("Fix bike", a)
	if err != nil || len(got) != 1 || got[0].ID != b {
		t.Fatalf("%v %v", got, err)
	}
}
