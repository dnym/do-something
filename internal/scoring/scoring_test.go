package scoring

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"dosomething/internal/model"
)

// testNow is the fixed "now" for all deterministic tests.
var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// hoursPerMonth matches the engine constant; 36 months and 18 months are exact
// integer hour counts (26298 h and 13149 h), which keeps decay tests exact.
const testHoursPerMonth = 24 * 365.25 / 12

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func want(t *testing.T, name string, got, want float64) {
	t.Helper()
	if !almost(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func mkItem(kind model.Kind, mutate ...func(*model.Item)) *model.Item {
	it := &model.Item{ID: "test-id", Title: "T", Kind: kind, Status: model.StatusNotStarted}
	for _, m := range mutate {
		m(it)
	}
	return it
}

// deadline returns a pointer to testNow shifted by days (negative = overdue).
func deadline(days float64) *time.Time {
	tt := testNow.Add(time.Duration(days * 24 * float64(time.Hour)))
	return &tt
}

func fptr(v float64) *float64 { return &v }

func termOf(t *testing.T, res Result, name string) (TermResult, bool) {
	t.Helper()
	for _, tr := range res.Terms {
		if tr.Name == name {
			return tr, true
		}
	}
	return TermResult{}, false
}

// --- base layer ---

// TestBaseAllUnknown pins the neutral all-prior baseline: a bare project with
// no ratings, measurements, deadline, or history scores base = 0.5 and, with
// only the (always-present, maximally fresh) cooldown term, final = 0.6.
func TestBaseAllUnknown(t *testing.T) {
	res, err := Score(mkItem(model.KindProject), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "base", res.Base, 0.5)
	want(t, "rightnow", res.RightNow, 1)
	want(t, "final", res.Final, 0.8*0.5+0.2*1)
	if len(res.Groups) != 3 {
		t.Fatalf("groups = %d, want 3 (value/commitment/interest all present)", len(res.Groups))
	}
	for _, g := range res.Groups {
		want(t, "group D "+string(g.Group), g.D, 0.5)
		for _, m := range g.Members {
			if m.State != model.StateUnknown {
				t.Errorf("%s.%s state = %s, want unknown", g.Group, m.Property, m.State)
			}
		}
	}
	cost := memberOf(t, res, model.GroupCommitment, model.PropCostLeft)
	if cost.Note != NoteKUnset {
		t.Errorf("cost_left note = %q, want %q (k has no default)", cost.Note, NoteKUnset)
	}
	cd, ok := termOf(t, res, TermCooldown)
	if !ok {
		t.Fatal("cooldown term missing (must be always present)")
	}
	want(t, "cooldown v", cd.V, 1) // never suggested ⇒ maximally fresh
	if len(res.Terms) != 1 {
		t.Errorf("terms = %v, want cooldown only", names(res.Terms))
	}
}

// TestFinalEqualsBaseWithZeroTermWeights pins the "no effective right-now
// term ⇒ final = base" edge case: every term is present (the item has a
// deadline, is in progress, has dependents) but all term weights are 0.
func TestFinalEqualsBaseWithZeroTermWeights(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TermWeights = map[string]float64{
		TermUrgency: 0, TermMomentum: 0, TermUnblocking: 0, TermCooldown: 0,
	}
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Deadline = deadline(10)
		i.Status = model.StatusInProgress
	})
	res, err := Score(it, Context{Dependents: []Dependent{{Status: model.StatusNotStarted, OtherOpenDeps: 0}}}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if res.Final != res.Base {
		t.Errorf("final = %v, want base %v", res.Final, res.Base)
	}
	if len(res.Terms) != 4 {
		t.Errorf("terms = %v, want all 4 present", names(res.Terms))
	}
}

// TestUrgencyProject: deadline in 10 days, 20 remaining-duration left ⇒ pace 2 h/day,
// v = 2/(2+2) = 0.5; cooldown fresh ⇒ 1. rightnow = 0.75, final = 0.55.
func TestUrgencyProject(t *testing.T) {
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Deadline = deadline(10)
		i.RemainingDuration = fptr(20)
	})
	res, err := Score(it, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, ok := termOf(t, res, TermUrgency)
	if !ok {
		t.Fatal("urgency term missing")
	}
	want(t, "urgency v", u.V, 0.5)
	if u.DaysLeft == nil || !almost(*u.DaysLeft, 10) {
		t.Errorf("days_left = %v, want 10", u.DaysLeft)
	}
	if u.Pace == nil || !almost(*u.Pace, 2) {
		t.Errorf("pace = %v, want 2", u.Pace)
	}
	want(t, "rightnow", res.RightNow, 0.75)
	want(t, "final", res.Final, 0.55)
}

