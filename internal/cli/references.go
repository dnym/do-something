package cli

import (
	"dosomething/internal/store"
	"errors"
)

// Resolve the whole batch before mutation, preserving the status commands'
// all-or-nothing behavior and reporting every invalid reference.
func resolveTransitionRefs(s *store.Store, verb store.Transition, refs []string) ([]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	problems := []store.IDProblem{}
	for _, ref := range refs {
		id, err := s.ResolveItemRef(ref)
		if err != nil {
			if !errors.Is(err, store.ErrInvalidID) {
				return nil, err
			}
			p := store.IDProblem{ID: ref, Code: "INVALID_ID"}
			var title *store.AmbiguousTitleError
			var prefix *store.AmbiguousIDError
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
	if len(problems) > 0 {
		return nil, &store.TransitionError{Verb: verb, Problems: problems}
	}
	return ids, nil
}
