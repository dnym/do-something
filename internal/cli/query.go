package cli

import (
	"dosomething/internal/config"
	"dosomething/internal/filter"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/scoring"
	"dosomething/internal/store"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"
)

func scoringConfig(c *config.Config) scoring.Config {
	r := scoring.DefaultConfig()
	r.Beta = c.Beta()
	r.Prior = c.Prior()
	r.CooldownDays = c.CooldownDays()
	r.KPace = c.KPace()
	r.DeadlineHorizon = c.DeadlineHorizon()
	r.NoDeadlineHorizon = c.NoDeadlineHorizon()
	r.GroupWeights = map[model.Group]float64{}
	r.PropertyWeights = map[string]float64{}
	r.TermWeights = map[string]float64{}
	r.Groups = map[model.Group]map[model.Kind][]model.Property{}
	for _, g := range model.AllGroups {
		r.GroupWeights[g] = c.GroupWeight(g)
		r.Groups[g] = map[model.Kind][]model.Property{}
		for _, k := range model.AllKinds {
			r.Groups[g][k] = c.GroupMembers(k, g)
		}
	}
	for _, p := range model.AllProperties() {
		if v, ok := c.K(string(p)); ok {
			r.K[p] = v
		}
		r.HalfLives[p] = c.HalfLife(p)
		for _, k := range model.AllKinds {
			r.PropertyWeights[string(k)+"."+string(p)] = c.PropertyWeight(k, p)
		}
	}
	for _, t := range []string{"urgency", "momentum", "unblocking", "mode_match", "engagement_match", "cooldown"} {
		r.TermWeights[t] = c.TermWeight(t)
	}
	return r
}
func makeQuery(f QueryFlags, c *config.Config) (filter.Query, error) {
	q := filter.Query{Effort: f.Effort, Budget: f.Budget, Type: f.Type, Category: f.Category, Kind: f.Kind, Status: f.Status, Mode: f.Mode, Engagement: f.Engagement, Tags: append([]string{}, f.Tag...), NotTags: append([]string{}, f.NotTag...), Text: f.Text, Unknown: f.Unknown, IncludeUnready: f.IncludeUnready, MinScore: f.MinScore, Limit: c.DefaultLimit(), Random: f.Random}
	if f.Limit != nil {
		q.Limit = *f.Limit
	}
	if q.Unknown == "" {
		q.Unknown = c.UnknownPolicy()
	}
	if f.Effort != "" {
		v := c.EffortThreshold(model.RatingLevel(f.Effort))
		q.MaxIntensity = &v
	}
	for _, x := range []struct {
		s string
		p **float64
	}{{f.Session, &q.SessionSeconds}, {f.FinishWithin, &q.FinishSeconds}} {
		if x.s != "" {
			d, e := time.ParseDuration(x.s)
			if e != nil {
				return q, e
			}
			n := d.Seconds()
			*x.p = &n
		}
	}
	if q.Effort != "" {
		q.EffortConstraint = &filter.EffortConstraint{Value: q.Effort, MaxIntensity: q.MaxIntensity}
	}
	return q, q.Validate()
}
func query(s *store.Store, c *config.Config, f QueryFlags, browse bool, id string) (output.Result, error) {
	q, e := makeQuery(f, c)
	if e != nil {
		return output.Result{}, invalid(e)
	}
	now := time.Now().UTC()
	out := output.Result{SchemaVersion: output.SchemaVersion, GeneratedAt: now, Filters: q, Items: []output.Item{}}
	items, e := s.AllItems()
	if e != nil {
		return out, e
	}
	events, e := s.AllEvents()
	if e != nil {
		return out, e
	}
	byID := map[string]*model.Item{}
	for _, it := range items {
		byID[it.ID] = it
		out.AllIDs = append(out.AllIDs, it.ID)
	}
	predicate, args := q.SQL()
	candidates, e := s.MatchingIDs(predicate, args)
	if e != nil {
		return out, e
	}
	cfg := scoringConfig(c)
	for _, it := range items {
		if !candidates[it.ID] {
			continue
		}
		if id != "" && it.ID != id {
			continue
		}
		if !browse && (it.Status != model.StatusNotStarted && it.Status != model.StatusInProgress) {
			continue
		}
		pass, ev := q.Match(it)
		if !pass {
			continue
		}
		blockers := []store.Blocker{}
		for _, dep := range it.Dependencies {
			if d := byID[dep]; d != nil && d.Status != model.StatusDone {
				blockers = append(blockers, store.Blocker{ID: d.ID, Title: d.Title, Status: d.Status})
			}
		}
		if !browse && !q.IncludeUnready && len(blockers) > 0 {
			continue
		}
		ctx := scoring.Context{Mode: model.Mode(q.Mode), Engagement: model.Engagement(q.Engagement)}
		for _, v := range events {
			if v.Type == model.EventStarted && v.ItemID != nil && *v.ItemID == it.ID {
				if ctx.LastStarted == nil || v.At.After(*ctx.LastStarted) {
					at := v.At
					ctx.LastStarted = &at
				}
			}
			if v.Type == model.EventSuggested {
				for _, candidate := range v.ItemIDs {
					if candidate == it.ID && (ctx.LastSuggested == nil || v.At.After(*ctx.LastSuggested)) {
						at := v.At
						ctx.LastSuggested = &at
					}
				}
			}
		}
		for _, dep := range items {
			contains := false
			others := 0
			for _, p := range dep.Dependencies {
				if p == it.ID {
					contains = true
				} else if byID[p] != nil && byID[p].Status != model.StatusDone {
					others++
				}
			}
			if contains {
				ctx.Dependents = append(ctx.Dependents, scoring.Dependent{Status: dep.Status, OtherOpenDeps: others})
			}
		}
		score, e := scoring.Score(it, ctx, cfg, now)
		if e != nil {
			return out, e
		}
		if score.Final < q.MinScore {
			continue
		}
		out.Items = append(out.Items, output.NewItem(it, score, blockers, ev, f.Explain, cfg.Beta))
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if *a.Score == *b.Score {
			return a.ID < b.ID
		}
		return *a.Score > *b.Score
	})
	out.Matched = len(out.Items)
	if q.Random {
		rand.Shuffle(len(out.Items), func(i, j int) { out.Items[i], out.Items[j] = out.Items[j], out.Items[i] })
	}
	if q.Limit > 0 && len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
	}
	out.Returned = len(out.Items)
	return out, nil
}
func record(s *store.Store, r *output.Result) error {
	ids := []string{}
	for _, it := range r.Items {
		ids = append(ids, it.ID)
	}
	b, e := json.Marshal(r.Filters)
	if e != nil {
		return e
	}
	q := string(b)
	var top *string
	if len(ids) > 0 {
		top = &ids[0]
	}
	_, e = s.AppendEvent(model.Event{Type: model.EventSuggested, ItemID: top, ItemIDs: ids, Query: &q})
	r.Recorded = e == nil
	return e
}
func effectiveConfig(s *store.Store) (*config.Config, error) {
	m, e := s.ConfigMap()
	if e != nil {
		return nil, fmt.Errorf("config: %w", e)
	}
	return config.NewConfig(m), nil
}
