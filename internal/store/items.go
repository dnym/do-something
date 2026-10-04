package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"dosomething/internal/model"
)

// ItemSpec is the input to Save for both add (creating=true) and edit
// (creating=false). Nil pointers mean: on create → store NULL/empty default; on
// edit → leave unchanged. Explicit Clear* flags turn a known measurement back
// into NULL (the first-class unset operations, plan §7).
type ItemSpec struct {
	Ongoing  *bool
	ID       string // required on edit; ignored on create
	Title    string
	Kind     model.Kind
	Status   *model.Status
	Category *string
	Type     *string
	Notes    *string
	URL      *string
	LegacyID *int64

	Deadline      *time.Time
	ClearDeadline bool

	RemainingDuration      *float64
	ClearRemainingDuration bool

	CostLeft  *float64
	ClearCost bool

	MinSessionDuration      *float64
	ClearMinSessionDuration bool

	TotalDuration      *float64
	ClearTotalDuration bool

	SetRatings   map[model.Property]model.Rating // rating rows to upsert; a nil RatedAt stores an undated rating (e.g. imported) — the input layer stamps the wall clock for recorded ratings
	UnsetRatings []model.Property                // rating rows to delete
	Tags         *[]string                       // non-nil = replace full set
	Modes        *[]string                       // non-nil = replace full set (built-in activity kinds)
	Engagements  *[]string                       // non-nil = replace full set (built-in engagement styles)
	DependsOn    *[]string                       // non-nil = replace full set
}

const itemColumns = `id, legacy_id, title, kind, status, category, "type", notes, url,
	created_at, updated_at, deadline, remaining_duration, cost_left, min_session_duration, total_duration, ongoing`

