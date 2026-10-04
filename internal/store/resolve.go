package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
		return "", err
	default:
		return "", &AmbiguousTitleError{Title: ref, Candidates: matches}
	}
}
