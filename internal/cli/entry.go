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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
)

func entrySpec(f Entry, s *store.Store, id string) (store.ItemSpec, error) {
	spec := store.ItemSpec{ID: id, Title: f.Title, Kind: model.Kind(f.Kind), Ongoing: f.Ongoing, Type: f.Type, Category: f.Category, Notes: f.Notes, URL: f.URL, RemainingDuration: f.RemainingDuration, CostLeft: f.Cost, MinSessionDuration: f.MinSessionDuration, TotalDuration: f.TotalDuration, ClearDeadline: f.ClearDeadline, ClearRemainingDuration: f.ClearRemainingDuration, ClearCost: f.ClearCost, ClearMinSessionDuration: f.ClearMinSessionDuration, ClearTotalDuration: f.ClearTotalDuration, SetRatings: map[model.Property]model.Rating{}}
	if f.Kind != "" && !spec.Kind.Valid() {
		return spec, invalid(fmt.Errorf("invalid kind %q", f.Kind))
	}
	if f.Status != "" {
		st := model.Status(f.Status)
		if !st.Valid() {
			return spec, invalid(&argumentError{"status", f.Status, constraints["status"], fmt.Errorf("allowed: %s", strings.Join(constraints["status"].Allowed, ", "))})
		}
		spec.Status = &st
	}
	kind := spec.Kind
	if id != "" {
		it, e := s.GetItem(id)
		if e != nil {
			return spec, e
		}
		if kind == "" {
			kind = it.Kind
		}
	}
	if f.Deadline != nil {
		if f.ClearDeadline {
			return spec, invalid(fmt.Errorf("deadline and clear-deadline conflict"))
		}
		d, e := time.Parse("2006-01-02", *f.Deadline)
		if e != nil {
			return spec, invalid(e)
		}
		spec.Deadline = &d
	}
	for _, v := range []struct {
		n     *float64
		clear bool
	}{{f.RemainingDuration, f.ClearRemainingDuration}, {f.Cost, f.ClearCost}, {f.MinSessionDuration, f.ClearMinSessionDuration}, {f.TotalDuration, f.ClearTotalDuration}} {
		if v.n != nil && (v.clear || *v.n < 0 || math.IsNaN(*v.n) || math.IsInf(*v.n, 0)) {
			return spec, invalid(fmt.Errorf("measurement must be finite, nonnegative, and not cleared simultaneously"))
		}
	}
	now := time.Now().UTC()
	for p, v := range map[model.Property]*float64{model.PropIntensity: f.Intensity, model.PropCareer: f.Career, model.PropPhysical: f.Physical, model.PropMental: f.Mental, model.PropSocial: f.Social, model.PropInterest: f.Interest, model.PropActuality: f.Actuality, model.PropInfluence: f.Influence, model.PropQuality: f.Quality} {
		if v != nil {
			if !kind.ApplicableProperty(p) || *v < 0 || *v > 1 || math.IsNaN(*v) || math.IsInf(*v, 0) {
				return spec, invalid(fmt.Errorf("rating %s must apply to %s and be in [0,1]", p, kind))
			}
			spec.SetRatings[p] = model.Rating{Property: p, Value: *v, RatedAt: &now}
		}
	}
	for _, name := range f.UnsetRating {
		for _, p := range strings.Split(name, ",") {
			prop := model.Property(p)
			if !prop.IsRating() {
				return spec, invalid(fmt.Errorf("invalid rating %s", p))
			}
			if _, ok := spec.SetRatings[prop]; ok {
				return spec, invalid(fmt.Errorf("cannot set and unset %s", p))
			}
			spec.UnsetRatings = append(spec.UnsetRatings, prop)
		}
	}
	if f.Tag != nil || f.ClearTags {
		if f.ClearTags && len(f.Tag) > 0 {
			return spec, invalid(fmt.Errorf("tag and clear-tags conflict"))
		}
		spec.Tags = &f.Tag
	}
	if f.DependsOn != nil || f.ClearDependencies {
		if f.ClearDependencies && len(f.DependsOn) > 0 {
			return spec, invalid(fmt.Errorf("depends-on and clear-dependencies conflict"))
		}
		deps := []string{}
		for _, v := range f.DependsOn {
			dep, e := s.ResolveItemRef(v)
			if e != nil {
				return spec, e
			}
			deps = append(deps, dep)
		}
		spec.DependsOn = &deps
	}
	if f.Modes != nil || f.ClearModes {
		if f.ClearModes && len(f.Modes) > 0 {
			return spec, invalid(fmt.Errorf("mode and clear-modes conflict"))
		}
		if f.ClearModes {
			empty := []string{}
			spec.Modes = &empty
		} else {
			for _, m := range f.Modes {
				if !model.Mode(m).Valid() {
					return spec, invalid(fmt.Errorf("invalid mode %s", m))
				}
			}
			spec.Modes = &f.Modes
		}
	}
	if f.Engagements != nil || f.ClearEngagements {
		if f.ClearEngagements && len(f.Engagements) > 0 {
			return spec, invalid(fmt.Errorf("engagement and clear-engagements conflict"))
		}
		if f.ClearEngagements {
			empty := []string{}
			spec.Engagements = &empty
		} else {
			for _, e := range f.Engagements {
				if !model.Engagement(e).Valid() {
					return spec, invalid(fmt.Errorf("invalid engagement %s", e))
				}
			}
			spec.Engagements = &f.Engagements
		}
	}
	return spec, nil
}

