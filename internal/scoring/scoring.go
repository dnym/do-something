// Package scoring implements the two-layer ranking engine (plan §5):
//
//	base     = weighted mean of the present groups' desirabilities D_g
//	D_g      = weighted mean of the group members' desirabilities d_p
//	           (an unknown applicable property contributes the prior)
//	rightnow = weighted mean of the present terms' values v_t
//	final    = base                              if no effective right-now term
//	         = (1−β)·base + β·rightnow           otherwise
//
// Right-now terms: urgency (deadline / no_deadline_horizon), momentum
// (status in_progress), unblocking (fraction of active dependents that would
// become ready if the item finished), mode_match (present only when the
// query selects an activity kind: 1 item has it, 0.5 item unclassified, 0
// other kinds only), engagement_match (present only when the query selects
// an engagement style, same value semantics over the item's engagements),
// and cooldown (ALWAYS present: 1 when never anchored,
// ≥ N days fresh, or N ≤ 0; else a linear 0→1 ramp over the most
// recent N days from max(last_suggested, last_started)).
//
// The engine is pure: an item, an event-derived context, the effective
// parameters, and a supplied `now` go in; scores come out. No store access,
// no config-file I/O, no clock reads. Every d and v is clamped to [0,1], so
// base, rightnow and final are bounded by construction.
package scoring

import (
	"errors"
	"fmt"
	"math"
	"time"

	"dosomething/internal/model"
)

// Right-now term names. They are stable contract keys (canonical English).
const (
	TermUrgency         = "urgency"
	TermMomentum        = "momentum"
	TermUnblocking      = "unblocking"
	TermModeMatch       = "mode_match"
	TermEngagementMatch = "engagement_match"
	TermCooldown        = "cooldown"
)

// Stable identifiers for the reasons a member was scored at the prior. The
// output layer maps them to localized prose; JSON carries them verbatim.
const (
	NoteNoRating = "prior_no_rating" // rating property, no rating row
	NoteNoValue  = "prior_no_value"  // cost-like measurement, no stored value
	NoteKUnset   = "prior_k_unset"   // cost-like measurement, k constant unset
	NoteNoCurve  = "prior_no_curve"  // property without a defined curve
)

// hoursPerMonth is the calendar-average month length used to convert rating
// ages to months (matching the tier-1 half-life unit).
const hoursPerMonth = 24 * 365.25 / 12

// Config carries every engine parameter. Missing map entries fall back to the
// neutral defaults listed on the fields; DefaultConfig returns the built-in
// values that mirror the tier-1 configuration.
type Config struct {
	// Beta blends right-now over base. Clamped to [0,1]; 0 ⇒ final = base.
	Beta float64
	// Prior is the desirability of unknown properties. Clamped to [0,1].
	Prior float64
	// CooldownDays is the freshness window. ≤ 0 disables the ramp (always 1).
	CooldownDays float64
	// KPace is the half-saturation pace (hours per day) for project
	// urgency.
	KPace float64
	// DeadlineHorizon is the day-based horizon (days) for the media deadline
	// curve and the project fallback when remaining_duration is unknown. ≤ 0
	// degenerates that curve to the neutral 0.5.
	DeadlineHorizon float64
	// NoDeadlineHorizon restores urgency for items without a deadline by
	// assuming a deadline N days out; 0 disables it.
	NoDeadlineHorizon float64

	// K holds the half-saturation constants for cost-like measurements, keyed
	// by property (remaining_duration, cost_left). A missing key
	// means the constant is unset: the measurement then behaves like unknown
	// (scored at Prior) regardless of its stored value — the deliberately
	// defaultless cost_left.
	K map[model.Property]float64
	// HalfLives holds rating decay half-lives in months, keyed by property.
	// Missing = infinite (never decays); ≤ 0 = fully decayed.
	HalfLives map[model.Property]float64
	// GroupWeights holds per-group weights; missing = 1.0. Negative values
	// are treated as 0.
	GroupWeights map[model.Group]float64
	// PropertyWeights holds per-property weights. Keys may be kind-scoped
	// ("<kind>.<property>") or kind-agnostic ("<property>"); kind-scoped wins.
	// Missing = 1.0; negative values are treated as 0.
	PropertyWeights map[string]float64
	// TermWeights holds right-now term weights, keyed by term name; missing =
	// 1.0; negative values are treated as 0.
	TermWeights map[string]float64
	// Groups holds tier-2 group→property composition, keyed by group then
	// kind. Missing entries fall back to the v1 built-in composition
	// (model.GroupMembers); an explicit empty list makes the group absent.
	Groups map[model.Group]map[model.Kind][]model.Property
}

