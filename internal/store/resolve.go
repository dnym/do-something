package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dosomething/internal/search"
)

// AmbiguousIDError reports that an id prefix matches multiple items. It is an
// INVALID_ID condition; the candidates (full ids, sorted) are listed so a human
// or agent can disambiguate (decision 18).
type AmbiguousIDError struct {
	Prefix     string   `json:"prefix"`
	Candidates []string `json:"candidates"`
}

func (e *AmbiguousIDError) Error() string {
	return fmt.Sprintf("invalid id: prefix %q matches %d items: %s", e.Prefix, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

func (e *AmbiguousIDError) Unwrap() error { return ErrInvalidID }

// IsAmbiguousID reports whether err is an ambiguous-prefix failure.
func IsAmbiguousID(err error) bool {
	var ae *AmbiguousIDError
	return errors.As(err, &ae)
}

// AmbiguousTitleError reports that an exact title matches multiple items. It
// is an INVALID_ID condition; the candidates (full ids, sorted) are listed so
// a human or agent can disambiguate.
type AmbiguousTitleError struct {
	Title      string   `json:"title"`
	Candidates []string `json:"candidates"`
}

func (e *AmbiguousTitleError) Error() string {
	return fmt.Sprintf("ambiguous title %q matches %d items: %s", e.Title, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

func (e *AmbiguousTitleError) Unwrap() error { return ErrInvalidID }

// IsAmbiguousTitle reports whether err is an ambiguous-title failure.
func IsAmbiguousTitle(err error) bool {
	var ae *AmbiguousTitleError
	return errors.As(err, &ae)
}

// ResolveID maps a full id or a unique id prefix to the stored item id.
// Commands accept prefixes; an ambiguous prefix fails with the candidates
// listed, a non-matching prefix fails INVALID_ID.
func (s *Store) ResolveID(prefix string) (string, error) {
	if prefix == "" {
		return "", &ItemNotFoundError{ID: prefix}
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM items WHERE id = ?`, prefix).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// Prefix match; escape LIKE wildcards so user input is matched literally.
	like := escapeLike(prefix) + `%`
	rows, err := s.db.Query(`SELECT id FROM items WHERE id LIKE ? ESCAPE '\' ORDER BY id`, like)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return "", err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", &ItemNotFoundError{ID: prefix}
	case 1:
		return matches[0], nil
	default:
		return "", &AmbiguousIDError{Prefix: prefix, Candidates: matches}
	}
}

// escapeLike escapes the LIKE special characters (\, %, _) with a backslash.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ResolveItemRef resolves an item reference to the stored item id. The
// reference is, in priority order: a full id, a unique id prefix, or — only
// when no id resolves uniquely — a unique exact (case-sensitive) title. Id forms
// take priority over titles, so a title that is also an existing item's id
// resolves as the id. Failures are the corresponding id-form failures
// (ItemNotFoundError, AmbiguousIDError) unless the title itself is ambiguous,
// which fails with AmbiguousTitleError.
func (s *Store) ResolveItemRef(ref string) (string, error) {
	id, err := s.ResolveID(ref)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrInvalidID) {
		return "", err
	}
	rows, qerr := s.db.Query(`SELECT id FROM items WHERE title = ? ORDER BY id`, ref)
	if qerr != nil {
		return "", qerr
	}
	defer rows.Close()
	matches := []string{}
	for rows.Next() {
		var m string
		if e := rows.Scan(&m); e != nil {
			return "", e
		}
		matches = append(matches, m)
	}
	if e := rows.Err(); e != nil {
		return "", e
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		var nf *ItemNotFoundError
		if errors.As(err, &nf) {
			suggestions, serr := s.SuggestTitles(ref, maxSuggestions)
			if serr != nil {
				return "", serr
			}
			return "", &ItemNotFoundError{ID: nf.ID, Suggestions: suggestions}
		}
		return "", err
	default:
		return "", &AmbiguousTitleError{Title: ref, Candidates: matches}
	}
}

// maxSuggestions bounds the "did you mean" hints attached to a failed lookup.
const maxSuggestions = 3

// SuggestTitles returns up to limit items whose titles are close to ref: a
// case- and whitespace-insensitive equal title first, then search matches by
// relevance, then whole-title typo matches. The hints never change resolution, which stays case-sensitive.
func (s *Store) SuggestTitles(ref string, limit int) ([]Suggestion, error) {
	out := []Suggestion{}
	if strings.TrimSpace(ref) == "" || limit <= 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT id, title FROM items ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		Suggestion
		rank float64
	}
	found := []scored{}
	for rows.Next() {
		var v Suggestion
		if err := rows.Scan(&v.ID, &v.Title); err != nil {
			return nil, err
		}
		if SameTitle(ref, v.Title) {
			found = append(found, scored{v, 2})
		} else if m := search.Find(ref, v.Title, nil); m != nil {
			found = append(found, scored{v, m.Relevance})
		} else if sim, ok := search.Similar(ref, v.Title); ok {
			found = append(found, scored{v, 0.6 * sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].rank > found[j].rank })
	for i := 0; i < len(found) && i < limit; i++ {
		out = append(out, found[i].Suggestion)
	}
	return out, nil
}

// SameTitle reports whether two titles differ only in case or whitespace.
func SameTitle(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// SimilarTitles lists other items whose titles differ from title only in
// case or whitespace — likely accidental duplicates.
func (s *Store) SimilarTitles(title, exceptID string) ([]Suggestion, error) {
	rows, err := s.db.Query(`SELECT id, title FROM items WHERE id <> ? ORDER BY id`, exceptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Suggestion{}
	for rows.Next() {
		var v Suggestion
		if err := rows.Scan(&v.ID, &v.Title); err != nil {
			return nil, err
		}
		if SameTitle(title, v.Title) {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}