// formatRatingValue renders a stored rating as its level label when it is a
// canonical anchor fraction, otherwise as the fraction itself.
func formatRatingValue(v float64) string {
	if l := model.LevelForValue(v); l != "" {
		return string(l)
	}
	return output.FormatFloat(v)
}

// parseRatingInput accepts a rating level label or a plain fraction in [0,1].
func parseRatingInput(raw string) (float64, error) {
	if l, ok := model.ParseRatingLevel(raw); ok {
		return l.Value(), nil
	}
	n, e := strconv.ParseFloat(raw, 64)
	if e != nil {
		return 0, fmt.Errorf("%q is not a rating level or a number in 0..1", raw)
	}
	if n < 0 || n > 1 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("rating must be a level or a number in 0..1, got %g", n)
	}
	return n, nil
}
func promptEntry(f *Entry, existing *model.Item, c *config.Config, in io.Reader, out io.Writer, tr *i18n.Translator) (map[string]string, error) {
	r := bufio.NewReader(in)
	updates := map[string]string{}
	ask := func(key, current string) (string, error) {
		fmt.Fprint(out, tr.T(key))
		if current != "" {
			fmt.Fprintf(out, " [%s]", current)
		}
		fmt.Fprint(out, " "+tr.T("prompt.skip_clear")+": ")
		line, e := r.ReadString('\n')
		if e != nil {
			return "", e
		}
		return strings.TrimSpace(line), nil
	}
	var e error
	title, kind := "", ""
	if existing != nil {
		title = existing.Title
		kind = string(existing.Kind)
	}
	if f.Title == "" {
		f.Title, e = ask("prompt.title", title)
		if e != nil {
			return nil, e
		}
		if f.Title == "" {
			f.Title = title
		}
	}
	if f.Kind == "" {
		fmt.Fprintln(out, strings.Join(constraints["kind"].Allowed, ", "))
		f.Kind, e = ask("prompt.kind", kind)
		if e != nil {
			return nil, e
		}
		if f.Kind == "" {
			f.Kind = kind
		}
	}
	if f.Kind == "" || !model.Kind(f.Kind).Valid() {
		return nil, invalid(&argumentError{"kind", f.Kind, constraints["kind"], fmt.Errorf("allowed: %s", strings.Join(constraints["kind"].Allowed, ", "))})
	}
	if f.Title == "" {
		return nil, invalid(fmt.Errorf("title and a valid kind are required"))
	}
	if f.Status == "" {
		current := ""
		if existing != nil {
			current = string(existing.Status)
		}
		pairs := []string{}
		for _, s := range model.AllStatuses {
			pairs = append(pairs, string(s)+" — "+tr.T("status."+string(s)))
		}
		fmt.Fprintln(out, strings.Join(pairs, ", "))
		fmt.Fprintln(out, tr.T("prompt.status_choices"))
		fmt.Fprint(out, tr.T("prompt.status"))
		if current != "" {
			fmt.Fprintf(out, " [%s]", current)
		}
		fmt.Fprint(out, ": ")
		line, e := r.ReadString('\n')
		if e != nil {
			return nil, e
		}
		v := strings.TrimSpace(line)
		if v != "" {
			if v == "clear" {
				return nil, invalid(&argumentError{"status", v, constraints["status"], fmt.Errorf("status always has a value; clear is not allowed")})
			}
			// A single digit selects the status in the order shown above.
			if len(v) == 1 && v[0] >= '1' && v[0] <= '4' {
				v = string(model.AllStatuses[v[0]-'1'])
			}
			if !model.Status(v).Valid() {
				return nil, invalid(&argumentError{"status", v, constraints["status"], fmt.Errorf("allowed: %s", strings.Join(constraints["status"].Allowed, ", "))})
			}
			f.Status = v
		}
	}
	for _, p := range []struct {
		key     string
		dest    **string
		current *string
	}{{"prompt.type", &f.Type, nil}, {"prompt.category", &f.Category, nil}, {"prompt.notes", &f.Notes, nil}, {"prompt.url", &f.URL, nil}} {
		if *p.dest != nil {
			continue
		}
		current := ""
		if existing != nil {
			switch p.key {
			case "prompt.type":
				p.current = existing.Type
			case "prompt.category":
				p.current = existing.Category
			case "prompt.notes":
				p.current = existing.Notes
			case "prompt.url":
				p.current = existing.URL
			}
			if p.current != nil {
				current = *p.current
			}
		}
		vocabKey := "vocabulary.types"
		topic := "type"
		if p.key == "prompt.category" {
			vocabKey = "vocabulary.categories"
			topic = "category"
		}
		if p.key == "prompt.type" || p.key == "prompt.category" {
			terms, _ := c.StringSlice(vocabKey)
			if len(terms) == 0 {
				fmt.Fprintln(out, tr.T("prompt.vocabulary_empty", "topic", topic, "key", vocabKey))
			} else {
				fmt.Fprintln(out, strings.Join(terms, ", "))
			}
		}
		v, e := ask(p.key, current)
		if e != nil {
			return nil, e
		}
		if v != "" {
			if v == "clear" {
				v = ""
			}
			*p.dest = &v
			if v != "" && (p.key == "prompt.type" || p.key == "prompt.category") {
				terms, _ := c.StringSlice(vocabKey)
				known := false
				for _, t := range terms {
					known = known || t == v
				}
				if !known {
					answer, e := ask("prompt.add_vocabulary", "")
					if e != nil {
						return nil, e
					}
					if answer == "y" || answer == "yes" {
						updates[vocabKey] = strings.Join(append(terms, v), ",")
					}
				}
			}
		}
	}
	rv := reflect.ValueOf(f).Elem()
	asked := []model.Property{}
	for _, p := range model.RatingProperties(model.Kind(f.Kind)) {
		name := strings.ToUpper(string(p[:1])) + string(p[1:])
		if rv.FieldByName(name).IsNil() && !slices.Contains(f.UnsetRating, string(p)) {
			asked = append(asked, p)
		}
	}
	if len(asked) > 0 {
		fmt.Fprintln(out, tr.T("prompt.rating_levels"))
	}
	for _, p := range asked {
		name := strings.ToUpper(string(p[:1])) + string(p[1:])
		current := ""
		if existing != nil {
			if v := existing.RatingValue(p); v != nil {
				current = formatRatingValue(*v)
			}
		}
		fmt.Fprintln(out, tr.T("rating.range."+string(p)))
		v, e := ask("rating.label."+string(p), current)
		if e != nil {
			return nil, e
		}
		if v == "" {
			continue
		}
		if v == "clear" {
			f.UnsetRating = append(f.UnsetRating, string(p))
			continue
		}
		n, e := parseRatingInput(v)
		if e != nil {
			return nil, invalid(e)
		}
		rv.FieldByName(name).Set(reflect.ValueOf(&n))
	}
	ongoing := existing != nil && existing.Ongoing
	if f.Ongoing != nil {
		ongoing = *f.Ongoing
	}
	for _, p := range []struct {
		key   string
		dest  **float64
		clear *bool
		prop  model.Property
	}{{"prompt.remaining_duration", &f.RemainingDuration, &f.ClearRemainingDuration, model.PropRemainingDuration}, {"prompt.cost_left", &f.Cost, &f.ClearCost, model.PropCostLeft}, {"prompt.min_session_duration", &f.MinSessionDuration, &f.ClearMinSessionDuration, model.PropMinSessionDuration}, {"prompt.total_duration", &f.TotalDuration, &f.ClearTotalDuration, model.PropTotalDuration}} {
		if *p.dest != nil || *p.clear || !model.Kind(f.Kind).ApplicableProperty(p.prop) || ongoing && (p.prop == model.PropTotalDuration || p.prop == model.PropRemainingDuration) {
			continue
		}
		current := ""
		if existing != nil {
			if v := existing.MeasurementValue(p.prop); v != nil {
				current = output.FormatFloat(*v)
				if p.prop != model.PropCostLeft {
					current = output.FormatDuration(*v, tr)
				}
			}
		}
		if p.prop == model.PropMinSessionDuration && current == "" && f.Type != nil {
			if hours, ok := c.MinSessionDurationDefault(*f.Type); ok {
				fmt.Fprintln(out, tr.T("prompt.session_default", "duration", output.FormatDuration(hours, tr)))
			}
		}
		if p.prop == model.PropTotalDuration || p.prop == model.PropRemainingDuration {
			fmt.Fprintln(out, tr.T("prompt.total_duration_choices"))
		}
		v, e := ask(p.key, current)
		if e != nil {
			return nil, e
		}
		if v == "" {
			continue
		}
		if v == "clear" {
			*p.clear = true
			continue
		}
		if (p.prop == model.PropTotalDuration || p.prop == model.PropRemainingDuration) && len(v) == 1 && v[0] >= '1' && v[0] <= '4' {
			n := c.DurationAnchors()[v[0]-'1']
			*p.dest = &n
			continue
		}
		var n float64
		if p.prop == model.PropCostLeft {
			n, e = strconv.ParseFloat(v, 64)
		} else {
			e = decodeParameter(string(p.prop), v, reflect.ValueOf(&n).Elem(), constraints["duration"])
		}
		if e != nil {
			return nil, invalid(e)
		}
		*p.dest = &n
	}
	for _, key := range []string{"prompt.deadline", "prompt.tags", "prompt.modes", "prompt.engagements", "prompt.dependency"} {
		if key == "prompt.deadline" && (f.Deadline != nil || f.ClearDeadline) || key == "prompt.tags" && (f.Tag != nil || f.ClearTags) || key == "prompt.modes" && (f.Modes != nil || f.ClearModes) || key == "prompt.engagements" && (f.Engagements != nil || f.ClearEngagements) || key == "prompt.dependency" && (f.DependsOn != nil || f.ClearDependencies) {
			continue
		}
		if key == "prompt.tags" {
			terms, _ := c.StringSlice("vocabulary.tags")
			fmt.Fprintln(out, strings.Join(terms, ", "))
		}
		if key == "prompt.modes" {
			pairs := []string{}
			for _, m := range constraints["mode"].Allowed {
				pairs = append(pairs, m+" — "+tr.T("mode."+m))
			}
			fmt.Fprintln(out, strings.Join(pairs, ", "))
		}
		if key == "prompt.engagements" {
			pairs := []string{}
			for _, e := range constraints["engagement"].Allowed {
				pairs = append(pairs, e+" — "+tr.T("engagement."+e))
			}
			fmt.Fprintln(out, strings.Join(pairs, ", "))
		}
		current := ""
		if existing != nil {
			switch key {
			case "prompt.deadline":
				if existing.Deadline != nil {
					current = existing.Deadline.Format("2006-01-02")
				}
			case "prompt.tags":
				current = strings.Join(existing.Tags, ",")
			case "prompt.modes":
				current = strings.Join(existing.Modes, ",")
			case "prompt.engagements":
				current = strings.Join(existing.Engagements, ",")
			case "prompt.dependency":
				current = strings.Join(existing.Dependencies, ",")
			}
		}
		v, e := ask(key, current)
		if e != nil {
			return nil, e
		}
		if v == "" {
			continue
		}
		switch key {
		case "prompt.deadline":
			if v == "clear" {
				f.ClearDeadline = true
			} else {
				f.Deadline = &v
			}
		case "prompt.tags":
			if v == "clear" {
				f.ClearTags = true
			} else {
				f.Tag = strings.Split(v, ",")
				terms, _ := c.StringSlice("vocabulary.tags")
				for i, tag := range f.Tag {
					tag = strings.TrimSpace(tag)
					f.Tag[i] = tag
					if tag == "" || slices.Contains(terms, tag) {
						continue
					}
					fmt.Fprintln(out, tag)
					answer, err := ask("prompt.add_vocabulary", "")
					if err != nil {
						return nil, err
					}
					if answer == "y" || answer == "yes" {
						terms = append(terms, tag)
						updates["vocabulary.tags"] = strings.Join(terms, ",")
					}
				}
			}
		case "prompt.modes":
			if v == "clear" {
				f.ClearModes = true
			} else {
				f.Modes = []string{}
				for _, p := range strings.Split(v, ",") {
					p = strings.TrimSpace(p)
					if !model.Mode(p).Valid() {
						return nil, invalid(&argumentError{"mode", p, constraints["mode"], fmt.Errorf("allowed: %s", strings.Join(constraints["mode"].Allowed, ", "))})
					}
					f.Modes = append(f.Modes, p)
				}
			}
		case "prompt.engagements":
			if v == "clear" {
				f.ClearEngagements = true
			} else {
				f.Engagements = []string{}
				for _, p := range strings.Split(v, ",") {
					p = strings.TrimSpace(p)
					if !model.Engagement(p).Valid() {
						return nil, invalid(&argumentError{"engagement", p, constraints["engagement"], fmt.Errorf("allowed: %s", strings.Join(constraints["engagement"].Allowed, ", "))})
					}
					f.Engagements = append(f.Engagements, p)
				}
			}
		case "prompt.dependency":
			if v == "clear" {
				f.ClearDependencies = true
			} else {
				// Same split as the --depends-on flag: comma-separated, a
				// backslash escapes a comma inside a title.
				f.DependsOn = kong.SplitEscaped(v, ',')
			}
		}
	}
	return updates, nil
}

