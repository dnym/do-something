// Package config implements the three-tier configuration model.
//
//	Tier 1 — built-ins: neutral defaults compiled into the binary. A fresh store
//	works with no file at all.
//	Tier 2 — DB config: the personal calibration, stored in the working DB's
//	config table. It is synced, digested, and merged with the data. Access is via
//	the `config` command; the store supplies the raw map.
//	Tier 3 — per-device: --db/--syncdir flags > env vars > local.toml > built-in.
//	Machine-specific by definition (paths, colour, language pin, device name);
//	never synced.
package config

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"dosomething/internal/model"
	"dosomething/internal/paths"
)

// DeviceConfig is the resolved tier-3 (per-device) configuration.
type DeviceConfig struct {
	DBPath     string `json:"db_path"`     // working-store path
	SyncDir    string `json:"sync_dir"`    // sync folder ("" = not configured)
	Color      string `json:"color"`       // auto | always | never
	Language   string `json:"language"`    // tier-3 language pin (may be empty)
	DeviceName string `json:"device_name"` // display-only label
}

// DeviceOptions carries flag-supplied tier-3 overrides. An empty field means the
// flag was not provided.
type DeviceOptions struct {
	DBPath   string
	SyncDir  string
	Color    string
	Language string // --lang (explicit; validated separately)
}

// LocalFile is the raw tier-3 per-device TOML file.
type LocalFile struct {
	DB         string `toml:"db"`
	SyncDir    string `toml:"syncdir"`
	Color      string `toml:"color"`
	Language   string `toml:"lang"`
	DeviceName string `toml:"device_name"`
}

// ReadLocalFile reads and parses the tier-3 file at path. A missing file is not
// an error (it yields an empty LocalFile).
func ReadLocalFile(path string) (*LocalFile, error) {
	lf := &LocalFile{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lf, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, lf); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return lf, nil
}

// LoadDevice resolves the tier-3 device configuration using the precedence
// flag > env > local.toml > built-in for each field.
func LoadDevice(opts DeviceOptions, localFile *LocalFile) (*DeviceConfig, error) {
	if localFile == nil {
		lf, err := ReadLocalFile(paths.LocalConfigFile())
		if err != nil {
			return nil, err
		}
		localFile = lf
	}
	dc := &DeviceConfig{Color: "auto"}

	// DB path: flag > env > toml > built-in.
	switch {
	case opts.DBPath != "":
		dc.DBPath = paths.ExpandHome(opts.DBPath)
	case os.Getenv(paths.EnvDB) != "":
		dc.DBPath = paths.ExpandHome(os.Getenv(paths.EnvDB))
	case localFile.DB != "":
		dc.DBPath = paths.ExpandHome(localFile.DB)
	default:
		dc.DBPath = paths.DefaultDBFile()
	}

	// Sync dir: flag > env > toml > (none).
	switch {
	case opts.SyncDir != "":
		dc.SyncDir = paths.ExpandHome(opts.SyncDir)
	case os.Getenv(paths.EnvSyncDir) != "":
		dc.SyncDir = paths.ExpandHome(os.Getenv(paths.EnvSyncDir))
	case localFile.SyncDir != "":
		dc.SyncDir = paths.ExpandHome(localFile.SyncDir)
	}

	if opts.Color != "" {
		dc.Color = opts.Color
	} else if localFile.Color != "" {
		dc.Color = localFile.Color
	}
	switch dc.Color {
	case "auto", "always", "never":
	default:
		return nil, fmt.Errorf("config: invalid color %q (want auto|always|never)", dc.Color)
	}

	if opts.Language != "" {
		dc.Language = opts.Language
	} else {
		dc.Language = localFile.Language
	}
	if dc.DeviceName == "" {
		dc.DeviceName = localFile.DeviceName
	}
	return dc, nil
}

// Config is the effective tier-1 + tier-2 configuration: a flat key→typed-value
// map plus a per-key source tier. Scoring keys fall through DB → built-in; a key
// absent from both (e.g. the intentionally-defaultless k.cost_left) is simply
// unset.
type Config struct {
	values  map[string]any
	sources map[string]string
	builtin map[string]any
	db      map[string]string
}

