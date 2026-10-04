package i18n

import (
	"strings"
	"testing"
)

func TestCatalogs(t *testing.T) {
	for _, lang := range KnownLocales {
		tr, e := New(lang)
		if e != nil {
			t.Fatal(e)
		}
		for _, k := range []string{"help.root", "prompt.title", "error.generic", "output.no_results", "rating.label.interest"} {
			v := tr.T(k, "code", "X", "detail", "test")
			if v == k || strings.TrimSpace(v) == "" {
				t.Errorf("%s: missing %s", lang, k)
			}
		}
	}
	sv, _ := New("sv")
	if sv.T("prompt.title") != "Titel" {
		t.Fatal(sv.T("prompt.title"))
	}
}
func TestCatalogCompleteness(t *testing.T) {
	gaps, e := CatalogCompleteness()
	if e != nil {
		t.Fatal(e)
	}
	for _, lang := range KnownLocales {
		if len(gaps[lang]) > 0 {
			t.Errorf("%s is missing keys present in en: %v", lang, gaps[lang])
		}
	}
}
func TestLocalePriority(t *testing.T) {
	t.Setenv("LC_ALL", "sv_SE.UTF-8")
	for _, tt := range []struct {
		in   LocaleInputs
		want string
		err  bool
	}{{LocaleInputs{}, "sv", false}, {LocaleInputs{Pin: "en"}, "en", false}, {LocaleInputs{Env: "en", Pin: "sv"}, "en", false}, {LocaleInputs{Flag: "sv", Env: "en"}, "sv", false}, {LocaleInputs{Flag: "xx"}, "", true}, {LocaleInputs{Env: "xx"}, "en", false}} {
		v, _, e := ResolveLocale(tt.in)
		if v != tt.want || (e != nil) != tt.err {
			t.Fatal(v, e)
		}
	}
}