// DefaultConfig returns the neutral built-in parameters (tier-1 defaults).
func DefaultConfig() Config {
	return Config{
		Beta:              0.2,
		Prior:             0.5,
		CooldownDays:      7,
		KPace:             2,
		DeadlineHorizon:   30,
		NoDeadlineHorizon: 0,
		K: map[model.Property]float64{
			model.PropRemainingDuration: 20,
		},
		HalfLives: map[model.Property]float64{
			model.PropInterest:  18,
			model.PropCareer:    36,
			model.PropPhysical:  36,
			model.PropMental:    36,
			model.PropSocial:    36,
			model.PropActuality: 12,
			model.PropQuality:   math.Inf(1),
			model.PropInfluence: math.Inf(1),
		},
		// mode_match and engagement_match are strong-but-not-absolute: each
		// must be able to lift a matching item above a better-base mismatched
		// one, without turning the axis into a hard filter (weight 0 is the
		// opt-out).
		TermWeights: map[string]float64{TermModeMatch: 2.0, TermEngagementMatch: 2.0},
	}
}

// Context carries the per-item, event-derived facts the engine cannot derive
// from the item row alone.
type Context struct {
	// LastSuggested is the most recent recorded suggestion that returned the
	// item; nil = never suggested (maximally fresh).
	LastSuggested *time.Time
	// LastStarted is the most recent `started` event for the item; nil =
	// never started. It resets cooldown freshness on start.
	LastStarted *time.Time
	// Dependents are the item's direct dependents.
	Dependents []Dependent
	// Mode is the activity kind selected for this query (e.g. --mode
	// thinking); zero = the query carries no mode, and the mode_match term
	// is absent entirely.
	Mode model.Mode
	// Engagement is the engagement style selected for this query (e.g.
	// --engagement focused); zero = the query carries no engagement, and
	// the engagement_match term is absent entirely.
	Engagement model.Engagement
}

// Dependent is one direct dependent of the scored item. Only not_started/in_progress
// dependents are active; a dependent becomes ready when the scored item
// finishes iff OtherOpenDeps == 0, where OtherOpenDeps counts the dependent's
// other dependencies (excluding the scored item) that are not done. Dropped
// prerequisites stay blockers (plan §2.10), so they count as open.
type Dependent struct {
	Status        model.Status
	OtherOpenDeps int
}

// MemberResult is one group member in the breakdown.
type MemberResult struct {
	Property  model.Property      `json:"property"`
	Weight    float64             `json:"weight"`
	D         float64             `json:"d"` // desirability [0,1]
	State     model.PropertyState `json:"state"`
	Value     *float64            `json:"value"` // raw stored value (nil when unknown)
	RatedAt   *time.Time          `json:"rated_at"`
	AgeMonths float64             `json:"age_months"` // rating age in months (0 when undated); ratings only
	Note      string              `json:"note"`       // "" or a Note* identifier explaining a prior score
}

// GroupResult is one present scoring group in the breakdown.
type GroupResult struct {
	Group   model.Group    `json:"group"`
	Weight  float64        `json:"weight"`
	D       float64        `json:"d"`     // group desirability (weighted mean of members)
	Share   float64        `json:"share"` // weight / Σweights — the group's share of base
	Members []MemberResult `json:"members"`
}

// TermResult is one present right-now term in the breakdown.
type TermResult struct {
	Name   string  `json:"name"` // one of the Term* constants
	Weight float64 `json:"weight"`
	V      float64 `json:"v"` // term value [0,1]

	// Numeric explain detail; nil when not applicable.
	DaysLeft  *float64 `json:"days_left"`  // urgency: days until the effective deadline (negative = overdue)
	Pace      *float64 `json:"pace"`       // urgency: project pace in hours/day (known remaining_duration)
	Ready     *int     `json:"ready"`      // unblocking: active dependents ready if the item finished
	Total     *int     `json:"total"`      // unblocking: active dependents
	AnchorAge *float64 `json:"anchor_age"` // cooldown: days since max(last_suggested, last_started)
}

// Result is the full two-layer breakdown for one item.
type Result struct {
	Base     float64       `json:"base"`
	RightNow float64       `json:"right_now"` // 0 when no effective term
	Final    float64       `json:"final"`
	Groups   []GroupResult `json:"groups"` // present groups, in model.AllGroups order
	Terms    []TermResult  `json:"terms"`  // present terms: urgency, momentum, unblocking, cooldown
}

