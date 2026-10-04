// Package model defines the unified data model for do-something: items (the
// single list), their kind-scoped ratings, measurements, tags, dependencies,
// and the append-only event log. It also fixes the property/group taxonomy that
// the scoring engine and the output contract both depend on, so that one stable
// key set is used everywhere.
package model

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Kind classifies an item.
type Kind string

const (
	KindProject Kind = "project"
	KindMedia   Kind = "media"
)

// AllKinds lists every kind, in stable order.
var AllKinds = []Kind{KindProject, KindMedia}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindProject, KindMedia:
		return true
	}
	return false
}

// Status is an item's lifecycle state.
type Status string

const (
	StatusNotStarted Status = "not_started"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusDropped    Status = "dropped"
)

// AllStatuses lists every status, in stable order.
var AllStatuses = []Status{StatusNotStarted, StatusInProgress, StatusDone, StatusDropped}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusNotStarted, StatusInProgress, StatusDone, StatusDropped:
		return true
	}
	return false
}

// Mode is a built-in activity kind: what the item is, by how it is engaged.
// Like status, it is interface vocabulary, not a rating: an item may carry
// several modes, and an item without modes is simply unclassified. The set is
// fixed in v1 (custom modes are a v2 candidate).
type Mode string

const (
	ModeMovement Mode = "movement" // movement and exercise
	ModeHandsOn  Mode = "hands_on" // hands-on work: building, crafting, repairing, maintaining
	ModeThinking Mode = "thinking" // head work on existing material: study, analysis, reading, puzzles
	ModeMaking   Mode = "making"   // the artifact drive: bringing something unfinished into the world, by hand or by mind
)

// AllModes lists every built-in mode, in stable order.
var AllModes = []Mode{ModeMovement, ModeHandsOn, ModeThinking, ModeMaking}

// Valid reports whether m is a built-in mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeMovement, ModeHandsOn, ModeThinking, ModeMaking:
		return true
	}
	return false
}

// Engagement is a built-in engagement style: how the item is suited to being
// engaged with, independent of its kind (focused study or loose reading, a
// technical workout or an unhurried walk). Like Mode it is interface
// vocabulary, not a rating: an item may carry several, and an item without
// engagements is unclassified. The set is fixed in v1.
type Engagement string

const (
	EngagementFocused Engagement = "focused" // sustained, relatively undivided engagement
	EngagementLoose   Engagement = "loose"   // relaxed, intermittent, low-demand engagement
)

// AllEngagements lists every built-in engagement, in stable order.
var AllEngagements = []Engagement{EngagementFocused, EngagementLoose}

// Valid reports whether e is a built-in engagement.
func (e Engagement) Valid() bool {
	switch e {
	case EngagementFocused, EngagementLoose:
		return true
	}
	return false
}

// RatingLevel is a canonical label for an ordinal 0..1 rating. Like kind,
// status, and mode it is interface vocabulary, not user data: a stored rating
// is always a plain 0..1 fraction, and a level just names one of the six
// canonical anchor fractions. The level anchors line up with the legacy import
// scales (0, 1/3, 2/3, 1 are the 0–3 anchors; 4/5 the 0–5 media anchor), so
// ported data lands exactly on labels where the scales agree.
type RatingLevel string

const (
	LevelNone     RatingLevel = "none"      // 0
	LevelLow      RatingLevel = "low"       // 1/3
	LevelMedium   RatingLevel = "medium"    // 1/2
	LevelHigh     RatingLevel = "high"      // 2/3
	LevelVeryHigh RatingLevel = "very_high" // 4/5
	LevelMax      RatingLevel = "max"       // 1
)

// AllLevels lists every rating level, in ascending value order.
var AllLevels = []RatingLevel{LevelNone, LevelLow, LevelMedium, LevelHigh, LevelVeryHigh, LevelMax}

var levelValues = map[RatingLevel]float64{
	LevelNone:     0,
	LevelLow:      1.0 / 3.0,
	LevelMedium:   1.0 / 2.0,
	LevelHigh:     2.0 / 3.0,
	LevelVeryHigh: 4.0 / 5.0,
	LevelMax:      1,
}

// Valid reports whether l is a known rating level.
func (l RatingLevel) Valid() bool {
	_, ok := levelValues[l]
	return ok
}

// Value returns the canonical fraction the level names.
func (l RatingLevel) Value() float64 { return levelValues[l] }

