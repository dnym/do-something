package store

import (
	"database/sql"
	"fmt"
	"time"
	"uuid"
)

// newDatabaseUUID mints the immutable store identity. UUIDv7 is timestamp-ordered
// and practically collision-resistant (≥62 random bits); correctness never
// depends on the timestamp ordering.
func newDatabaseUUID() string { return uuid.NewV7().String() }

// newEventID mints a UUIDv7 for an append-only event row.
func newEventID() string { return uuid.NewV7().String() }

// newItemID mints a UUIDv7 for a new item.
func newItemID() string { return uuid.NewV7().String() }

// nowRFC3339 is the wall-clock instant in RFC3339 UTC, used for created_at /
// updated_at / rated_at / at bookkeeping.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// parseTime parses an RFC3339 timestamp, tolerating a trailing "Z" and fractional
// seconds, returning UTC.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, err
		}
	}
	return t.UTC(), nil
}

// insertGenerated retries only a collision on id, never another constraint error.
// The caller's transaction keeps related rows tied to the successful identity.
func insertGenerated(tx *sql.Tx, generate func() string, query string, args ...any) (string, error) {
	for range 16 {
		id := generate()
		values := append([]any{id}, args...)
		result, err := tx.Exec(query+" ON CONFLICT(id) DO NOTHING", values...)
		if err != nil {
			return "", err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if count == 1 {
			return id, nil
		}
	}
	return "", fmt.Errorf("UUID generation repeatedly collided")
}
