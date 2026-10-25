package search

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFind(t *testing.T) {
	for _, tc := range []struct{ query, title, kind string }{
		{"woodworkng", "Woodworking course", "fuzzy"},
		{"sandng", "Sanding walls", "fuzzy"},
		{"sanding", "Sandign walls", "fuzzy"},
		{"paint buy", "Buy paint", "partial"},
		{"bookshelf", "Build a book shelf", "partial"},
		{"book shelf", "Build a bookshelf", "partial"},
		{"buy paint", "BUY PAINT", "exact"},
		{"buy paint", "Buy paint online", "partial"},
		{"buy   paint", "Buy\npaint", "exact"},
		{"buy-paint", "Buy paint online", "partial"},
		{"målnng", "Målning hemma", "fuzzy"},
		{"猫の絵", "猫の絵を描く", "partial"},
		{"++", "Learn C++", "partial"},
		{"cafe", "Read a book", ""},
		{"cat", "Cut", ""}, // short words do not expand to spelling neighbors
		{"", "Anything", ""},
		{"   ", "Anything", ""},
		{"paint", "", ""},
		{"paint buy", "Buy supplies", ""}, // every query word is required
		{"paint paint", "Paint", ""},      // cannot reuse a single matching word
		{"sand fish", "Sand walls", ""},
	} {
		t.Run(tc.query+"/"+tc.title, func(t *testing.T) {
			m := Find(tc.query, tc.title, nil)
			if tc.kind == "" {
				if m != nil {
					t.Fatalf("unexpected match: %+v", m)
				}
				return
			}
			if m == nil || m.Kind != tc.kind || m.Field != "title" {
				t.Fatalf("match: %+v, want %s", m, tc.kind)
			}
			if m.Relevance < 0 || m.Relevance > 1 || math.IsNaN(m.Relevance) {
				t.Fatal(m)
			}
		})
	}
}

func TestRankingAndFieldPreference(t *testing.T) {
	notes := "Buy paint"
	matches := []*Match{
		Find("buy paint", "Buy paint", nil),
		Find("buy paint", "Buy paint online", nil),
		Find("buy paint", "Buy plaint", nil),
		Find("buy paint", "A shopping errand", &notes),
	}
	for i, m := range matches {
		if m == nil {
			t.Fatalf("missing match %d", i)
		}
		if i > 0 && matches[i-1].Relevance <= m.Relevance {
			t.Fatalf("unexpected order: %+v vs %+v", matches[i-1], m)
		}
	}
	// Title evidence must win even over an exact notes match.
	m := Find("buy paint", "Buy plaint", &notes)
	if m == nil || m.Field != "title" || m.Kind != "fuzzy" {
		t.Fatal(m)
	}
	// Query words cannot be split across title and notes.
	tail := "paint"
	if m := Find("buy paint", "Buy", &tail); m != nil {
		t.Fatal(m)
	}
}

func TestNotesExcerpt(t *testing.T) {
	notes := strings.Repeat("İ unrelated. ", 100) + "Try woodworking after lunch. " + strings.Repeat("More text. ", 100)
	for _, query := range []string{"woodworking", "woodworkng", "after try"} {
		m := Find(query, "A course", &notes)
		if m == nil || m.Field != "notes" || !strings.Contains(m.Excerpt, "woodworking") {
			t.Fatalf("%s: %+v", query, m)
		}
		if !utf8.ValidString(m.Excerpt) || utf8.RuneCountInString(m.Excerpt) > 162 || strings.ContainsAny(m.Excerpt, "\n\r\t") {
			t.Fatal(m)
		}
		if !strings.HasPrefix(m.Excerpt, "…") || !strings.HasSuffix(m.Excerpt, "…") {
			t.Fatal(m)
		}
	}
}

func TestBoundedSpellingDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"paint", "paint", 0}, {"paint", "pait", 1}, {"pait", "paint", 1},
		{"sandng", "sanding", 1}, {"sanding", "sandign", 1},
		{"abcdef", "abcfed", 2}, {"åäö", "åöä", 1},
		{"a", "", 1}, {"", "a", 1}, {"", "", 0},
		{strings.Repeat("a", 10000), strings.Repeat("b", 10000), 10000},
	} {
		for limit := 0; limit <= 2; limit++ {
			got := distance([]rune(tc.a), []rune(tc.b), limit)
			if tc.want <= limit && got != tc.want || tc.want > limit && got <= limit {
				t.Fatalf("%q/%q limit %d: got %d, actual %d", tc.a, tc.b, limit, got, tc.want)
			}
		}
	}
}