// TestUrgencyMedia: deadline in 15 days ⇒ v = 1 − 15/30 = 0.5 (same blend as
// the project case).
func TestUrgencyMedia(t *testing.T) {
	it := mkItem(model.KindMedia, func(i *model.Item) {
		i.Deadline = deadline(15)
	})
	res, err := Score(it, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, _ := termOf(t, res, TermUrgency)
	want(t, "urgency v", u.V, 0.5)
	if u.Pace != nil {
		t.Errorf("media pace = %v, want nil (day-based)", *u.Pace)
	}
	want(t, "rightnow", res.RightNow, 0.75)
	want(t, "final", res.Final, 0.55)
}

// TestUrgencyAbsent: no deadline and no_deadline_horizon ⇒ the term is absent
// (cooldown alone keeps right-now = 1, final = 0.6).
func TestUrgencyAbsent(t *testing.T) {
	res, err := Score(mkItem(model.KindProject), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := termOf(t, res, TermUrgency); ok {
		t.Fatal("urgency present without a deadline")
	}
	want(t, "final", res.Final, 0.6)
}

// TestNoDeadlineHorizon: no_deadline_horizon = 14 restores "assume 14 days".
// Media: v = 1 − 14/30 ⇒ final 0.5533…. Project with 20 h left: pace 20/14,
// v = 5/12 ⇒ final 0.5416….
func TestNoDeadlineHorizon(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NoDeadlineHorizon = 14

	media, err := Score(mkItem(model.KindMedia), Context{}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "media final", media.Final, 0.5533333333333333)

	project, err := Score(mkItem(model.KindProject, func(i *model.Item) {
		i.RemainingDuration = fptr(20)
	}), Context{}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "project final", project.Final, 0.5416666666666666)
}

// TestUrgencyRemainingDurationFallback: a project with a deadline but unknown
// remaining_duration falls back to the day-based curve: v = 1 − 10/30 ⇒ final
// 0.4833….
func TestUrgencyRemainingDurationFallback(t *testing.T) {
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Deadline = deadline(10)
	})
	res, err := Score(it, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, _ := termOf(t, res, TermUrgency)
	want(t, "urgency v", u.V, 2.0/3)
	if u.Pace != nil {
		t.Errorf("pace = %v, want nil (fallback)", *u.Pace)
	}
	// rightnow = (2/3 + 1)/2 = 5/6
	want(t, "final", res.Final, 17.0/30)
}

// TestUrgencyOverdue: overdue deadlines pin urgency at (near) 1 — the clamp in
// the media curve and the max(days,1) floor in the project pace both do that.
func TestUrgencyOverdue(t *testing.T) {
	media, err := Score(mkItem(model.KindMedia, func(i *model.Item) {
		i.Deadline = deadline(-5)
	}), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, _ := termOf(t, media, TermUrgency)
	want(t, "media v", u.V, 1)
	want(t, "media final", media.Final, 0.6)

	project, err := Score(mkItem(model.KindProject, func(i *model.Item) {
		i.Deadline = deadline(-5)
		i.RemainingDuration = fptr(10)
	}), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, _ = termOf(t, project, TermUrgency)
	want(t, "project v", u.V, 10.0/12) // pace 10/1
	// base = (0.5 + (2/3+0.5)/2 + 0.5)/3 = 19/36 (whl known ⇒ commitment D = 7/12)
	want(t, "project final", project.Final, 0.8*(19.0/36)+0.2*(11.0/12))
}

// TestMomentum: an in-progress item (suggested at "now", so cooldown = 0)
// gains exactly β/2 from the momentum term over the same not_started item.
func TestMomentum(t *testing.T) {
	mutate := func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{
			model.PropCareer:   {Value: 1, RatedAt: &testNow},
			model.PropPhysical: {Value: 1, RatedAt: &testNow},
			model.PropMental:   {Value: 1, RatedAt: &testNow},
			model.PropSocial:   {Value: 1, RatedAt: &testNow},
			model.PropInterest: {Value: 1, RatedAt: &testNow},
		}
		i.RemainingDuration = fptr(0)
	}
	// base = (1 + 0.75 + 1)/3: value all-1, commitment (whl 0 ⇒ 1, cost k
	// unset ⇒ prior) = 0.75, interest 1.
	base := (1.0 + 0.75 + 1.0) / 3

	not_started, err := Score(mkItem(model.KindProject, mutate), Context{LastSuggested: &testNow}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := termOf(t, not_started, TermMomentum); ok {
		t.Error("momentum present for a not_started item")
	}
	want(t, "not_started final", not_started.Final, (1-0.2)*base)

	progress, err := Score(mkItem(model.KindProject, func(i *model.Item) {
		i.Status = model.StatusInProgress
		mutate(i)
	}), Context{LastSuggested: &testNow}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "in_progress final", progress.Final, (1-0.2)*base+0.2*0.5)
}

// TestUnblocking: only active (not_started/in_progress) dependents count in the
// denominator; a dependent becomes ready iff its other dependencies are all
// done. Terminated dependents neither help nor block.
func TestUnblocking(t *testing.T) {
	// mixed: 2 active (1 ready, 1 not) + 2 terminated ⇒ v = 0.5
	mixed, err := Score(mkItem(model.KindProject), Context{Dependents: []Dependent{
		{Status: model.StatusNotStarted, OtherOpenDeps: 0},
		{Status: model.StatusNotStarted, OtherOpenDeps: 1},
		{Status: model.StatusDone, OtherOpenDeps: 0},
		{Status: model.StatusDropped, OtherOpenDeps: 0},
	}}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, ok := termOf(t, mixed, TermUnblocking)
	if !ok {
		t.Fatal("unblocking term missing")
	}
	want(t, "unblocking v", u.V, 0.5)
	if u.Ready == nil || *u.Ready != 1 || u.Total == nil || *u.Total != 2 {
		t.Errorf("ready/total = %v/%v, want 1/2", u.Ready, u.Total)
	}
	want(t, "final", mixed.Final, 0.55)

	// all terminated ⇒ term absent
	inactive, err := Score(mkItem(model.KindProject), Context{Dependents: []Dependent{
		{Status: model.StatusDone, OtherOpenDeps: 0},
		{Status: model.StatusDropped, OtherOpenDeps: 1},
	}}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := termOf(t, inactive, TermUnblocking); ok {
		t.Error("unblocking present with no active dependents")
	}
	want(t, "final", inactive.Final, 0.6)

	// all active and ready ⇒ v = 1
	all, err := Score(mkItem(model.KindProject), Context{Dependents: []Dependent{
		{Status: model.StatusNotStarted, OtherOpenDeps: 0},
		{Status: model.StatusInProgress, OtherOpenDeps: 0},
	}}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	u, _ = termOf(t, all, TermUnblocking)
	want(t, "unblocking v", u.V, 1)
}

// TestCostLike pins the cost curve and the defaultless-cost rule:
//   - remaining_duration k = 20: 10 h ⇒ d = 2/3
//   - cost_left k = 100: 500 ⇒ d = 1/6
//   - cost_left k unset: any stored value is scored at the prior (known state,
//     prior note)
func TestCostLike(t *testing.T) {
	lenIt := mkItem(model.KindMedia, func(i *model.Item) { i.RemainingDuration = fptr(10) })
	res, err := Score(lenIt, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "base (remaining duration 10h)", res.Base, (0.5+2.0/3+0.5)/3)
	m := memberOf(t, res, model.GroupCommitment, model.PropRemainingDuration)
	want(t, "remaining duration d", m.D, 2.0/3)
	if m.State != model.StateKnown {
		t.Errorf("remaining duration state = %s, want known", m.State)
	}

	cfg := DefaultConfig()
	cfg.K[model.PropCostLeft] = 100
	costIt := mkItem(model.KindProject, func(i *model.Item) { i.CostLeft = fptr(500) })
	res, err = Score(costIt, Context{}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "base (cost k=100 v=500)", res.Base, (0.5+(0.5+1.0/6)/2+0.5)/3)
	m = memberOf(t, res, model.GroupCommitment, model.PropCostLeft)
	want(t, "cost d", m.D, 1.0/6)

	res, err = Score(costIt, Context{}, DefaultConfig(), testNow) // no k.cost_left
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	m = memberOf(t, res, model.GroupCommitment, model.PropCostLeft)
	if m.State != model.StateKnown || m.D != 0.5 || m.Note != NoteKUnset {
		t.Errorf("cost (k unset) = state %s d %v note %q, want known/0.5/%s", m.State, m.D, m.Note, NoteKUnset)
	}
}

// TestDecay pins the rating age curve: 0.5 + (v−0.5)·2^(−age/halfLife), with
// undated ratings and infinite half-lives never decaying.
func TestDecay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GroupWeights = map[model.Group]float64{model.GroupValue: 1} // isolate value
	scoreCareer := func(r model.Rating) float64 {
		it := mkItem(model.KindProject, func(i *model.Item) {
			i.Ratings = map[model.Property]model.Rating{model.PropCareer: r}
		})
		res, err := Score(it, Context{}, cfg, testNow)
		if err != nil {
			t.Fatalf("score: %v", err)
		}
		m := memberOf(t, res, model.GroupValue, model.PropCareer)
		return m.D
	}

	// 36 months old (exact: 26298 h) at half-life 36 ⇒ half the swing left.
	want(t, "36mo decay", scoreCareer(model.Rating{Value: 1, RatedAt: oldTime(36 * testHoursPerMonth)}), 0.75)
	// 18 months old (13149 h) ⇒ 2^(−0.5).
	want(t, "18mo decay", scoreCareer(model.Rating{Value: 1, RatedAt: oldTime(18 * testHoursPerMonth)}),
		0.5+0.5*math.Sqrt(0.5))
	// undated (imported) ⇒ no decay.
	want(t, "undated", scoreCareer(model.Rating{Value: 1}), 1.0)
	// future date (clock skew) ⇒ no negative decay.
	future := testNow.Add(100 * 24 * time.Hour)
	want(t, "future date", scoreCareer(model.Rating{Value: 0, RatedAt: &future}), 0.0)
	// low rating decays toward 0.5 from below.
	want(t, "low rating 18mo", scoreCareer(model.Rating{Value: 0, RatedAt: oldTime(18 * testHoursPerMonth)}),
		0.5-0.5*math.Sqrt(0.5))

	// infinite half-life (quality): a 100-year-old rating is unchanged.
	it := mkItem(model.KindMedia, func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{
			model.PropQuality: {Value: 0.2, RatedAt: oldTime(876600)}, // 100 years
		}
	})
	res, err := Score(it, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	m := memberOf(t, res, model.GroupValue, model.PropQuality)
	want(t, "infinite half-life", m.D, 0.2)
}

// TestSparseProtection: withholding evidence cannot win. An item with one
// excellent rating scores above a fully-rated mediocre item.
func TestSparseProtection(t *testing.T) {
	sparse := mkItem(model.KindProject, func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{model.PropCareer: {Value: 1, RatedAt: &testNow}}
	})
	mediocre := mkItem(model.KindProject, func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{
			model.PropCareer:   {Value: 0.5, RatedAt: &testNow},
			model.PropPhysical: {Value: 0.5, RatedAt: &testNow},
			model.PropMental:   {Value: 0.5, RatedAt: &testNow},
			model.PropSocial:   {Value: 0.5, RatedAt: &testNow},
		}
	})
	rs, err := Score(sparse, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	rm, err := Score(mediocre, Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "sparse base", rs.Base, (5.0/8+0.5+0.5)/3) // value (1+3·0.5)/4 = 0.625
	want(t, "mediocre base", rm.Base, 0.5)
	if rs.Base <= rm.Base {
		t.Errorf("sparse %v did not beat fully-rated mediocre %v", rs.Base, rm.Base)
	}
}

// TestZeroGroupWeights: with no group carrying weight, base falls back to the
// prior regardless of ratings (group D's are still reported for --explain).
func TestZeroGroupWeights(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GroupWeights = map[model.Group]float64{
		model.GroupValue: 0, model.GroupCommitment: 0, model.GroupInterest: 0,
	}
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{
			model.PropCareer: {Value: 1, RatedAt: &testNow},
		}
	})
	res, err := Score(it, Context{}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "base", res.Base, 0.5)
	want(t, "value D (still reported)", res.Groups[0].D, (1.0+3*0.5)/4) // career 1, rest prior
}

// TestCustomComposition: tier-2 group composition is honored — a project's
// value group composed of (quality, interest) scores those two ratings.
func TestCustomComposition(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Groups = map[model.Group]map[model.Kind][]model.Property{
		model.GroupValue: {model.KindProject: {model.PropQuality, model.PropInterest}},
	}
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Ratings = map[model.Property]model.Rating{
			model.PropQuality:  {Value: 1, RatedAt: &testNow},
			model.PropInterest: {Value: 0.2, RatedAt: &testNow},
		}
	})
	res, err := Score(it, Context{}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	g := groupOf(t, res, model.GroupValue)
	props := make([]string, len(g.Members))
	for i, m := range g.Members {
		props[i] = string(m.Property)
	}
	if len(props) != 2 || props[0] != "quality" || props[1] != "interest" {
		t.Fatalf("value members = %v, want [quality interest]", props)
	}
	want(t, "value D", g.D, 0.6)
}

// TestAllTermsPresent pins the canonical term order and a full four-term
// right-now blend: urgency 0.5 (pace 2), momentum 1, unblocking 1, cooldown
// 1/7 ⇒ rightnow 37/56.
func TestAllTermsPresent(t *testing.T) {
	it := mkItem(model.KindProject, func(i *model.Item) {
		i.Status = model.StatusInProgress
		i.Deadline = deadline(5)
		i.RemainingDuration = fptr(10)
	})
	suggested := testNow.Add(-24 * time.Hour)
	res, err := Score(it, Context{
		LastSuggested: &suggested,
		Dependents:    []Dependent{{Status: model.StatusNotStarted, OtherOpenDeps: 0}},
	}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if got := names(res.Terms); len(got) != 4 || got[0] != TermUrgency || got[1] != TermMomentum ||
		got[2] != TermUnblocking || got[3] != TermCooldown {
		t.Fatalf("term order = %v", got)
	}
	u, _ := termOf(t, res, TermUrgency)
	want(t, "urgency", u.V, 0.5)
	mo, _ := termOf(t, res, TermMomentum)
	want(t, "momentum", mo.V, 1)
	ub, _ := termOf(t, res, TermUnblocking)
	want(t, "unblocking", ub.V, 1)
	cd, _ := termOf(t, res, TermCooldown)
	want(t, "cooldown", cd.V, 1.0/7)
	want(t, "rightnow", res.RightNow, 37.0/56)
	want(t, "final", res.Final, 0.8*res.Base+0.2*(37.0/56))
}

// TestModeMatch: mode_match is present only when the query carries a mode.
// v = 1 the item has the selected mode, v = 0.5 it is unclassified, v = 0 it
// has other modes only. Default weight is 2.0 (strong but not absolute);
// weight 0 disables the term. It re-ranks; it never excludes.
func TestModeMatch(t *testing.T) {
	mk := func(modes ...string) *model.Item {
		return mkItem(model.KindProject, func(i *model.Item) { i.Modes = modes })
	}
	// Without a query mode the term is absent entirely, even on a modal item.
	res, err := Score(mk("thinking"), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := termOf(t, res, TermModeMatch); ok {
		t.Error("mode_match must be absent without a query mode")
	}
	want(t, "final (no query mode)", res.Final, 0.8*0.5+0.2*1) // cooldown only

	ctx := Context{Mode: model.ModeThinking}
	run := func(it *model.Item, cfg Config) Result {
		r, e := Score(it, ctx, cfg, testNow)
		if e != nil {
			t.Fatalf("score: %v", e)
		}
		return r
	}
	cases := []struct {
		name  string
		it    *model.Item
		v, rn float64 // term v; right-now blend (weights 2.0 / 1.0)
	}{
		{"match", mk("thinking"), 1, 1},
		{"multi-mode", mk("hands_on", "thinking"), 1, 1},
		{"unclassified", mk(), 0.5, 2.0 / 3},
		{"mismatch", mk("making", "hands_on"), 0, 1.0 / 3},
		{"movement-only mismatch", mk("movement"), 0, 1.0 / 3},
		{"making+thinking match", mk("making", "thinking"), 1, 1},
	}
	for _, c := range cases {
		res := run(c.it, DefaultConfig())
		m, ok := termOf(t, res, TermModeMatch)
		if !ok {
			t.Fatalf("%s: mode_match term missing", c.name)
		}
		want(t, c.name+" v", m.V, c.v)
		want(t, c.name+" weight", m.Weight, 2.0)
		want(t, c.name+" rightnow", res.RightNow, c.rn)
		want(t, c.name+" final", res.Final, 0.8*0.5+0.2*c.rn)
		if got := names(res.Terms); len(got) != 2 || got[0] != TermModeMatch || got[1] != TermCooldown {
			t.Errorf("%s: term order = %v, want [mode_match cooldown]", c.name, got)
		}
	}
	// Strong-but-not-absolute: match > unclassified > mismatch.
	ranks := map[string]float64{}
	for _, c := range cases {
		ranks[c.name] = run(c.it, DefaultConfig()).Final
	}
	if !(ranks["match"] > ranks["unclassified"] && ranks["unclassified"] > ranks["mismatch"]) {
		t.Errorf("ranking = %v, want match > unclassified > mismatch", ranks)
	}
	// Weight 0 disables the term: every item lands on the pure cooldown blend.
	cfg := DefaultConfig()
	cfg.TermWeights = map[string]float64{TermModeMatch: 0}
	for _, c := range cases {
		res := run(c.it, cfg)
		want(t, c.name+" rightnow (w=0)", res.RightNow, 1)
		want(t, c.name+" final (w=0)", res.Final, 0.8*0.5+0.2*1)
	}
	// A higher base still beats a mode match: the term re-ranks, it does not
	// exclude. A mismatched item with a strong base outranks a matched item
	// with a weak one (both fresh).
	strong := mkItem(model.KindProject, func(i *model.Item) {
		i.Modes = []string{"movement"}
		i.Ratings = map[model.Property]model.Rating{model.PropInterest: {Value: 1, RatedAt: &testNow}}
	})
	weak := mkItem(model.KindProject, func(i *model.Item) {
		i.Modes = []string{"thinking"}
		i.Ratings = map[model.Property]model.Rating{model.PropInterest: {Value: 0, RatedAt: &testNow}}
	})
	r, e := Score(strong, ctx, DefaultConfig(), testNow)
	if e != nil {
		t.Fatal(e)
	}
	w, e := Score(weak, ctx, DefaultConfig(), testNow)
	if e != nil {
		t.Fatal(e)
	}
	if !(r.Final > w.Final) {
		t.Errorf("strong mismatched (%v) must beat weak matched (%v): base outweighs a 2.0-weighted term", r.Final, w.Final)
	}
}

// TestEngagementMatch: engagement_match mirrors mode_match exactly over the
// item's engagement styles: present only when the query carries an
// engagement, v = 1 / 0.5 / 0 for fit / unclassified / other-only. Default
// weight 2.0; weight 0 disables the term.
func TestEngagementMatch(t *testing.T) {
	mk := func(engagements ...string) *model.Item {
		return mkItem(model.KindProject, func(i *model.Item) { i.Engagements = engagements })
	}
	// Without a query engagement the term is absent entirely.
	res, err := Score(mk("focused"), Context{}, DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := termOf(t, res, TermEngagementMatch); ok {
		t.Error("engagement_match must be absent without a query engagement")
	}

	ctx := Context{Engagement: model.EngagementFocused}
	run := func(it *model.Item, cfg Config) Result {
		r, e := Score(it, ctx, cfg, testNow)
		if e != nil {
			t.Fatalf("score: %v", e)
		}
		return r
	}
	cases := []struct {
		name  string
		it    *model.Item
		v, rn float64 // term v; right-now blend (weights 2.0 / 1.0)
	}{
		{"match", mk("focused"), 1, 1},
		{"multi", mk("loose", "focused"), 1, 1},
		{"unclassified", mk(), 0.5, 2.0 / 3},
		{"mismatch", mk("loose"), 0, 1.0 / 3},
	}
	for _, c := range cases {
		res := run(c.it, DefaultConfig())
		m, ok := termOf(t, res, TermEngagementMatch)
		if !ok {
			t.Fatalf("%s: engagement_match term missing", c.name)
		}
		want(t, c.name+" v", m.V, c.v)
		want(t, c.name+" weight", m.Weight, 2.0)
		want(t, c.name+" rightnow", res.RightNow, c.rn)
		want(t, c.name+" final", res.Final, 0.8*0.5+0.2*c.rn)
		if got := names(res.Terms); len(got) != 2 || got[0] != TermEngagementMatch || got[1] != TermCooldown {
			t.Errorf("%s: term order = %v, want [engagement_match cooldown]", c.name, got)
		}
	}
	// Weight 0 disables the term: every item lands on the pure cooldown blend.
	cfg := DefaultConfig()
	cfg.TermWeights = map[string]float64{TermEngagementMatch: 0}
	for _, c := range cases {
		res := run(c.it, cfg)
		want(t, c.name+" rightnow (w=0)", res.RightNow, 1)
		want(t, c.name+" final (w=0)", res.Final, 0.8*0.5+0.2*1)
	}
}

// TestModeEngagementBlend: the two axes are independent terms that blend in
// the same right-now layer with cooldown: one flag gives (2v + 1)/3, both
// flags give (2v_mode + 2v_eng + 1)/5.
func TestModeEngagementBlend(t *testing.T) {
	mk := func(modes, engagements []string) *model.Item {
		return mkItem(model.KindProject, func(i *model.Item) {
			i.Modes = modes
			i.Engagements = engagements
		})
	}
	ctx := Context{Mode: model.ModeThinking, Engagement: model.EngagementFocused}
	run := func(it *model.Item) Result {
		r, e := Score(it, ctx, DefaultConfig(), testNow)
		if e != nil {
			t.Fatalf("score: %v", e)
		}
		return r
	}
	f := func(modes, engagements []string) *model.Item { return mk(modes, engagements) }
	cases := []struct {
		name string
		it   *model.Item
		rn   float64
	}{
		{"both fit", f([]string{"thinking"}, []string{"focused"}), 1},
		{"mode fit, engagement mismatch", f([]string{"thinking"}, []string{"loose"}), 0.6},
		{"mode mismatch, engagement fit", f([]string{"movement"}, []string{"focused"}), 0.6},
		{"both mismatch", f([]string{"movement"}, []string{"loose"}), 0.2},
		{"both unclassified", f(nil, nil), 0.6},
	}
	for _, c := range cases {
		res := run(c.it)
		want(t, c.name+" rightnow", res.RightNow, c.rn)
		want(t, c.name+" final", res.Final, 0.8*0.5+0.2*c.rn)
		if got := names(res.Terms); len(got) != 3 || got[0] != TermModeMatch || got[1] != TermEngagementMatch || got[2] != TermCooldown {
			t.Errorf("%s: term order = %v, want [mode_match engagement_match cooldown]", c.name, got)
		}
	}
}

// TestCooldownResetOnStarted: the cooldown anchor is max(last_suggested,
// last_started) — starting an item makes it fresh again even if it was
// suggested more recently.
func TestCooldownResetOnStarted(t *testing.T) {
	suggested3d := testNow.Add(-72 * time.Hour)
	cfg := DefaultConfig() // cooldown 7 days

	s, err := Score(mkItem(model.KindProject), Context{LastSuggested: &suggested3d}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "final (suggested 3d ago)", s.Final, 0.4+0.2*3.0/7)

	started1d := testNow.Add(-24 * time.Hour)
	b, err := Score(mkItem(model.KindProject), Context{LastSuggested: &suggested3d, LastStarted: &started1d}, cfg, testNow)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	want(t, "final (started 1d ago)", b.Final, 0.4+0.2*1.0/7)
}

// TestMonotonicRatings: raising a (fresh) rating never lowers the final score.
func TestMonotonicRatings(t *testing.T) {
	mk := func(v float64) float64 {
		it := mkItem(model.KindProject, func(i *model.Item) {
			i.Ratings = map[model.Property]model.Rating{model.PropInterest: {Value: v, RatedAt: &testNow}}
		})
		res, err := Score(it, Context{}, DefaultConfig(), testNow)
		if err != nil {
			t.Fatalf("score: %v", err)
		}
		return res.Final
	}
	if mk(0.8) <= mk(0.2) {
		t.Errorf("raising interest %v → %v did not raise final", mk(0.2), mk(0.8))
	}
}

// TestScoreErrors: nil item and unknown kind are rejected.
func TestScoreErrors(t *testing.T) {
	if _, err := Score(nil, Context{}, DefaultConfig(), testNow); err == nil {
		t.Error("nil item: want error")
	}
	if _, err := Score(mkItem(model.Kind("bogus")), Context{}, DefaultConfig(), testNow); err == nil {
		t.Error("invalid kind: want error")
	}
}

// --- boundedness fuzz ---

// TestBoundedness: across randomized items, contexts, and (deliberately
// degenerate) configs, every d, D, v, and the three summary scores stay in
// [0,1] — bounded by construction.
func TestBoundedness(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 3000; i++ {
		cfg := fuzzConfig(rng)
		item := fuzzItem(rng)
		ctx := fuzzContext(rng)
		now := testNow.Add(time.Duration(rng.Intn(10000)) * time.Minute)
		res, err := Score(item, ctx, cfg, now)
		if err != nil {
			t.Fatalf("iter %d: score: %v", i, err)
		}
		assertBounded(t, i, res)
	}
}

func assertBounded(t *testing.T, iter int, r Result) {
	t.Helper()
	check := func(name string, v float64) {
		if math.IsNaN(v) || v < -1e-12 || v > 1+1e-12 {
			t.Fatalf("iter %d: %s = %v out of [0,1]", iter, name, v)
		}
	}
	check("base", r.Base)
	check("rightnow", r.RightNow)
	check("final", r.Final)
	for _, g := range r.Groups {
		check("group D "+string(g.Group), g.D)
		for _, m := range g.Members {
			check("member d "+string(m.Property), m.D)
		}
	}
	for _, tr := range r.Terms {
		check("term v "+tr.Name, tr.V)
	}
}

var fuzzVals = []float64{-2, -0.5, 0, 0.25, 1, 3, 30, 200}

func fuzzConfig(rng *rand.Rand) Config {
	cfg := DefaultConfig()
	val := func() float64 { return fuzzVals[rng.Intn(len(fuzzVals))] }
	if rng.Intn(3) == 0 {
		cfg.Beta = val()
	}
	if rng.Intn(3) == 0 {
		cfg.Prior = val()
	}
	if rng.Intn(4) == 0 {
		cfg.CooldownDays = val()
	}
	if rng.Intn(4) == 0 {
		cfg.KPace = val()
	}
	if rng.Intn(4) == 0 {
		cfg.DeadlineHorizon = val()
	}
	if rng.Intn(4) == 0 {
		cfg.NoDeadlineHorizon = val()
	}
	if rng.Intn(3) == 0 {
		cfg.K = map[model.Property]float64{}
		for _, p := range []model.Property{model.PropRemainingDuration, model.PropTotalDuration, model.PropCostLeft} {
			if rng.Intn(2) == 0 {
				cfg.K[p] = val()
			}
		}
	}
	if rng.Intn(4) == 0 {
		cfg.HalfLives = map[model.Property]float64{}
		for _, p := range []model.Property{model.PropInterest, model.PropCareer, model.PropPhysical, model.PropMental, model.PropSocial, model.PropActuality} {
			cfg.HalfLives[p] = val()
		}
		if rng.Intn(2) == 0 {
			cfg.HalfLives[model.PropQuality] = math.Inf(1)
		}
	}
	if rng.Intn(5) == 0 {
		cfg.GroupWeights = map[model.Group]float64{}
		for _, g := range model.AllGroups {
			cfg.GroupWeights[g] = val()
		}
	}
	if rng.Intn(5) == 0 {
		cfg.PropertyWeights = map[string]float64{}
		for _, p := range model.AllProperties() {
			if rng.Intn(2) == 0 {
				cfg.PropertyWeights[string(model.KindProject)+"."+string(p)] = val()
			}
		}
	}
	if rng.Intn(5) == 0 {
		cfg.TermWeights = map[string]float64{}
		for _, name := range []string{TermUrgency, TermMomentum, TermUnblocking, TermCooldown} {
			cfg.TermWeights[name] = val()
		}
	}
	if rng.Intn(6) == 0 {
		cfg.Groups = map[model.Group]map[model.Kind][]model.Property{
			model.GroupValue: {model.KindProject: {model.PropQuality, model.PropInterest}},
		}
	}
	return cfg
}

func fuzzItem(rng *rand.Rand) *model.Item {
	kind := model.AllKinds[rng.Intn(len(model.AllKinds))]
	it := &model.Item{
		ID: "fuzz", Title: "F", Kind: kind,
		Status:  model.AllStatuses[rng.Intn(len(model.AllStatuses))],
		Ratings: map[model.Property]model.Rating{},
	}
	for _, p := range model.RatingProperties(kind) {
		if rng.Intn(2) != 0 {
			continue
		}
		v := []float64{0, 0.25, 0.5, 0.75, 1}[rng.Intn(5)]
		r := model.Rating{Value: v}
		switch rng.Intn(4) {
		case 0: // undated
		case 1:
			tt := testNow.Add(-time.Duration(rng.Intn(5000)) * 24 * time.Hour)
			r.RatedAt = &tt
		case 2: // future date (clock skew)
			tt := testNow.Add(time.Duration(rng.Intn(200)) * 24 * time.Hour)
			r.RatedAt = &tt
		default:
			tt := testNow
			r.RatedAt = &tt
		}
		it.Ratings[p] = r
	}
	meas := func() *float64 {
		if rng.Intn(3) != 0 {
			return nil
		}
		v := []float64{0, 0.5, 20, 1e6}[rng.Intn(4)]
		return &v
	}
	it.RemainingDuration = meas()
	it.TotalDuration = meas()
	it.CostLeft = meas()
	if rng.Intn(2) == 0 {
		tt := testNow.Add(time.Duration(rng.Intn(121)-60) * 24 * time.Hour)
		it.Deadline = &tt
	}
	return it
}

func fuzzContext(rng *rand.Rand) Context {
	var ctx Context
	if rng.Intn(2) == 0 {
		tt := testNow.Add(-time.Duration(rng.Intn(30)) * 24 * time.Hour)
		ctx.LastSuggested = &tt
	}
	if rng.Intn(2) == 0 {
		tt := testNow.Add(-time.Duration(rng.Intn(30)) * 24 * time.Hour)
		ctx.LastStarted = &tt
	}
	for i := 0; i < rng.Intn(4); i++ {
		ctx.Dependents = append(ctx.Dependents, Dependent{
			Status:        model.AllStatuses[rng.Intn(len(model.AllStatuses))],
			OtherOpenDeps: rng.Intn(4),
		})
	}
	return ctx
}

// --- test helpers ---

func oldTime(hours float64) *time.Time {
	tt := testNow.Add(-time.Duration(hours) * time.Hour)
	return &tt
}

func memberOf(t *testing.T, res Result, g model.Group, p model.Property) MemberResult {
	t.Helper()
	for _, mr := range groupOf(t, res, g).Members {
		if mr.Property == p {
			return mr
		}
	}
	t.Fatalf("member %s not in group %s", p, g)
	return MemberResult{}
}

func groupOf(t *testing.T, res Result, g model.Group) GroupResult {
	t.Helper()
	for _, gr := range res.Groups {
		if gr.Group == g {
			return gr
		}
	}
	t.Fatalf("group %s not in result", g)
	return GroupResult{}
}

func names(terms []TermResult) []string {
	out := make([]string, len(terms))
	for i, tr := range terms {
		out[i] = tr.Name
	}
	return out
}