// BuiltinValues returns a fresh copy of the tier-1 default values.
func BuiltinValues() map[string]any {
	return map[string]any{
		"scoring.beta":                 0.2,
		"scoring.group.value":          1.0,
		"scoring.group.commitment":     1.0,
		"scoring.group.interest":       1.0,
		"scoring.k.remaining_duration": 20.0,
		// scoring.k.cost_left: intentionally NO default — cost behaves like unknown
		// until the owner calibrates it in their own currency unit.
		"scoring.k_pace":                           2.0,
		"scoring.deadline_horizon_days":            30.0,
		"scoring.effort.none":                      0.0,
		"scoring.effort.low":                       1.0 / 3.0,
		"scoring.effort.medium":                    1.0 / 2.0,
		"scoring.effort.high":                      2.0 / 3.0,
		"scoring.effort.very_high":                 0.8,
		"scoring.effort.max":                       1.0,
		"scoring.prior":                            0.5,
		"scoring.term.urgency":                     1.0,
		"scoring.term.momentum":                    1.0,
		"scoring.term.unblocking":                  1.0,
		"scoring.term.mode_match":                  2.0,
		"scoring.term.engagement_match":            2.0,
		"scoring.term.cooldown":                    1.0,
		"scoring.group_members.value.project":      []string{"career", "physical", "mental", "social"},
		"scoring.group_members.value.media":        []string{"quality", "influence", "actuality"},
		"scoring.group_members.commitment.project": []string{"remaining_duration", "cost_left"},
		"scoring.group_members.commitment.media":   []string{"remaining_duration"},
		"scoring.group_members.interest.project":   []string{"interest"},
		"scoring.group_members.interest.media":     []string{"interest"},
		"scoring.half_life.interest":               18.0,
		"scoring.half_life.career":                 36.0,
		"scoring.half_life.physical":               36.0,
		"scoring.half_life.mental":                 36.0,
		"scoring.half_life.social":                 36.0,
		"scoring.half_life.actuality":              12.0,
		"scoring.half_life.quality":                math.Inf(1),
		"scoring.half_life.influence":              math.Inf(1),
		"scoring.half_life.intensity":              math.Inf(1),
		"scoring.no_deadline_horizon":              0.0,
		"scoring.cooldown_days":                    7.0,
		"scoring.duration_anchors":                 []float64{0.5, 3, 20, 100},
		"scoring.cost_unit":                        "",
		"output.default_limit":                     10,
		"output.unknown_policy":                    "include",
	}
}

// NewConfig builds the effective config from the built-in defaults and a raw
// tier-2 (DB) map of key→text value.
func NewConfig(db map[string]string) *Config {
	b := BuiltinValues()
	values := make(map[string]any, len(b)+len(db))
	sources := make(map[string]string, len(b)+len(db))
	for k, v := range b {
		values[k] = v
		sources[k] = "builtin"
	}
	for k, text := range db {
		hint, ok := b[k]
		if !ok {
			hint = inferType(text)
			if strings.HasPrefix(k, "vocabulary.") {
				hint = []string{}
			}
		}
		parsed, err := parseValue(text, hint)
		if err != nil {
			// A malformed tier-2 value is treated as an error surfaced by the
			// store/doctor; here we fall back to the string form so lookups do not
			// panic, and the raw value is retained for reporting.
			values[k] = text
		} else {
			values[k] = parsed
		}
		sources[k] = "db"
	}
	return &Config{values: values, sources: sources, builtin: b, db: db}
}

// inferType guesses a value type from its text when a key has no built-in default.
func inferType(text string) any {
	if _, err := strconv.ParseFloat(text, 64); err == nil {
		return float64(0)
	}
	return ""
}

