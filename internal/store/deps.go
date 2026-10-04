package store

import (
	"database/sql"
	"fmt"
)

// AddDependency adds an edge itemID → depID (itemID depends on depID), rejecting
// self-edges and cycles (application-level reachability check).
func (s *Store) AddDependency(itemID, depID string) error {
	if itemID == depID {
		return fmt.Errorf("%w: %s", ErrCycle, itemID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Both endpoints must exist.
	for _, id := range []string{itemID, depID} {
		if _, err := s.getItemTx(tx, id); err != nil {
			return err
		}
	}
	if err := s.checkCycle(tx, itemID, depID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO dependencies(item_id, depends_on) VALUES(?,?)`, itemID, depID); err != nil {
		return err
	}
	// Bump updated_at on the item whose graph changed.
	if _, err := tx.Exec(`UPDATE items SET updated_at=? WHERE id=?`, nowRFC3339(), itemID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveDependency deletes the edge itemID → depID. A missing edge is not an
// error (idempotent).
func (s *Store) RemoveDependency(itemID, depID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.getItemTx(tx, itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM dependencies WHERE item_id=? AND depends_on=?`, itemID, depID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE items SET updated_at=? WHERE id=?`, nowRFC3339(), itemID); err != nil {
		return err
	}
	return tx.Commit()
}

// Dependencies returns the ids that itemID depends on (its prerequisites).
func (s *Store) Dependencies(itemID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT depends_on FROM dependencies WHERE item_id=? ORDER BY depends_on`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Dependents returns the ids that depend on itemID (what it would unblock).
func (s *Store) Dependents(itemID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT item_id FROM dependencies WHERE depends_on=? ORDER BY item_id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// checkCycle reports whether adding an edge itemID → depID would create a cycle.
// That happens iff itemID is reachable from depID by following depends_on edges.
func (s *Store) checkCycle(tx *sql.Tx, itemID, depID string) error {
	visited := map[string]bool{depID: true}
	queue := []string{depID}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node == itemID {
			return fmt.Errorf("%w: %s -> %s", ErrCycle, itemID, depID)
		}
		rows, err := tx.Query(`SELECT depends_on FROM dependencies WHERE item_id=?`, node)
		if err != nil {
			return err
		}
		var next []string
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return err
			}
			next = append(next, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, n := range next {
			if !visited[n] {
				visited[n] = true
				queue = append(queue, n)
			}
		}
	}
	return nil
}