// Score computes the full breakdown for one item.
func Score(item *model.Item, ctx Context, cfg Config, now time.Time) (Result, error) {
	if item == nil {
		return Result{}, errors.New("scoring: nil item")
	}
	if !item.Kind.Valid() {
		return Result{}, fmt.Errorf("scoring: unknown kind %q", item.Kind)
	}
	base, groups := baseScore(item, cfg, now)
	rightnow, terms, effective := rightNowScore(item, ctx, cfg, now)
	final := base
	if effective {
		beta := clamp01(cfg.Beta)
		final = (1-beta)*base + beta*rightnow
	}
	return Result{Base: base, RightNow: rightnow, Final: final, Groups: groups, Terms: terms}, nil
}

// --- base layer ---

// baseScore computes base and the per-group breakdown. A group is present when
// its (effective) member list is non-empty; a present group whose member
// weights sum to 0 keeps D = prior but contributes nothing to base. When no
// group carries weight, base = prior.
func baseScore(item *model.Item, cfg Config, now time.Time) (float64, []GroupResult) {
	prior := clamp01(cfg.Prior)
	groups := make([]GroupResult, 0, len(model.AllGroups))
	num, den := 0.0, 0.0
	for _, g := range model.AllGroups {
		members := groupMembers(item.Kind, g, cfg)
		if item.Ongoing {
			applicable := make([]model.Property, 0, len(members))
			for _, p := range members {
				if p != model.PropRemainingDuration && p != model.PropTotalDuration {
					applicable = append(applicable, p)
				}
			}
			members = applicable
		}
		if len(members) == 0 {
			continue // group absent
		}
		wg := nonNeg(weightOf(cfg.GroupWeights, g, 1.0))
		res := GroupResult{Group: g, Weight: wg}
		dnum, dden := 0.0, 0.0
		for _, p := range members {
			wp := nonNeg(propertyWeight(cfg, item.Kind, p))
			m := memberScore(item, p, wp, cfg, prior, now)
			res.Members = append(res.Members, m)
			dnum += wp * m.D
			dden += wp
		}
		d := prior
		if dden > 0 {
			d = dnum / dden
		}
		res.D = clamp01(d)
		groups = append(groups, res)
		if wg > 0 {
			num += wg * res.D
			den += wg
		}
	}
	base := prior
	if den > 0 {
		base = num / den
	}
	base = clamp01(base)
	for i := range groups {
		if den > 0 {
			groups[i].Share = groups[i].Weight / den
		}
	}
	return base, groups
}

// groupMembers returns the effective member list for a group of a kind: the
// tier-2 composition if given, else the v1 built-in composition.
func groupMembers(kind model.Kind, g model.Group, cfg Config) []model.Property {
	if byKind, ok := cfg.Groups[g]; ok {
		if ps, ok := byKind[kind]; ok {
			return ps
		}
	}
	return model.GroupMembers(kind, g)
}

// costLikeMeasurements are the measurements scored with a k half-saturation
// curve. Any other property placed in a group (e.g. deadline, min_session_duration) has
// no defined curve and scores at the prior.
var costLikeMeasurements = map[model.Property]bool{
	model.PropRemainingDuration: true,
	model.PropCostLeft:          true,
}

// memberScore computes the desirability of one group member.
func memberScore(item *model.Item, p model.Property, weight float64, cfg Config, prior float64, now time.Time) MemberResult {
	m := MemberResult{Property: p, Weight: weight}
	if p.IsRating() {
		r, ok := item.Ratings[p]
		if !ok {
			m.State = model.StateUnknown
			m.D = prior
			m.Note = NoteNoRating
			return m
		}
		raw := r.Value
		m.State = model.StateKnown
		m.Value = &raw
		m.RatedAt = r.RatedAt
		if r.RatedAt != nil {
			if age := now.Sub(*r.RatedAt); age > 0 {
				m.AgeMonths = age.Hours() / hoursPerMonth
			}
		}
		m.D = effectiveRating(r.Value, r.RatedAt, halfLifeOf(cfg, p), now)
		return m
	}
	if costLikeMeasurements[p] {
		v := item.MeasurementValue(p)
		if _, hasK := cfg.K[p]; !hasK {
			// Deliberately defaultless constant (cost_left): until calibrated
			// in the owner's own unit the measurement behaves like unknown —
			// even when a value is stored.
			m.State = item.PropertyStateOf(p)
			m.Value = v
			m.D = prior
			m.Note = NoteKUnset
			return m
		}
		if v == nil {
			m.State = model.StateUnknown
			m.D = prior
			m.Note = NoteNoValue
			return m
		}
		m.State = model.StateKnown
		m.Value = v
		m.D = costLike(cfg.K[p], *v)
		return m
	}
	m.State = item.PropertyStateOf(p)
	m.Value = item.MeasurementValue(p)
	m.D = prior
	m.Note = NoteNoCurve
	return m
}

