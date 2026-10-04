package cli

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"github.com/alecthomas/kong"
)

// Parameter constraints drive parsing, schema discovery, and completions together.
type constraint struct {
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	Allowed          []string `json:"allowed,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	Format           string   `json:"format,omitempty"`
}

func number(v float64) *float64 { return &v }

var constraints = func() map[string]constraint {
	m := map[string]constraint{
		"effort":            {},
		"rating":            {Minimum: number(0), Maximum: number(1), Format: "rating"},
		"mode":              {},
		"engagement":        {},
		"unknown":           {Allowed: []string{"include", "exclude"}},
		"output-format":     {Allowed: []string{"text", "json"}},
		"data-format":       {Allowed: []string{"json"}},
		"color":             {Allowed: []string{"auto", "always", "never"}},
		"lang":              {Allowed: i18n.KnownLocales},
		"master":            {Allowed: []string{"local", "remote"}},
		"deps-action":       {Allowed: []string{"list", "add", "remove"}},
		"config-action":     {Allowed: []string{"get", "set", "unset"}},
		"shell":             {Allowed: []string{"bash", "zsh", "fish"}},
		"nonnegative":       {Minimum: number(0)},
		"positive":          {ExclusiveMinimum: number(0)},
		"unit":              {Minimum: number(0), Maximum: number(1)},
		"duration":          {Minimum: number(0), Format: "duration"},
		"positive-duration": {ExclusiveMinimum: number(0), Format: "duration"},
		"date":              {Format: "date"},
	}
	for _, l := range model.AllLevels {
		c := m["effort"]
		c.Allowed = append(c.Allowed, string(l))
		m["effort"] = c
	}
	for _, k := range model.AllKinds {
		c := m["kind"]
		c.Allowed = append(c.Allowed, string(k))
		m["kind"] = c
		c = m["kind-filter"]
		c.Allowed = append(c.Allowed, string(k))
		m["kind-filter"] = c
	}
	c := m["kind-filter"]
	c.Allowed = append(c.Allowed, "both")
	m["kind-filter"] = c
	for _, s := range model.AllStatuses {
		c := m["status"]
		c.Allowed = append(c.Allowed, string(s))
		m["status"] = c
	}
	for _, md := range model.AllModes {
		c := m["mode"]
		c.Allowed = append(c.Allowed, string(md))
		m["mode"] = c
	}
	for _, e := range model.AllEngagements {
		c := m["engagement"]
		c.Allowed = append(c.Allowed, string(e))
		m["engagement"] = c
	}
	for _, p := range model.AllProperties() {
		if p.IsRating() {
			c := m["rating-property"]
			c.Allowed = append(c.Allowed, string(p))
			m["rating-property"] = c
		}
	}
	return m
}()

type argumentError struct {
	Argument   string
	Value      any
	Constraint constraint
	Cause      error
}

func (e *argumentError) Error() string {
	return fmt.Sprintf("invalid value for %s: %v (%v)", e.Argument, e.Value, e.Cause)
}
func (e *argumentError) Unwrap() error { return e.Cause }

func parameterOptions() []kong.Option {
	options := []kong.Option{}
	for name, c := range constraints {
		options = append(options, kong.NamedMapper(name, kong.MapperFunc(func(ctx *kong.DecodeContext, target reflect.Value) error {
			// Consume the token ourselves: negative values are otherwise treated
			// as short flags and Kong's suggestion can erase the typed error.
			token := ctx.Scan.Pop()
			if token.IsEOL() {
				return &argumentError{ctx.Value.Name, nil, c, fmt.Errorf("value is required")}
			}
			raw, ok := token.Value.(string)
			if !ok {
				return &argumentError{ctx.Value.Name, token.Value, c, fmt.Errorf("expected text value")}
			}
			return decodeParameter(ctx.Value.Name, raw, target, c)
		})))
	}
	return options
}
func decodeParameter(name, raw string, target reflect.Value, c constraint) error {
	fail := func(e error) error { return &argumentError{name, raw, c, e} }
	if target.Kind() == reflect.Pointer {
		value := reflect.New(target.Type().Elem())
		if err := decodeParameter(name, raw, value.Elem(), c); err != nil {
			return err
		}
		target.Set(value)
		return nil
	}
	if target.Kind() == reflect.Slice {
		for _, part := range strings.Split(raw, ",") {
			value := reflect.New(target.Type().Elem()).Elem()
			if err := decodeParameter(name, part, value, c); err != nil {
				return err
			}
			target.Set(reflect.Append(target, value))
		}
		return nil
	}
	if len(c.Allowed) > 0 && !slices.Contains(c.Allowed, raw) {
		return fail(fmt.Errorf("allowed: %s", strings.Join(c.Allowed, ", ")))
	}
	var n float64
	switch target.Kind() {
	case reflect.String:
		switch c.Format {
		case "duration":
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fail(err)
			}
			n = d.Seconds()
		case "date":
			if _, err := time.Parse("2006-01-02", raw); err != nil {
				return fail(err)
			}
		}
		target.SetString(raw)
	case reflect.Float64:
		if c.Format == "duration" {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fail(err)
			}
			n = d.Hours()
			target.SetFloat(n)
			break
		}
		if c.Format == "rating" {
			if l, ok := model.ParseRatingLevel(raw); ok {
				target.SetFloat(l.Value())
				return nil
			}
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fail(err)
		}
		n = v
		target.SetFloat(v)
	case reflect.Int:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return fail(err)
		}
		n = float64(v)
		target.SetInt(int64(v))
	default:
		return fail(fmt.Errorf("unsupported parameter type %s", target.Type()))
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || c.Minimum != nil && n < *c.Minimum || c.ExclusiveMinimum != nil && n <= *c.ExclusiveMinimum || c.Maximum != nil && n > *c.Maximum {
		return fail(fmt.Errorf("value outside allowed finite range"))
	}
	return nil
}