// ParseRatingLevel looks up a level by its canonical name.
func ParseRatingLevel(s string) (RatingLevel, bool) {
	l := RatingLevel(s)
	return l, l.Valid()
}

// LevelForValue returns the level that names v exactly, or "" when v is not a
// canonical anchor fraction. Used to display stored ratings as their label.
func LevelForValue(v float64) RatingLevel {
	for _, l := range AllLevels {
		if levelValues[l] == v {
			return l
		}
	}
	return ""
}

// Property is a canonical property key: a 0..1 ordinal rating or a numeric
// measurement. The full union of these keys is the output contract's
// property_states key set (see the plan, §7).
type Property string

const (
	// Ordinal ratings (0..1), stored in the ratings table with a per-row rated_at.
	PropIntensity Property = "intensity"
	PropCareer    Property = "career"
	PropPhysical  Property = "physical"
	PropMental    Property = "mental"
	PropSocial    Property = "social"
	PropInterest  Property = "interest"
	PropActuality Property = "actuality"
	PropInfluence Property = "influence"
	PropQuality   Property = "quality"

	// Numeric measurements stored as columns on items.
	PropRemainingDuration  Property = "remaining_duration"
	PropCostLeft           Property = "cost_left"
	PropMinSessionDuration Property = "min_session_duration"
	PropTotalDuration      Property = "total_duration"
	PropDeadline           Property = "deadline"
)

// ratingProperties is the set of 0..1 ordinal ratings.
var ratingProperties = map[Property]bool{
	PropIntensity: true, PropCareer: true, PropPhysical: true, PropMental: true,
	PropSocial: true, PropInterest: true, PropActuality: true, PropInfluence: true,
	PropQuality: true,
}

// IsRating reports whether p is a 0..1 ordinal rating (as opposed to a numeric
// measurement).
func (p Property) IsRating() bool { return ratingProperties[p] }

// AllProperties returns the full union key set used by the output contract, in a
// stable order (ratings first, then measurements).
func AllProperties() []Property {
	return []Property{
		PropIntensity, PropCareer, PropPhysical, PropMental, PropSocial,
		PropInterest, PropActuality, PropInfluence, PropQuality,
		PropRemainingDuration, PropCostLeft, PropMinSessionDuration, PropTotalDuration, PropDeadline,
	}
}

// RatingProperties returns the ordinal-rating properties applicable to a kind.
func RatingProperties(kind Kind) []Property {
	switch kind {
	case KindProject:
		return []Property{PropIntensity, PropCareer, PropPhysical, PropMental, PropSocial, PropInterest}
	case KindMedia:
		return []Property{PropIntensity, PropActuality, PropInfluence, PropQuality, PropInterest}
	}
	return nil
}

// MeasurementProperties returns the numeric-measurement properties applicable to
// a kind (deadline applies to both kinds).
func MeasurementProperties(kind Kind) []Property {
	switch kind {
	case KindProject, KindMedia:
		return []Property{PropRemainingDuration, PropCostLeft, PropMinSessionDuration, PropTotalDuration, PropDeadline}
	}
	return nil
}

// ApplicableProperty reports whether p is applicable to kind at all (rating or
// measurement).
func (kind Kind) ApplicableProperty(p Property) bool {
	for _, r := range RatingProperties(kind) {
		if r == p {
			return true
		}
	}
	for _, m := range MeasurementProperties(kind) {
		if m == p {
			return true
		}
	}
	return false
}

// Group is a conceptual scoring group. Projects and media share the same three
// top-level dimensions so one mixed ranking list is structurally meaningful.
type Group string

const (
	GroupValue      Group = "value"
	GroupCommitment Group = "commitment"
	GroupInterest   Group = "interest"
)

// AllGroups lists the scoring groups in stable order.
var AllGroups = []Group{GroupValue, GroupCommitment, GroupInterest}

// GroupMembers returns the properties that compose a group for a kind. A group
// with no applicable members for a kind is absent from scoring.
func GroupMembers(kind Kind, g Group) []Property {
	switch g {
	case GroupValue:
		switch kind {
		case KindProject:
			return []Property{PropCareer, PropPhysical, PropMental, PropSocial}
		case KindMedia:
			return []Property{PropQuality, PropInfluence, PropActuality}
		}
	case GroupCommitment:
		switch kind {
		case KindProject:
			return []Property{PropRemainingDuration, PropCostLeft}
		case KindMedia:
			return []Property{PropRemainingDuration}
		}
	case GroupInterest:
		return []Property{PropInterest}
	}
	return nil
}