// halfLifeOf returns the decay half-life (months) for a rating property;
// missing = infinite.
func halfLifeOf(cfg Config, p model.Property) float64 {
	if v, ok := cfg.HalfLives[p]; ok {
		return v
	}
	return math.Inf(1)
}

// effectiveRating applies the age decay: 0.5 + (v−0.5)·2^(−age/halfLife).
// Undated ratings (RatedAt nil, e.g. imported) never decay; an infinite
// half-life never decays; a non-positive half-life fully decays to the
// midpoint; future dates (clock skew) decay by zero.
func effectiveRating(v float64, ratedAt *time.Time, halfLifeMonths float64, now time.Time) float64 {
	v = clamp01(v)
	if ratedAt == nil || math.IsInf(halfLifeMonths, 1) {
		return v
	}
	age := now.Sub(*ratedAt)
	if age <= 0 {
		return v
	}
	if halfLifeMonths <= 0 {
		return 0.5
	}
	decay := math.Pow(2, -(age.Hours()/hoursPerMonth)/halfLifeMonths)
	return clamp01(0.5 + (v-0.5)*decay)
}

// costLike maps a non-negative cost v to desirability with half-saturation k:
// v = 0 ⇒ 1; v → ∞ ⇒ 0; k ≤ 0 ⇒ 0 for any positive v (zero tolerance).
func costLike(k, v float64) float64 {
	if v <= 0 {
		return 1
	}
	if k <= 0 {
		return 0
	}
	return k / (k + v)
}

// --- right-now layer ---

// rightNowScore computes rightnow and the per-term breakdown. Terms are
// appended in canonical order: urgency, momentum, unblocking, cooldown.
// effective=false when no present term carries a positive weight; the caller
// then sets final = base.
func rightNowScore(item *model.Item, ctx Context, cfg Config, now time.Time) (float64, []TermResult, bool) {
	terms := make([]TermResult, 0, 4)
	num, den := 0.0, 0.0
	add := func(name string, v float64, fill func(*TermResult)) {
		t := TermResult{Name: name, Weight: nonNeg(weightOf(cfg.TermWeights, name, 1.0)), V: clamp01(v)}
		if fill != nil {
			fill(&t)
		}
		terms = append(terms, t)
		if t.Weight > 0 {
			num += t.Weight * t.V
			den += t.Weight
		}
	}

	// urgency: present when a deadline is known, or no_deadline_horizon
	// restores "assume N days" for deadline-less items.
	if dl, ok := urgencyDaysLeft(item, cfg, now); ok {
		pace := paceOf(item, dl)
		v := urgencyValue(item, dl, pace, cfg)
		add(TermUrgency, v, func(t *TermResult) {
			d := dl
			t.DaysLeft = &d
			if pace != nil {
				p := *pace
				t.Pace = &p
			}
		})
	}

	// momentum: present iff the item is in progress.
	if item.Status == model.StatusInProgress {
		add(TermMomentum, 1, nil)
	}

	// unblocking: present iff the item has at least one active (not_started or
	// in_progress) dependent.
	ready, active := 0, 0
	for _, d := range ctx.Dependents {
		if d.Status != model.StatusNotStarted && d.Status != model.StatusInProgress {
			continue
		}
		active++
		if d.OtherOpenDeps <= 0 {
			ready++
		}
	}
	if active > 0 {
		add(TermUnblocking, float64(ready)/float64(active), func(t *TermResult) {
			r, a := ready, active
			t.Ready = &r
			t.Total = &a
		})
	}

	// mode_match: present only when the query carries a mode. v = 1 the item
	// carries the selected kind; v = 0.5 it has no modes (unclassified);
	// v = 0 it carries only other kinds. It re-ranks; it never excludes.
	if ctx.Mode != "" {
		v := 0.5
		for _, m := range item.Modes {
			if m == string(ctx.Mode) {
				v = 1
				break
			}
		}
		if v != 1 && len(item.Modes) > 0 {
			v = 0
		}
		add(TermModeMatch, v, nil)
	}

	// engagement_match: present only when the query carries an engagement.
	// v = 1 the item carries the selected engagement; v = 0.5 it has no
	// engagements (unclassified); v = 0 it carries only other engagements.
	// It re-ranks; it never excludes.
	if ctx.Engagement != "" {
		v := 0.5
		for _, e := range item.Engagements {
			if e == string(ctx.Engagement) {
				v = 1
				break
			}
		}
		if v != 1 && len(item.Engagements) > 0 {
			v = 0
		}
		add(TermEngagementMatch, v, nil)
	}

	// cooldown: ALWAYS present.
	age := cooldownAnchorAge(ctx, now)
	v := cooldownValue(age, cfg)
	add(TermCooldown, v, func(t *TermResult) {
		if age != nil {
			a := *age
			t.AnchorAge = &a
		}
	})

	rightnow := 0.0
	if den > 0 {
		rightnow = num / den
	}
	return clamp01(rightnow), terms, den > 0
}