func refreshRatings(id string, s *store.Store, opts store.Options, device *config.DeviceConfig, in io.Reader, out io.Writer, tr *i18n.Translator) error {
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, tr.T("prompt.refresh"))
	answer, e := reader.ReadString('\n')
	if e != nil {
		return e
	}
	if strings.TrimSpace(answer) != "y" && strings.TrimSpace(answer) != "yes" {
		return nil
	}
	it, e := s.GetItem(id)
	if e != nil {
		return e
	}
	spec := store.ItemSpec{ID: id, SetRatings: map[model.Property]model.Rating{}}
	now := time.Now().UTC()
	fmt.Fprintln(out, tr.T("prompt.rating_levels"))
	for _, p := range model.RatingProperties(it.Kind) {
		current := ""
		if n := it.RatingValue(p); n != nil {
			current = formatRatingValue(*n)
		}
		fmt.Fprintf(out, "%s (%s) [%s]: ", tr.T("rating.label."+string(p)), tr.T("rating.range."+string(p)), current)
		line, e := reader.ReadString('\n')
		if e != nil {
			return e
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "clear" {
			spec.UnsetRatings = append(spec.UnsetRatings, p)
			continue
		}
		n, e := parseRatingInput(line)
		if e != nil {
			return invalid(e)
		}
		spec.SetRatings[p] = model.Rating{Property: p, Value: n, RatedAt: &now}
	}
	if len(spec.SetRatings) == 0 && len(spec.UnsetRatings) == 0 {
		return nil
	}
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