// PropertyState classifies a property for output.
type PropertyState string

const (
	// StateKnown: the property has a value / rating row.
	StateKnown PropertyState = "known"
	// StateUnknown: applicable to the kind but no value / rating row → scored at
	// the prior, labelled "unknown" (never "not_applicable").
	StateUnknown PropertyState = "unknown"
	// StateNotApplicable: not applicable to the item's kind (e.g. a media item
	// has no career rating). Value is always null.
	StateNotApplicable PropertyState = "not_applicable"
)

// EventType is an event kind.
type EventType string

const (
	EventSuggested EventType = "suggested"
	EventStarted   EventType = "started"
	EventCompleted EventType = "completed"
	EventDropped   EventType = "dropped"
	EventLogged    EventType = "logged"
)

// AllEventTypes lists every event type, in stable order.
var AllEventTypes = []EventType{EventSuggested, EventStarted, EventCompleted, EventDropped, EventLogged}

// Rating is one 0..1 ordinal rating for a property of an item.
type Rating struct {
	Property Property   `json:"property"`
	Value    float64    `json:"value"`
	RatedAt  *time.Time `json:"rated_at"` // nil = undated (e.g. imported) → no decay
}

// Item is one row of the unified list. Ratings and tags are the normalized
// children; measurements are nullable columns.
type Item struct {
	Ongoing bool   `json:"ongoing"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    Kind   `json:"kind"`
	Status  Status `json:"status"`

	LegacyID *int64  `json:"legacy_id"`
	Category *string `json:"category"`
	Type     *string `json:"type"`
	Notes    *string `json:"notes"`
	URL      *string `json:"url"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Deadline           *time.Time `json:"deadline"`
	RemainingDuration  *float64   `json:"remaining_duration"` // active hours
	CostLeft           *float64   `json:"cost_left"`
	MinSessionDuration *float64   `json:"min_session_duration"` // hours
	TotalDuration      *float64   `json:"total_duration"`       // active hours

	Ratings map[Property]Rating `json:"ratings"`
	Tags    []string            `json:"tags"`
	// Modes are the built-in activity kinds the item carries (see Mode). An
	// empty list is unclassified, not "nothing".
	Modes []string `json:"modes"`
	// Engagements are the built-in engagement styles the item carries (see
	// Engagement). An empty list is unclassified, not "nothing".
	Engagements []string `json:"engagements"`

	// Dependencies are the ids this item depends on (its prerequisites).
	Dependencies []string `json:"dependencies"`
}

// RatingValue returns the effective value for a rating property, or nil if there
// is no rating row (unknown).
func (it *Item) RatingValue(p Property) *float64 {
	if it.Ratings == nil {
		return nil
	}
	if r, ok := it.Ratings[p]; ok {
		v := r.Value
		return &v
	}
	return nil
}

// MeasurementValue returns the stored numeric value for a measurement property,
// or nil if unset.
func (it *Item) MeasurementValue(p Property) *float64 {
	switch p {
	case PropRemainingDuration:
		return it.RemainingDuration
	case PropCostLeft:
		return it.CostLeft
	case PropMinSessionDuration:
		return it.MinSessionDuration
	case PropTotalDuration:
		return it.TotalDuration
	}
	return nil
}

// PropertyStateOf classifies p for this item (three-state missingness).
func (it *Item) PropertyStateOf(p Property) PropertyState {
	if it.Ongoing && (p == PropTotalDuration || p == PropRemainingDuration) {
		return StateNotApplicable
	}
	if !it.Kind.ApplicableProperty(p) {
		return StateNotApplicable
	}
	if p.IsRating() {
		if _, ok := it.Ratings[p]; ok {
			return StateKnown
		}
		return StateUnknown
	}
	switch p {
	case PropDeadline:
		if it.Deadline != nil {
			return StateKnown
		}
		return StateUnknown
	default:
		if it.MeasurementValue(p) != nil {
			return StateKnown
		}
		return StateUnknown
	}
}

// SortTags returns a copy of the tags in sorted order.
func (it *Item) SortedTags() []string {
	out := make([]string, len(it.Tags))
	copy(out, it.Tags)
	sort.Strings(out)
	return out
}