// Save creates (creating=true) or updates (creating=false) an item in one
// all-or-nothing transaction, including its ratings, tags, and dependencies.
// On create it mints a UUIDv7 and returns it.
func (s *Store) Save(spec ItemSpec, creating bool) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := nowRFC3339()

	id := spec.ID
	if creating {
		if spec.ID != "" {
			return "", fmt.Errorf("store: create with a fixed id is not supported")
		}
		id = newItemID()
	}

	if creating {
		if spec.Title == "" {
			return "", errors.New("store: title is required")
		}
		if !spec.Kind.Valid() {
			return "", fmt.Errorf("store: invalid kind %q", spec.Kind)
		}
	}

	// Load the existing row (edit) to compute effective values.
	var existing *model.Item
	if !creating {
		existing, err = s.getItemTx(tx, id)
		if err != nil {
			return "", err
		}
	}

	// Effective value for each column: explicit clear → NULL; explicit new value
	// → that value; otherwise keep the existing value (nil on create).
	var (
		exCat, exType, exNotes, exURL           *string
		exDeadline                              *time.Time
		exRemaining, exCost, exSession, exTotal *float64
		exStatus                                model.Status
		exKind                                  model.Kind
		exTitle                                 string
		exLegacy                                *int64
	)
	exStatus = model.StatusNotStarted
	if !creating {
		exCat, exType, exNotes, exURL = existing.Category, existing.Type, existing.Notes, existing.URL
		exDeadline = existing.Deadline
		exRemaining, exCost, exSession, exTotal = existing.RemainingDuration, existing.CostLeft, existing.MinSessionDuration, existing.TotalDuration
		exStatus, exKind, exTitle = existing.Status, existing.Kind, existing.Title
		exLegacy = existing.LegacyID
	}

	kind := exKind
	if spec.Kind != "" {
		kind = spec.Kind
	}
	title := exTitle
	if spec.Title != "" {
		title = spec.Title
	}
	status := exStatus
	if spec.Status != nil {
		status = *spec.Status
	}
	cat := exCat
	if spec.Category != nil {
		cat = spec.Category
	}
	typ := exType
	if spec.Type != nil {
		typ = spec.Type
	}
	notes := exNotes
	if spec.Notes != nil {
		notes = spec.Notes
	}
	urlv := exURL
	if spec.URL != nil {
		urlv = spec.URL
	}
	legacy := exLegacy
	if spec.LegacyID != nil {
		legacy = spec.LegacyID
	}
	deadline := exDeadline
	if spec.ClearDeadline {
		deadline = nil
	} else if spec.Deadline != nil {
		deadline = spec.Deadline
	}
	remaining := exRemaining
	if spec.ClearRemainingDuration {
		remaining = nil
	} else if spec.RemainingDuration != nil {
		remaining = spec.RemainingDuration
	}
	cost := exCost
	if spec.ClearCost {
		cost = nil
	} else if spec.CostLeft != nil {
		cost = spec.CostLeft
	}
	session := exSession
	if spec.ClearMinSessionDuration {
		session = nil
	} else if spec.MinSessionDuration != nil {
		session = spec.MinSessionDuration
	}
	total := exTotal
	if spec.ClearTotalDuration {
		total = nil
	} else if spec.TotalDuration != nil {
		total = spec.TotalDuration
	}

	ongoing := false
	if existing != nil {
		ongoing = existing.Ongoing
	}
	if spec.Ongoing != nil {
		ongoing = *spec.Ongoing
	}
	candidate := &model.Item{Ongoing: ongoing, ID: id, Title: title, Kind: kind, Status: status, Category: cat, Type: typ, Notes: notes, URL: urlv, LegacyID: legacy, Deadline: deadline, RemainingDuration: remaining, CostLeft: cost, MinSessionDuration: session, TotalDuration: total, Ratings: map[model.Property]model.Rating{}, Tags: []string{}, Modes: []string{}, Engagements: []string{}, Dependencies: []string{}}
	if existing != nil {
		candidate.CreatedAt = existing.CreatedAt
		candidate.UpdatedAt = existing.UpdatedAt
		candidate.Tags = append(candidate.Tags, existing.Tags...)
		candidate.Modes = append(candidate.Modes, existing.Modes...)
		candidate.Engagements = append(candidate.Engagements, existing.Engagements...)
		candidate.Dependencies = append(candidate.Dependencies, existing.Dependencies...)
		for p, r := range existing.Ratings {
			candidate.Ratings[p] = r
		}
	}
	for _, p := range spec.UnsetRatings {
		if !p.IsRating() {
			return "", fmt.Errorf("invalid rating %s", p)
		}
		delete(candidate.Ratings, p)
	}
	for p, r := range spec.SetRatings {
		candidate.Ratings[p] = r
	}
	if spec.Tags != nil {
		candidate.Tags = dedup(*spec.Tags)
	}
	if spec.Modes != nil {
		candidate.Modes = dedup(*spec.Modes)
	}
	if spec.Engagements != nil {
		candidate.Engagements = dedup(*spec.Engagements)
	}
	if spec.DependsOn != nil {
		candidate.Dependencies = dedup(*spec.DependsOn)
	}
	if err := candidate.Validate(); err != nil {
		return "", err
	}
	if existing != nil && reflect.DeepEqual(candidate, existing) {
		return id, nil
	}

	if creating {
		if id, err = insertGenerated(tx, newItemID, `INSERT INTO items(`+itemColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			legacy, title, string(kind), string(status),
			nNullStr(cat), nNullStr(typ), nNullStr(notes), nNullStr(urlv),
			now, now, nNullTime(deadline), remaining, cost, session, total, ongoing,
		); err != nil {
			return "", fmt.Errorf("store: insert item: %w", err)
		}
	} else {
		if _, err := tx.Exec(`UPDATE items SET title=?, kind=?, status=?, category=?, type=?, notes=?, url=?,
			legacy_id=?, deadline=?, remaining_duration=?, cost_left=?, min_session_duration=?, total_duration=?, ongoing=?, updated_at=? WHERE id=?`,
			title, string(kind), string(status), nNullStr(cat), nNullStr(typ), nNullStr(notes), nNullStr(urlv),
			legacy, nNullTime(deadline), remaining, cost, session, total, ongoing, now, id,
		); err != nil {
			return "", fmt.Errorf("store: update item %s: %w", id, err)
		}
	}

	// A status change records the same event the matching transition verb
	// writes (started / completed / dropped); not_started records none, like
	// reopen. On create the baseline is not_started.
	if status != exStatus {
		if ev := StatusEvent(status); ev != "" {
			at, _ := parseTime(now)
			if _, err := s.appendEventTx(tx, model.Event{ItemID: &id, Type: ev, At: at}); err != nil {
				return "", err
			}
		}
	}

	// Ratings: delete unset rows, upsert set rows.
	for _, p := range spec.UnsetRatings {
		if _, err := tx.Exec(`DELETE FROM ratings WHERE item_id=? AND property=?`, id, string(p)); err != nil {
			return "", err
		}
	}
	for p, r := range spec.SetRatings {
		ratedAt := nNullTime(r.RatedAt)
		if _, err := tx.Exec(`INSERT INTO ratings(item_id, property, value, rated_at) VALUES(?,?,?,?)
			ON CONFLICT(item_id, property) DO UPDATE SET value=excluded.value, rated_at=excluded.rated_at`,
			id, string(p), r.Value, ratedAt); err != nil {
			return "", err
		}
	}

	// Tags: full replacement when provided.
	if spec.Tags != nil {
		if _, err := tx.Exec(`DELETE FROM item_tags WHERE item_id=?`, id); err != nil {
			return "", err
		}
		for _, t := range *spec.Tags {
			if t == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO item_tags(item_id, tag) VALUES(?,?)`, id, normalizeTag(t)); err != nil {
				return "", err
			}
		}
	}

	// Modes: full replacement when provided.
	if spec.Modes != nil {
		if _, err := tx.Exec(`DELETE FROM item_modes WHERE item_id=?`, id); err != nil {
			return "", err
		}
		for _, m := range *spec.Modes {
			if m == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO item_modes(item_id, mode) VALUES(?,?)`, id, m); err != nil {
				return "", err
			}
		}
	}

	// Engagements: full replacement when provided.
	if spec.Engagements != nil {
		if _, err := tx.Exec(`DELETE FROM item_engagements WHERE item_id=?`, id); err != nil {
			return "", err
		}
		for _, e := range *spec.Engagements {
			if e == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO item_engagements(item_id, engagement) VALUES(?,?)`, id, e); err != nil {
				return "", err
			}
		}
	}

	// Dependencies: full replacement when provided, with endpoint-existence and
	// cycle checks.
	if spec.DependsOn != nil {
		for _, dep := range *spec.DependsOn {
			if dep == id {
				return "", fmt.Errorf("%w: %s: self-dependency is not allowed", ErrCycle, id)
			}
			if _, err := s.getItemTx(tx, dep); err != nil {
				return "", err
			}
			if err := s.checkCycle(tx, id, dep); err != nil {
				return "", err
			}
		}
		if _, err := tx.Exec(`DELETE FROM dependencies WHERE item_id=?`, id); err != nil {
			return "", err
		}
		for _, dep := range *spec.DependsOn {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO dependencies(item_id, depends_on) VALUES(?,?)`, id, dep); err != nil {
				return "", err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// GetItem loads a single item with its ratings, tags, and dependencies.
func (s *Store) GetItem(id string) (*model.Item, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	it, err := s.getItemTx(tx, id)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// AllItems loads every item with its children. For the expected dataset size
// (~1k rows) this is simpler and correct than N+1 queries; the filter package
// then narrows the set in SQL.
func (s *Store) AllItems() ([]*model.Item, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT ` + itemColumns + ` FROM items ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadChildrenTx(tx, it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) getItemTx(tx *sql.Tx, id string) (*model.Item, error) {
	row := tx.QueryRow(`SELECT `+itemColumns+` FROM items WHERE id=?`, id)
	it, err := scanItemRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &ItemNotFoundError{ID: id}
		}
		return nil, err
	}
	if err := s.loadChildrenTx(tx, it); err != nil {
		return nil, err
	}
	return it, nil
}

// ItemNotFoundError is an INVALID_ID condition for a single item.
type ItemNotFoundError struct{ ID string }

func (e *ItemNotFoundError) Error() string { return "no item matches id or prefix " + e.ID }
func (e *ItemNotFoundError) Unwrap() error { return ErrInvalidID }

// ItemNotFound reports whether err is an unknown-item failure.
func ItemNotFound(err error) bool {
	var nf *ItemNotFoundError
	return errors.As(err, &nf)
}

func (s *Store) loadChildrenTx(tx *sql.Tx, it *model.Item) error {
	// Ratings.
	rows, err := tx.Query(`SELECT property, value, rated_at FROM ratings WHERE item_id=?`, it.ID)
	if err != nil {
		return err
	}
	it.Ratings = map[model.Property]model.Rating{}
	for rows.Next() {
		var p string
		var v float64
		var ratedAt sql.NullString
		if err := rows.Scan(&p, &v, &ratedAt); err != nil {
			rows.Close()
			return err
		}
		r := model.Rating{Property: model.Property(p), Value: v}
		if ratedAt.Valid {
			if t, perr := parseTime(ratedAt.String); perr == nil {
				r.RatedAt = &t
			}
		}
		it.Ratings[model.Property(p)] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// Tags.
	trows, err := tx.Query(`SELECT tag FROM item_tags WHERE item_id=? ORDER BY tag`, it.ID)
	if err != nil {
		return err
	}
	it.Tags = []string{}
	for trows.Next() {
		var t string
		if err := trows.Scan(&t); err != nil {
			trows.Close()
			return err
		}
		it.Tags = append(it.Tags, t)
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		return err
	}
	trows.Close()

	// Modes.
	mrows, err := tx.Query(`SELECT mode FROM item_modes WHERE item_id=? ORDER BY mode`, it.ID)
	if err != nil {
		return err
	}
	it.Modes = []string{}
	for mrows.Next() {
		var m string
		if err := mrows.Scan(&m); err != nil {
			mrows.Close()
			return err
		}
		it.Modes = append(it.Modes, m)
	}
	if err := mrows.Err(); err != nil {
		mrows.Close()
		return err
	}
	mrows.Close()

	// Engagements.
	erows, err := tx.Query(`SELECT engagement FROM item_engagements WHERE item_id=? ORDER BY engagement`, it.ID)
	if err != nil {
		return err
	}
	it.Engagements = []string{}
	for erows.Next() {
		var e string
		if err := erows.Scan(&e); err != nil {
			erows.Close()
			return err
		}
		it.Engagements = append(it.Engagements, e)
	}
	if err := erows.Err(); err != nil {
		erows.Close()
		return err
	}
	erows.Close()

	// Dependencies (edges this item depends on).
	drows, err := tx.Query(`SELECT depends_on FROM dependencies WHERE item_id=? ORDER BY depends_on`, it.ID)
	if err != nil {
		return err
	}
	it.Dependencies = []string{}
	for drows.Next() {
		var d string
		if err := drows.Scan(&d); err != nil {
			drows.Close()
			return err
		}
		it.Dependencies = append(it.Dependencies, d)
	}
	if err := drows.Err(); err != nil {
		drows.Close()
		return err
	}
	drows.Close()
	return nil
}

// scanItem reads an item row from a *sql.Row.
func scanItemRow(row *sql.Row) (*model.Item, error) {
	return scanValues(
		row.Scan,
	)
}

// scanItem reads an item row from a *sql.Rows.
func scanItem(rows *sql.Rows) (*model.Item, error) {
	return scanValues(rows.Scan)
}

func scanValues(scan func(dest ...any) error) (*model.Item, error) {
	var (
		it        model.Item
		legacy    sql.NullInt64
		category  sql.NullString
		type_     sql.NullString
		notes     sql.NullString
		url       sql.NullString
		deadline  sql.NullString
		remaining sql.NullFloat64
		cost      sql.NullFloat64
		session   sql.NullFloat64
		total     sql.NullFloat64
		created   string
		updated   string
		kind      string
		status    string
	)
	if err := scan(
		&it.ID, &legacy, &it.Title, &kind, &status, &category, &type_, &notes, &url,
		&created, &updated, &deadline, &remaining, &cost, &session, &total, &it.Ongoing,
	); err != nil {
		return nil, err
	}
	it.Kind = model.Kind(kind)
	it.Status = model.Status(status)
	if legacy.Valid {
		v := legacy.Int64
		it.LegacyID = &v
	}
	if category.Valid {
		it.Category = &category.String
	}
	if type_.Valid {
		it.Type = &type_.String
	}
	if notes.Valid {
		it.Notes = &notes.String
	}
	if url.Valid {
		it.URL = &url.String
	}
	if deadline.Valid {
		if t, err := parseTime(deadline.String); err == nil {
			it.Deadline = &t
		}
	}
	if remaining.Valid {
		it.RemainingDuration = &remaining.Float64
	}
	if cost.Valid {
		it.CostLeft = &cost.Float64
	}
	if session.Valid {
		it.MinSessionDuration = &session.Float64
	}
	if total.Valid {
		it.TotalDuration = &total.Float64
	}
	it.CreatedAt, _ = parseTime(created)
	it.UpdatedAt, _ = parseTime(updated)
	it.Ratings = map[model.Property]model.Rating{}
	return &it, nil
}

// normalizeTag preserves user data exactly as typed.
func normalizeTag(t string) string {
	return t
}

func nNullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nNullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return toTimeStr(t)
}

func toTimeStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func dedup(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if v != "" && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	sort.Strings(out)
	return out
}
