package cli

import (
	"dosomething/internal/store"
	"errors"
)

// Resolve the whole batch before mutation, preserving the status commands'
// all-or-nothing behavior and reporting every invalid reference.
func resolveTransitionRefs(s *store.Store, verb store.Transition, refs []string) ([]string, error) {
	ids, problems, err := resolveRefs(s, refs)
	if err == nil && len(problems) > 0 {
		err = &store.TransitionError{Verb: verb, Problems: problems}
	}
	return ids, err
}

// resolveRefs resolves every reference, collecting each invalid or ambiguous
// one as a problem (with candidates or suggestions) instead of stopping.
func resolveRefs(s *store.Store, refs []string) ([]string, []store.IDProblem, error) {
	ids := []string{}
	seen := map[string]bool{}
	problems := []store.IDProblem{}
	for _, ref := range refs {
		id, err := s.ResolveItemRef(ref)
		if err != nil {
			if !errors.Is(err, store.ErrInvalidID) {
				return nil, nil, err
			}
			p := store.IDProblem{ID: ref, Code: "INVALID_ID"}
			var title *store.AmbiguousTitleError
			var prefix *store.AmbiguousIDError
			var missing *store.ItemNotFoundError
			if errors.As(err, &missing) && len(missing.Suggestions) > 0 {
				p.Suggestions = missing.Suggestions
			}
			if errors.As(err, &title) {
				p.Candidates = title.Candidates
			}
			if errors.As(err, &prefix) {
				p.Candidates = prefix.Candidates
			}
			problems = append(problems, p)
			continue
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, problems, nil
}