// Event is one append-only log row. ItemID is opaque historical attribution with
// no foreign key: it may reference an item that no longer exists locally after a
// keep-local/take-remote, which is what makes an unconditional event union safe.
type Event struct {
	Activity *LoggedActivity `json:"activity"`
	ID       string          `json:"id"`
	ItemID   *string         `json:"item_id"`
	ItemIDs  []string        `json:"item_i_ds"` // JSON array of returned candidate ids (suggested only)
	Type     EventType       `json:"type"`
	At       time.Time       `json:"at"`
	Query    *string         `json:"query"` // JSON of the interpreted filter (suggested only)
}

// KeyedDependency is one edge in the dependency graph: item depends on target.
type KeyedDependency struct {
	ItemID    string `json:"item_id"`
	DependsOn string `json:"depends_on"`
}

// Validate performs lightweight in-memory checks on an item. It does not
// enforce cross-item invariants (cycles, referential integrity) — those live in
// the store.
func (it *Item) Validate() error {
	if it.Ongoing && (it.TotalDuration != nil || it.RemainingDuration != nil) {
		return &OngoingDurationError{}
	}
	if it.ID == "" {
		return fmt.Errorf("item: empty id")
	}
	if it.Title == "" {
		return fmt.Errorf("item %s: empty title", shortID(it.ID))
	}
	if !it.Kind.Valid() {
		return fmt.Errorf("item %s: invalid kind %q", shortID(it.ID), it.Kind)
	}
	if !it.Status.Valid() {
		return fmt.Errorf("item %s: invalid status %q", shortID(it.ID), it.Status)
	}
	for p, r := range it.Ratings {
		if !p.IsRating() || !it.Kind.ApplicableProperty(p) || math.IsNaN(r.Value) || math.IsInf(r.Value, 0) || r.Value < 0 || r.Value > 1 {
			return fmt.Errorf("item %s: rating %s out of range [0,1]: %v", shortID(it.ID), p, r.Value)
		}
	}
	for _, v := range []*float64{it.RemainingDuration, it.CostLeft, it.MinSessionDuration, it.TotalDuration} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return fmt.Errorf("non-finite measurement")
		}
	}
	if it.RemainingDuration != nil && *it.RemainingDuration < 0 {
		return fmt.Errorf("item %s: remaining_duration negative", shortID(it.ID))
	}
	if it.CostLeft != nil && *it.CostLeft < 0 {
		return fmt.Errorf("item %s: cost_left negative", shortID(it.ID))
	}
	if it.MinSessionDuration != nil && *it.MinSessionDuration < 0 {
		return fmt.Errorf("item %s: min_session_duration negative", shortID(it.ID))
	}
	if it.TotalDuration != nil && *it.TotalDuration < 0 {
		return fmt.Errorf("item %s: total_duration negative", shortID(it.ID))
	}
	for _, m := range it.Modes {
		if !Mode(m).Valid() {
			return fmt.Errorf("item %s: invalid mode %q", shortID(it.ID), m)
		}
	}
	for _, e := range it.Engagements {
		if !Engagement(e).Valid() {
			return fmt.Errorf("item %s: invalid engagement %q", shortID(it.ID), e)
		}
	}
	return nil
}

// shortID renders a full UUID as a short display prefix.
func shortID(id string) string {
	const n = 6
	if len(id) <= n {
		return id
	}
	return id[:n] + "…"
}

// FloatPtr is a small helper for taking the address of a float64 literal.
func FloatPtr(f float64) *float64 { return &f }

// IntPtr is a small helper for taking the address of an int64 literal.
func IntPtr(i int64) *int64 { return &i }

// StrPtr is a small helper for taking the address of a string literal.
func StrPtr(s string) *string { return &s }

// EstimatedProgress returns a percentage based on the current duration estimates.
// Unknown, zero-total, or inconsistent estimates cannot yield meaningful progress.
// Estimates may be revised independently; this never changes completion status.
func (it *Item) EstimatedProgress() *float64 {
	if it.Ongoing || it.TotalDuration == nil || it.RemainingDuration == nil || *it.TotalDuration <= 0 || *it.RemainingDuration < 0 || *it.RemainingDuration > *it.TotalDuration {
		return nil
	}
	progress := 100 * (1 - *it.RemainingDuration / *it.TotalDuration)
	return &progress
}

// OngoingDurationError preserves unknown vs. inapplicable duration semantics.
type OngoingDurationError struct{}

func (*OngoingDurationError) Error() string {
	return "ongoing items cannot have total_duration or remaining_duration; clear both explicitly when converting an existing item"
}