// parseValue parses text into the type indicated by typeHint.
func parseValue(text string, typeHint any) (any, error) {
	switch typeHint.(type) {
	case float64:
		return parseFloatLoose(text)
	case int:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, err
		}
		return int(n), nil
	case []float64:
		return parseFloatSlice(text)
	case []string:
		return parseStringSlice(text)
	case string:
		return text, nil
	default:
		if f, err := parseFloatLoose(text); err == nil {
			return f, nil
		}
		return text, nil
	}
}

// parseFloatLoose accepts "inf"/"-inf" and "nan" in addition to ordinary floats.
func parseFloatLoose(text string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "inf", "+inf", "infinity":
		return math.Inf(1), nil
	case "-inf", "-infinity":
		return math.Inf(-1), nil
	case "nan":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(strings.TrimSpace(text), 64)
}

func parseFloatSlice(text string) ([]float64, error) {
	parts := strings.Split(text, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := parseFloatLoose(p)
		if err != nil {
			return nil, fmt.Errorf("bad number %q: %w", p, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func parseStringSlice(text string) ([]string, error) {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// --- Typed getters (effective values; built-in fallback already applied) ---

// Float returns the effective float value for key, or ok=false if the key is
// unset (no built-in default and no db override).
func (c *Config) Float(key string) (float64, bool) {
	v, ok := c.values[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

func (c *Config) Int(key string) (int, bool) {
	v, ok := c.values[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(int)
	return n, ok
}

func (c *Config) String(key string) (string, bool) {
	v, ok := c.values[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (c *Config) StringSlice(key string) ([]string, bool) {
	v, ok := c.values[key]
	if !ok {
		return nil, false
	}
	s, ok := v.([]string)
	return s, ok
}

func (c *Config) FloatSlice(key string) ([]float64, bool) {
	v, ok := c.values[key]
	if !ok {
		return nil, false
	}
	s, ok := v.([]float64)
	return s, ok
}

// Source reports the tier a key's effective value came from: "builtin", "db", or
// "" if the key is unset.
func (c *Config) Source(key string) string {
	return c.sources[key]
}

// Has reports whether a key has an effective value at any tier.
func (c *Config) Has(key string) bool {
	_, ok := c.values[key]
	return ok
}

// StringValue stringifies an effective value for display in the `config` command.
func (c *Config) StringValue(key string) string {
	v, ok := c.values[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case float64:
		return formatFloat(t)
	case int:
		return strconv.Itoa(t)
	case string:
		return t
	case []float64:
		parts := make([]string, len(t))
		for i, f := range t {
			parts[i] = formatFloat(f)
		}
		return strings.Join(parts, ", ")
	case []string:
		return strings.Join(t, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	case f == math.Trunc(f):
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// Keys returns the sorted set of all effective keys (built-in ∪ db).
func (c *Config) Keys() []string {
	seen := map[string]bool{}
	for k := range c.values {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- Semantic accessors used by the scoring engine ---

// GroupWeight returns the weight of a scoring group (built-in 1.0 default).
func (c *Config) GroupWeight(g model.Group) float64 {
	if f, ok := c.Float("scoring.group." + string(g)); ok {
		return f
	}
	return 1.0
}

// PropertyWeight returns the per-property weight for a kind: the kind-scoped key
// wins, then the kind-agnostic key, then 1.0.
func (c *Config) PropertyWeight(kind model.Kind, p model.Property) float64 {
	if f, ok := c.Float("scoring.weights." + string(kind) + "." + string(p)); ok {
		return f
	}
	if f, ok := c.Float("scoring.weights." + string(p)); ok {
		return f
	}
	return 1.0
}

// K returns the half-saturation constant for a cost-like measurement. ok=false
// means the key is unset (e.g. cost_left with no calibrated k) — the measurement
// then behaves like unknown (d = prior).
func (c *Config) K(measurement string) (float64, bool) {
	return c.Float("scoring.k." + measurement)
}

// KPace returns the half-saturation pace (hours/day) for project urgency.
func (c *Config) KPace() float64 {
	if f, ok := c.Float("scoring.k_pace"); ok {
		return f
	}
	return 2.0
}

// HalfLife returns the decay half-life (months) for a rating property. An
// infinite value means the property never decays.
func (c *Config) HalfLife(p model.Property) float64 {
	if f, ok := c.Float("scoring.half_life." + string(p)); ok {
		return f
	}
	return math.Inf(1)
}

// Beta returns the right-now blend weight.
func (c *Config) Beta() float64 {
	if f, ok := c.Float("scoring.beta"); ok {
		return f
	}
	return 0.2
}

// Prior returns the unknown-value prior.
func (c *Config) Prior() float64 {
	if f, ok := c.Float("scoring.prior"); ok {
		return f
	}
	return 0.5
}

// CooldownDays returns the freshness window in days.
func (c *Config) CooldownDays() float64 {
	if f, ok := c.Float("scoring.cooldown_days"); ok {
		return f
	}
	return 7.0
}

// NoDeadlineHorizon returns the assumed horizon (days) for items with no deadline;
// 0 means the urgency term is simply absent.
func (c *Config) NoDeadlineHorizon() float64 {
	if f, ok := c.Float("scoring.no_deadline_horizon"); ok {
		return f
	}
	return 0
}

// DeadlineHorizon returns the day-based urgency horizon (days): the media
// deadline curve 1 − clamp(days_left/h, 0, 1) and the project fallback when
// remaining_duration is unknown.
func (c *Config) DeadlineHorizon() float64 {
	if f, ok := c.Float("scoring.deadline_horizon_days"); ok {
		return f
	}
	return 30
}

// TermWeight returns a right-now term weight (built-in 1.0 default).
func (c *Config) TermWeight(name string) float64 {
	if f, ok := c.Float("scoring.term." + name); ok {
		return f
	}
	return 1.0
}

// EffortThreshold returns the intensity ceiling an effort level admits
// (--effort X keeps items with intensity ≤ the threshold): the tier-2
// override (scoring.effort.<level>) wins, then the level's canonical value.
func (c *Config) EffortThreshold(l model.RatingLevel) float64 {
	if f, ok := c.Float("scoring.effort." + string(l)); ok {
		return f
	}
	return l.Value()
}

// GroupMembers returns the effective group→property composition for a kind:
// a tier-2 override (scoring.group_members.<group>.<kind>) wins over the v1
// built-in composition (model.GroupMembers).
func (c *Config) GroupMembers(kind model.Kind, g model.Group) []model.Property {
	if s, ok := c.StringSlice("scoring.group_members." + string(g) + "." + string(kind)); ok {
		out := make([]model.Property, 0, len(s))
		for _, p := range s {
			out = append(out, model.Property(p))
		}
		return out
	}
	return model.GroupMembers(kind, g)
}

// MinSessionDurationDefault returns the default min-session-duration (hours) for a type, if configured.
func (c *Config) MinSessionDurationDefault(t string) (float64, bool) {
	if t == "" {
		return 0, false
	}
	return c.Float("defaults.min_session_duration." + t)
}

// Types returns the effective type vocabulary (user-defined; empty when unset).
func (c *Config) Types() []string {
	if s, ok := c.StringSlice("vocabulary.types"); ok {
		return s
	}
	return []string{}
}

// DurationAnchors returns the coarse duration estimates (hours).
func (c *Config) DurationAnchors() []float64 {
	if s, ok := c.FloatSlice("scoring.duration_anchors"); ok {
		return s
	}
	return []float64{0.5, 3, 20, 100}
}

// CostUnit returns the display-only currency label.
func (c *Config) CostUnit() string {
	s, _ := c.String("scoring.cost_unit")
	return s
}

// DefaultLimit returns the default result limit.
func (c *Config) DefaultLimit() int {
	if n, ok := c.Int("output.default_limit"); ok {
		return n
	}
	return 10
}

// UnknownPolicy returns the default unknown-value policy ("include"/"exclude").
func (c *Config) UnknownPolicy() string {
	s, _ := c.String("output.unknown_policy")
	if s == "" {
		return "include"
	}
	return s
}