// urgencyDaysLeft returns the days until the effective deadline (negative when
// overdue), or ok=false when the urgency term is absent.
func urgencyDaysLeft(item *model.Item, cfg Config, now time.Time) (float64, bool) {
	if item.Deadline != nil {
		return item.Deadline.Sub(now).Hours() / 24, true
	}
	if cfg.NoDeadlineHorizon > 0 {
		return cfg.NoDeadlineHorizon, true
	}
	return 0, false
}

// paceOf returns the project's remaining pace (hours per day) with the
// day count floored at 1 (overdue ⇒ maximum pressure), or nil when the item
// is not a project or remaining_duration is unknown (day-based fallback applies).
func paceOf(item *model.Item, daysLeft float64) *float64 {
	if item.Kind != model.KindProject || item.RemainingDuration == nil {
		return nil
	}
	d := daysLeft
	if d < 1 {
		d = 1
	}
	p := *item.RemainingDuration / d
	return &p
}

// urgencyValue maps the effective days-left to a term value: projects with a
// known pace use the pace curve; media (and projects without remaining_duration)
// use the day-based curve 1 − clamp(days_left/horizon, 0, 1). A horizon of
// ≤ 0 degenerates the day-based curve to the neutral 0.5.
func urgencyValue(item *model.Item, daysLeft float64, pace *float64, cfg Config) float64 {
	if pace != nil {
		return paceScore(cfg.KPace, *pace)
	}
	if cfg.DeadlineHorizon <= 0 {
		return 0.5
	}
	return 1 - clamp01(daysLeft/cfg.DeadlineHorizon)
}

// paceScore maps pace (hours/day) to urgency with half-saturation kPace:
// pace 0 ⇒ 0; pace → ∞ ⇒ 1; kPace ≤ 0 ⇒ 1 for any positive pace.
func paceScore(kPace, pace float64) float64 {
	if pace <= 0 {
		return 0
	}
	if kPace <= 0 {
		return 1
	}
	return pace / (kPace + pace)
}

// cooldownAnchorAge returns the days since max(last_suggested, last_started),
// or nil when neither exists (never suggested ⇒ maximally fresh).
func cooldownAnchorAge(ctx Context, now time.Time) *float64 {
	var anchor time.Time
	found := false
	if ctx.LastSuggested != nil {
		anchor = *ctx.LastSuggested
		found = true
	}
	if ctx.LastStarted != nil && (!found || ctx.LastStarted.After(anchor)) {
		anchor = *ctx.LastStarted
		found = true
	}
	if !found {
		return nil
	}
	days := now.Sub(anchor).Hours() / 24
	if days < 0 {
		days = 0
	}
	return &days
}

// cooldownValue: 1 when never anchored, ≥ N days fresh, or N ≤ 0 (disabled);
// otherwise a linear 0→1 ramp over the most recent N days.
func cooldownValue(anchorAgeDays *float64, cfg Config) float64 {
	if anchorAgeDays == nil || cfg.CooldownDays <= 0 {
		return 1
	}
	return clamp01(*anchorAgeDays / cfg.CooldownDays)
}

// --- small helpers ---

func weightOf[M ~string](m map[M]float64, key M, def float64) float64 {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

func propertyWeight(cfg Config, kind model.Kind, p model.Property) float64 {
	if v, ok := cfg.PropertyWeights[string(kind)+"."+string(p)]; ok {
		return v
	}
	if v, ok := cfg.PropertyWeights[string(p)]; ok {
		return v
	}
	return 1.0
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0.5
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// nonNeg treats negative and NaN weights as 0.
func nonNeg(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	return v
}
