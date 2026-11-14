package cli

import (
	"bufio"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

type HistoryFlags struct {
	ID    string   `arg:"" optional:""`
	Event []string `type:"event-type"`
	Since string   `type:"date"`
	Limit *int     `type:"nonnegative"`
	All   bool
}

type StatsFlags struct {
	Since string `type:"date"`
	Until string `type:"date"`
	All   bool
}

type UndoFlags struct {
	ID     string `arg:"" optional:""`
	Yes    bool
	DryRun bool
}

// defaultHistoryLimit keeps the unfiltered history readable.
const defaultHistoryLimit = 20

// statsDefaultDays is the default stats window, including today.
const statsDefaultDays = 30

// eventContext loads every event with item titles and undo links.
func eventContext(s *store.Store) ([]model.Event, map[string]*model.Item, map[string]string, error) {
	events, err := s.AllEvents()
	if err != nil {
		return nil, nil, nil, err
	}
	items, err := s.AllItems()
	if err != nil {
		return nil, nil, nil, err
	}
	byID := map[string]*model.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	reverted, err := s.RevertedIDs()
	return events, byID, reverted, err
}

func historyItem(id string, items map[string]*model.Item) output.HistoryItem {
	ref := output.HistoryItem{ID: id}
	if it := items[id]; it != nil {
		title := it.Title
		ref.Title = &title
	}
	return ref
}

// localDate parses a YYYY-MM-DD flag as local midnight.
func localDate(v string) (time.Time, error) {
	return time.ParseInLocation(time.DateOnly, v, time.Local)
}

func history(s *store.Store, f HistoryFlags) (output.History, error) {
	h := output.History{SchemaVersion: output.SchemaVersion, Events: []output.HistoryEvent{}}
	itemID := ""
	if f.ID != "" {
		var err error
		if itemID, err = s.ResolveItemRef(f.ID); err != nil {
			return h, err
		}
	}
	var since time.Time
	if f.Since != "" {
		var err error
		if since, err = localDate(f.Since); err != nil {
			return h, invalid(err)
		}
	}
	events, items, reverted, err := eventContext(s)
	if err != nil {
		return h, err
	}
	for id := range items {
		h.AllIDs = append(h.AllIDs, id)
	}
	byID := map[string]*output.HistoryEvent{}
	all := []output.HistoryEvent{}
	for _, ev := range events {
		out := output.HistoryEvent{ID: ev.ID, Type: ev.Type, At: ev.At, Activity: ev.Activity, Reverts: ev.Reverts, Suggested: []output.HistoryItem{}}
		if ev.ItemID != nil {
			ref := historyItem(*ev.ItemID, items)
			out.Item = &ref
		}
		for _, id := range ev.ItemIDs {
			out.Suggested = append(out.Suggested, historyItem(id, items))
		}
		if by, ok := reverted[ev.ID]; ok {
			out.RevertedBy = &by
		}
		all = append(all, out)
	}
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	types := f.Event
	for i := len(all) - 1; i >= 0; i-- { // newest first
		ev := all[i]
		switch {
		case len(types) > 0 && !slices.Contains(types, string(ev.Type)):
			continue
		case len(types) == 0 && !f.All && ev.Type == model.EventSuggested:
			continue // suggestions are frequent; ask for them explicitly
		case !since.IsZero() && ev.At.Before(since):
			continue
		case itemID != "" && !eventConcerns(ev, itemID):
			continue
		}
		h.Events = append(h.Events, ev)
	}
	h.Matched = len(h.Events)
	limit := defaultHistoryLimit
	if f.Limit != nil {
		limit = *f.Limit
	}
	if limit > 0 && len(h.Events) > limit {
		h.Events = h.Events[:limit]
	}
	h.Returned = len(h.Events)
	h.LinkTargets(byID)
	return h, nil
}

func eventConcerns(ev output.HistoryEvent, id string) bool {
	if ev.Item != nil && ev.Item.ID == id {
		return true
	}
	for _, s := range ev.Suggested {
		if s.ID == id {
			return true
		}
	}
	return false
}

// isoWeek labels a time's local ISO 8601 week.
func isoWeek(t time.Time) string {
	y, w := t.Local().ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

func stats(s *store.Store, f StatsFlags, now time.Time) (output.Stats, error) {
	r := output.Stats{SchemaVersion: output.SchemaVersion, ByType: []output.StatsBucket{}, ByWeek: []output.StatsWeek{}, Estimates: []output.StatsEstimate{}}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	until := today
	if f.Until != "" {
		var err error
		if until, err = localDate(f.Until); err != nil {
			return r, invalid(err)
		}
	}
	end := until.AddDate(0, 0, 1) // exclusive
	var since time.Time
	switch {
	case f.All:
	case f.Since != "":
		var err error
		if since, err = localDate(f.Since); err != nil {
			return r, invalid(err)
		}
	default:
		since = until.AddDate(0, 0, -(statsDefaultDays - 1))
	}
	if !since.IsZero() {
		if since.After(until) {
			return r, invalid(&argumentError{"since", f.Since, constraint{Format: "date"}, fmt.Errorf("since must not be after until")})
		}
		d := since.Format(time.DateOnly)
		r.Since = &d
	}
	r.Until = until.Format(time.DateOnly)
	events, items, reverted, err := eventContext(s)
	if err != nil {
		return r, err
	}
	inPeriod := func(t time.Time) bool { return !t.Before(since) && t.Before(end) }
	type key struct{ kind, typ string }
	buckets := map[key]*output.StatsBucket{}
	weeks := map[string]*output.StatsWeek{}
	logged := map[string]float64{} // all-time logged hours per item, for estimates
	finished := []string{}
	for _, ev := range events {
		if _, undone := reverted[ev.ID]; undone || ev.Type == model.EventReverted {
			continue
		}
		if ev.Type == model.EventLogged && ev.ItemID != nil && ev.Activity.Duration != nil {
			logged[*ev.ItemID] += *ev.Activity.Duration
		}
		if !inPeriod(ev.At) {
			continue
		}
		switch ev.Type {
		case model.EventStarted:
			r.Started++
		case model.EventDropped:
			r.Dropped++
		case model.EventCompleted:
			r.Completed++
			if ev.ItemID != nil {
				finished = append(finished, *ev.ItemID)
			}
		case model.EventLogged:
			a := ev.Activity
			r.Sessions++
			k := key{}
			bucket := output.StatsBucket{}
			if it := items[*ev.ItemID]; it != nil {
				kind := it.Kind
				bucket.Kind, bucket.Type = &kind, it.Type
				k.kind = string(kind)
				if it.Type != nil {
					k.typ = strings.ToLower(*it.Type)
				}
			}
			b := buckets[k]
			if b == nil {
				b = &bucket
				buckets[k] = b
			}
			b.Sessions++
			if a.Cost != nil {
				r.Cost += *a.Cost
				b.Cost += *a.Cost
			}
			if a.Duration != nil {
				r.Hours += *a.Duration
				b.Hours += *a.Duration
				wk := isoWeek(ev.At)
				if weeks[wk] == nil {
					weeks[wk] = &output.StatsWeek{Week: wk}
				}
				weeks[wk].Hours += *a.Duration
				weeks[wk].Sessions++
			}
		}
	}
	for _, b := range buckets {
		r.ByType = append(r.ByType, *b)
	}
	sort.SliceStable(r.ByType, func(i, j int) bool { return r.ByType[i].Hours > r.ByType[j].Hours })
	for _, w := range weeks {
		r.ByWeek = append(r.ByWeek, *w)
	}
	sort.Slice(r.ByWeek, func(i, j int) bool { return r.ByWeek[i].Week < r.ByWeek[j].Week })
	seen := map[string]bool{}
	ratios := []float64{}
	for _, id := range finished {
		it := items[id]
		if seen[id] || it == nil || it.Ongoing || it.TotalDuration == nil || *it.TotalDuration <= 0 || logged[id] <= 0 {
			continue
		}
		seen[id] = true
		ratio := logged[id] / *it.TotalDuration
		ratios = append(ratios, ratio)
		r.Estimates = append(r.Estimates, output.StatsEstimate{ID: id, Title: it.Title, EstimateHours: *it.TotalDuration, ActualHours: logged[id], Ratio: ratio})
	}
	if len(ratios) > 0 {
		sort.Float64s(ratios)
		m := ratios[len(ratios)/2]
		if len(ratios)%2 == 0 {
			m = (ratios[len(ratios)/2-1] + m) / 2
		}
		r.MedianRatio = &m
	}
	return r, nil
}

func undo(s *store.Store, f UndoFlags, interactive bool, in io.Reader, prompt io.Writer, tr *i18n.Translator) (output.UndoMutation, error) {
	result := output.UndoMutation{SchemaVersion: output.SchemaVersion, DryRun: f.DryRun}
	if !f.DryRun && !f.Yes && !interactive {
		return result, invalid(fmt.Errorf("undo needs --yes when not run in an interactive terminal (or --dry-run to preview)"))
	}
	itemID := ""
	if f.ID != "" {
		var err error
		if itemID, err = s.ResolveItemRef(f.ID); err != nil {
			return result, err
		}
	}
	allIDs, _, err := allItemReferences(s)
	if err != nil {
		return result, err
	}
	result.AllIDs = allIDs
	target, err := s.LastUndoableLog(itemID)
	if err != nil {
		return result, err
	}
	preview, err := s.RevertLog(target, true)
	if err != nil {
		return result, err
	}
	result.Undo = preview
	if f.DryRun {
		return result, nil
	}
	if !f.Yes {
		if err := output.UndoText(prompt, output.UndoMutation{DryRun: true, Undo: preview, AllIDs: allIDs}, tr); err != nil {
			return result, err
		}
		ok, err := askYesNo(in, prompt, tr.T("prompt.undo"), tr)
		if err != nil {
			return result, err
		}
		if !ok {
			result.Cancelled = true
			return result, nil
		}
	}
	result.Undo, err = s.RevertLog(target, false)
	result.Changed = err == nil
	return result, err
}

// askYesNo asks a [y/N] question; anything but yes declines.
func askYesNo(in io.Reader, out io.Writer, question string, tr *i18n.Translator) (bool, error) {
	reader := bufio.NewReader(in)
	for {
		if _, err := fmt.Fprint(out, question+" "); err != nil {
			return false, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "j", "ja":
			return true, nil
		case "", "n", "no", "nej":
			return false, nil
		}
		if _, err := fmt.Fprintln(out, tr.T("prompt.yes_no")); err != nil {
			return false, err
		}
	}
}
