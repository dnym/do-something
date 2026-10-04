// Package i18n is the localization layer. ALL human-facing display strings —
// prompts, text output, help prose, and text-mode error prose — flow through
// Translator.T(key, args…). The JSON contract, schema output, flag names and
// values, exit codes, error codes, config keys, and user data are NEVER
// localized (see the plan, §3.3).
//
// Shipped locales are en (canonical, also the fallback) and sv. Locale
// resolution: --lang flag > DO_SOMETHING_LANG > local.toml lang pin >
// LC_ALL/LC_MESSAGES/LANG subtag > built-in en. Missing keys fall back to
// English and never crash.
package i18n

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.toml
var localeFS embed.FS

// KnownLocales lists the shipped locales. A --lang value outside this set is an
// INVALID_ARGUMENT; any other source outside this set falls back to en.
var KnownLocales = []string{"en", "sv"}

// known reports whether locale is a shipped locale.
func known(locale string) bool {
	for _, k := range KnownLocales {
		if k == locale {
			return true
		}
	}
	return false
}

// Translator resolves messages for a single locale with en fallback.
type Translator struct {
	bundle   *goi18n.Bundle
	localize *goi18n.Localizer
	locale   string
}

// New builds a Translator for locale. An unknown locale transparently resolves
// every message to English (it never errors); the caller is responsible for
// validating an explicitly supplied --lang value via ValidateLocale.
func New(locale string) (*Translator, error) {
	bundle := goi18n.NewBundle(language.MustParse("en"))
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("i18n: read embedded locales: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		if _, err := bundle.LoadMessageFileFS(localeFS, "locales/"+e.Name()); err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", e.Name(), err)
		}
	}
	if locale == "" {
		locale = "en"
	}
	localizer := goi18n.NewLocalizer(bundle, locale)
	return &Translator{bundle: bundle, localize: localizer, locale: locale}, nil
}

// Locale returns the resolved locale the translator was built for.
func (tr *Translator) Locale() string { return tr.locale }

// T resolves a message key, substituting named arguments. args is a flat list of
// key/value pairs: T("error.invalid_value", "value", x, "argument", y). A missing
// key (or a missing translation that also lacks an en fallback) returns the key
// itself rather than failing.
func (tr *Translator) T(key string, args ...interface{}) string {
	if tr == nil || tr.localize == nil {
		return key
	}
	var msg map[string]interface{}
	if len(args) > 0 {
		msg = make(map[string]interface{}, len(args)/2)
		for i := 0; i+1 < len(args); i += 2 {
			k, ok := args[i].(string)
			if !ok {
				continue
			}
			msg[k] = args[i+1]
		}
	}
	res, err := tr.localize.Localize(&goi18n.LocalizeConfig{
		MessageID:    key,
		TemplateData: msg,
	})
	if err != nil && res == "" {
		// A key missing even from the canonical en catalog: return the key so we
		// never render blank or panic. (Any key present in en resolves for every
		// locale via the base-bundle fallback.)
		return key
	}
	return res
}

// ValidateLocale reports whether an explicitly supplied locale (from --lang) is a
// shipped locale.
func ValidateLocale(locale string) bool { return known(locale) }

// LocaleInputs are the ordered sources for locale resolution.
type LocaleInputs struct {
	Flag string // --lang (highest precedence; an unknown value is an error)
	Env  string // DO_SOMETHING_LANG
	Pin  string // local.toml lang pin
}

// ResolveLocale implements the precedence chain. It returns the locale to use,
// the source it came from (for diagnostics), and an error only when the --lang
// flag names an unshipped locale. Every other source that is set but unknown is
// skipped and the result falls back to en.
func ResolveLocale(in LocaleInputs) (locale, source string, err error) {
	if in.Flag != "" {
		if !known(in.Flag) {
			return "", "", fmt.Errorf("unknown language %q; known: %s", in.Flag, strings.Join(KnownLocales, ", "))
		}
		return in.Flag, "flag", nil
	}
	if in.Env != "" {
		if known(in.Env) {
			return in.Env, "env", nil
		}
		return "en", "env-unknown", nil
	}
	if in.Pin != "" {
		if known(in.Pin) {
			return in.Pin, "config", nil
		}
		return "en", "config-unknown", nil
	}
	if tag := envLocaleTag(); tag != "" {
		if known(tag) {
			return tag, "environment", nil
		}
		return "en", "environment-unknown", nil
	}
	return "en", "default", nil
}

// envLocaleTag extracts a language subtag from LC_ALL/LC_MESSAGES/LANG, e.g.
// "sv_SE.UTF-8" → "sv".
func envLocaleTag() string {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if v == "" {
			continue
		}
		tag := v
		if i := strings.IndexAny(tag, "."); i >= 0 {
			tag = tag[:i]
		}
		if i := strings.IndexAny(tag, "_-"); i >= 0 {
			tag = tag[:i]
		}
		if tag != "" {
			return tag
		}
	}
	return ""
}

// CatalogCompleteness is keyed by locale, with missing English message ids.
func CatalogCompleteness() (map[string][]string, error) {
	catalogs := map[string]map[string]any{}
	for _, lang := range KnownLocales {
		b, e := localeFS.ReadFile("locales/" + lang + ".toml")
		if e != nil {
			return nil, e
		}
		var m map[string]any
		if e = toml.Unmarshal(b, &m); e != nil {
			return nil, e
		}
		catalogs[lang] = m
	}
	out := map[string][]string{}
	for _, lang := range KnownLocales {
		out[lang] = []string{}
		for key := range catalogs["en"] {
			if _, ok := catalogs[lang][key]; !ok {
				out[lang] = append(out[lang], key)
			}
		}
		sort.Strings(out[lang])
	}
	return out, nil
}
