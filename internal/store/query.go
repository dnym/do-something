package store

// MatchingIDs accepts only predicates compiled by internal/filter, never user SQL.
func (s *Store) MatchingIDs(predicate string, args []any) (map[string]bool, error) {
	rows, e := s.db.Query("SELECT i.id FROM items AS i WHERE "+predicate, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out[id] = true
	}
	return out, rows.Err()
}
