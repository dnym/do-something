package cli

import (
	"bufio"
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

type ReviewFlags struct {
	ID    string `arg:"" optional:""`
	Kind  string `type:"kind-filter"`
	Limit *int   `type:"nonnegative"`
}

// staleDecay is how far (0..1) a rating must have decayed toward the prior
// before review asks about it again; unknown ratings always qualify.
const staleDecay = 0.25

// defaultReviewLimit bounds one review session.
const defaultReviewLimit = 10

const hoursPerMonth = 24 * 365.25 / 12

// reviewCandidates lists active items whose ratings are unknown or stale,
// most in need of review first. With id set, it returns that item with every
// applicable rating, whatever its state.
func reviewCandidates(s *store.Store, c *config.Config, f ReviewFlags, now time.Time) (output.Review, error) {
	r := output.Review{SchemaVersion: output.SchemaVersion, StaleDecay: staleDecay, Items: []output.ReviewItem{}, Single: f.ID != ""}
	id := ""
	if f.ID != "" {
		var err error
		if id, err = s.ResolveItemRef(f.ID); err != nil {
			return r, err
		}
	}
	items, err := s.AllItems()
	if err != nil {
		return r, err
	}
	for _, it := range items {
		r.AllIDs = append(r.AllIDs, it.ID)
	}
	kind := f.Kind
	for _, it := range items {
		switch {
		case id != "" && it.ID != id:
			continue
		case id == "" && it.Status != model.StatusNotStarted && it.Status != model.StatusInProgress:
			continue
		case kind != "" && kind != "both" && string(it.Kind) != kind:
			continue
		}
		ri := output.ReviewItem{ID: it.ID, Title: it.Title, Kind: it.Kind, Type: it.Type, Status: it.Status, Ratings: []output.ReviewRating{}}
		total, stale := 0.0, 0
		for _, p := range model.RatingProperties(it.Kind) {
			rr := output.ReviewRating{Property: p, State: model.StateUnknown, Decay: 1, Stale: true}
			if hl := c.HalfLife(p); !math.IsInf(hl, 1) {
				rr.HalfLifeMonths = &hl
			}
			if rating, ok := it.Ratings[p]; ok {
				v := rating.Value
				rr.State, rr.Value, rr.RatedAt, rr.Decay = model.StateKnown, &v, rating.RatedAt, 0
				if rating.RatedAt != nil {
					age := now.Sub(*rating.RatedAt).Hours() / hoursPerMonth
					rr.AgeMonths = &age
					if rr.HalfLifeMonths != nil {
						rr.Decay = decayed(age, *rr.HalfLifeMonths)
					}
				}
				rr.Stale = rr.Decay >= staleDecay
			}
			total += rr.Decay
			if rr.Stale {
				stale++
			}
			ri.Ratings = append(ri.Ratings, rr)
		}
		if len(ri.Ratings) > 0 {
			ri.Staleness = total / float64(len(ri.Ratings))
		}
		if id == "" && stale == 0 {
			continue
		}
		r.Items = append(r.Items, ri)
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].Staleness > r.Items[j].Staleness })
	r.Matched = len(r.Items)
	limit := defaultReviewLimit
	if f.Limit != nil {
		limit = *f.Limit
	}
	if limit > 0 && len(r.Items) > limit {
		r.Items = r.Items[:limit]
	}
	r.Returned = len(r.Items)
	return r, nil
}

// decayed is the fraction of a rating's weight lost toward the prior after
// age months with the given half-life (≤ 0 = fully decayed).
func decayed(age, halfLife float64) float64 {
	if halfLife <= 0 {
		return 1
	}
	return 1 - math.Pow(0.5, math.Max(0, age)/halfLife)
}

// reviewSession walks the candidates interactively, asking for each stale or
// unknown rating (every rating for a single named item) and saving after each
// item, so quitting keeps finished items. The store lock is held only while
// saving.
func reviewSession(r output.Review, all bool, opts store.Options, device *config.DeviceConfig, in io.Reader, out io.Writer, tr *i18n.Translator) (output.ReviewResult, error) {
	res := output.ReviewResult{SchemaVersion: output.SchemaVersion, Items: []output.ReviewSaved{}}
	if len(r.Items) == 0 {
		fmt.Fprintln(out, tr.T("output.review.nothing"))
		return res, nil
	}
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, tr.T("prompt.review_intro"))
	fmt.Fprintln(out, tr.T("prompt.rating_levels"))
	for n, it := range r.Items {
		fmt.Fprintf(out, "\n%s\n", tr.T("output.review.item", "n", fmt.Sprint(n+1), "total", fmt.Sprint(len(r.Items)), "title", it.Title, "id", output.Prefix(it.ID, r.AllIDs), "kind", string(it.Kind)))
		spec := store.ItemSpec{ID: it.ID, SetRatings: map[model.Property]model.Rating{}}
		quit, skip := false, false
		for _, rr := range it.Ratings {
			if !all && !rr.Stale {
				continue
			}
			current := tr.T("output.review.unknown")
			if rr.Value != nil {
				current = formatRatingValue(*rr.Value)
				if rr.AgeMonths != nil {
					current += ", " + tr.T("output.review.age", "months", fmt.Sprintf("%.0f", *rr.AgeMonths))
				}
			}
			for {
				fmt.Fprintf(out, "  %s (%s) [%s]: ", tr.T("rating.label."+string(rr.Property)), tr.T("rating.range."+string(rr.Property)), current)
				line, err := reader.ReadString('\n')
				if err != nil && line == "" {
					quit = true // EOF ends the session, keeping what was saved
					break
				}
				line = strings.TrimSpace(line)
				now := time.Now().UTC()
				switch strings.ToLower(line) {
				case "":
				case "=":
					if rr.Value == nil {
						fmt.Fprintln(out, "  "+tr.T("prompt.review_nothing_to_confirm"))
						continue
					}
					spec.SetRatings[rr.Property] = model.Rating{Property: rr.Property, Value: *rr.Value, RatedAt: &now}
				case "clear":
					spec.UnsetRatings = append(spec.UnsetRatings, rr.Property)
				case "s", "skip":
					skip = true
				case "q", "quit":
					quit = true
				default:
					v, err := parseRatingInput(line)
					if err != nil {
						fmt.Fprintln(out, "  "+err.Error())
						continue
					}
					spec.SetRatings[rr.Property] = model.Rating{Property: rr.Property, Value: v, RatedAt: &now}
				}
				break
			}
			if quit || skip {
				break
			}
		}
		if changed := len(spec.SetRatings) + len(spec.UnsetRatings); changed > 0 {
			if err := saveRatings(spec, opts, device); err != nil {
				return res, err
			}
			res.Items = append(res.Items, output.ReviewSaved{ID: it.ID, Title: it.Title, Updated: changed})
			res.Updated += changed
			res.Changed = true
		}
		res.Reviewed++
		if quit {
			break
		}
	}
	return res, nil
}

// saveRatings applies one item's rating changes under the application lock
// and publishes the result.
func saveRatings(spec store.ItemSpec, opts store.Options, device *config.DeviceConfig) error {
	unlock, e := store.Acquire(opts)
	if e != nil {
		return e
	}
	defer unlock()
	opts.Locked = true
	writer, e := store.Open(store.OpenWrite, opts)
	if e != nil {
		return e
	}
	defer writer.Close()
	if _, e = writer.Save(spec, false); e != nil {
		return e
	}
	return publish(writer, device)
}
