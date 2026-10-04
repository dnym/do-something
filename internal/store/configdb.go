package store

import (
	"database/sql"
)

// ConfigMap returns the raw tier-2 (in-database) config as key→text value.
func (s *Store) ConfigMap() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// GetConfig returns the tier-2 value for key ("" and false if absent).
func (s *Store) GetConfig(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM config WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetConfig upserts a tier-2 config key, stamping updated_at.
func (s *Store) SetConfig(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO config(key, value, updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, nowRFC3339())
	return err
}

// UnsetConfig deletes a tier-2 config key, returning false if it was absent.
// Deletion is a first-class, merge-visible operation (the value falls back to
// the tier-1 built-in).
func (s *Store) UnsetConfig(key string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM config WHERE key=?`, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
