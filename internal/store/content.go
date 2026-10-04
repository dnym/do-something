package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"dosomething/internal/config"
	"dosomething/internal/model"
	"dosomething/internal/paths"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"uuid"
)

// Row preserves SQL NULL independently of empty strings and zero.
type Row map[string]any
type Content struct {
	SchemaVersion int   `json:"schema_version"`
	Items         []Row `json:"items"`
	Ratings       []Row `json:"ratings"`
	Tags          []Row `json:"item_tags"`
	Modes         []Row `json:"item_modes"`
	Engagements   []Row `json:"item_engagements"`
	Dependencies  []Row `json:"dependencies"`
	Config        []Row `json:"config"`
	Events        []Row `json:"events"`
}
type Table struct {
	Name    string
	Columns []string
	Keys    int
	Rows    []Row
}

func (c Content) Tables() []Table {
	return []Table{
		{"items", strings.Split("id,legacy_id,title,kind,status,category,type,notes,url,created_at,updated_at,deadline,remaining_duration,cost_left,min_session_duration,total_duration,ongoing", ","), 1, c.Items},
		{"ratings", []string{"item_id", "property", "value", "rated_at"}, 2, c.Ratings},
		{"item_tags", []string{"item_id", "tag"}, 2, c.Tags},
		{"item_modes", []string{"item_id", "mode"}, 2, c.Modes},
		{"item_engagements", []string{"item_id", "engagement"}, 2, c.Engagements},
		{"dependencies", []string{"item_id", "depends_on"}, 2, c.Dependencies},
		{"config", []string{"key", "value", "updated_at"}, 1, c.Config},
		{"events", []string{"id", "item_id", "item_ids", "type", "at", "query", "activity"}, 1, c.Events},
	}
}
func EmptyContent() Content {
	return Content{SchemaVersion: SchemaVersion, Items: []Row{}, Ratings: []Row{}, Tags: []Row{}, Modes: []Row{}, Engagements: []Row{}, Dependencies: []Row{}, Config: []Row{}, Events: []Row{}}
}
func (c *Content) SetTable(name string, rows []Row) {
	switch name {
	case "items":
		c.Items = rows
	case "ratings":
		c.Ratings = rows
	case "item_tags":
		c.Tags = rows
	case "item_modes":
		c.Modes = rows
	case "item_engagements":
		c.Engagements = rows
	case "dependencies":
		c.Dependencies = rows
	case "config":
		c.Config = rows
	case "events":
		c.Events = rows
	}
}
func RowKey(t Table, r Row) string {
	a := []any{}
	for _, k := range t.Columns[:t.Keys] {
		a = append(a, r[k])
	}
	b, _ := json.Marshal(a)
	return string(b)
}
func EqualRows(a, b Row) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func (s *Store) Content() (Content, error) { return readContent(s.db) }
func readContent(db *sql.DB) (Content, error) {
	c := EmptyContent()
	tx, e := db.Begin()
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	for _, t := range c.Tables() {
		rows, e := tx.Query(`SELECT "` + strings.Join(t.Columns, `","`) + `" FROM "` + t.Name + `"`)
		if e != nil {
			return c, e
		}
		out := []Row{}
		for rows.Next() {
			v := make([]any, len(t.Columns))
			p := make([]any, len(v))
			for i := range v {
				p[i] = &v[i]
			}
			if e = rows.Scan(p...); e != nil {
				rows.Close()
				return c, e
			}
			r := Row{}
			for i, k := range t.Columns {
				r[k] = v[i]
			}
			out = append(out, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return c, e
		}
		sort.Slice(out, func(i, j int) bool { return RowKey(t, out[i]) < RowKey(t, out[j]) })
		c.SetTable(t.Name, out)
	}
	return c, nil
}

// ReadSnapshot never enables WAL or writes to an inbound file.
func ReadSnapshot(path string) (Content, string, error) {
	db, e := openReadOnly(path)
	if e != nil {
		return Content{}, "", e
	}
	defer db.Close()
	if e = ValidateDB(db); e != nil {
		return Content{}, "", e
	}
	var id string
	if e = db.QueryRow("SELECT value FROM meta WHERE key='database_uuid'").Scan(&id); e != nil {
		return Content{}, "", e
	}
	c, e := readContent(db)
	if e == nil {
		e = c.Validate()
	}
	return c, id, e
}

// Canonical uses explicit types and length prefixes, including table/column identities.
func (c Content) Canonical(events bool) ([]byte, error) {
	var b bytes.Buffer
	put := func(s string) { _ = binary.Write(&b, binary.BigEndian, uint64(len(s))); b.WriteString(s) }
	put("do-something/content/1")
	for _, t := range c.Tables() {
		if t.Name == "events" && !events {
			continue
		}
		put(t.Name)
		for _, col := range t.Columns {
			put(col)
		}
		rows := append([]Row{}, t.Rows...)
		sort.Slice(rows, func(i, j int) bool { return RowKey(t, rows[i]) < RowKey(t, rows[j]) })
		_ = binary.Write(&b, binary.BigEndian, uint64(len(rows)))
		for _, r := range rows {
			for _, k := range t.Columns {
				v := r[k]
				if t.Name == "items" && k == "ongoing" && v == nil {
					v = int64(0)
				}
				if n, ok := v.(json.Number); ok {
					var e error
					if k == "legacy_id" || k == "ongoing" {
						v, e = n.Int64()
					} else {
						v, e = n.Float64()
					}
					if e != nil {
						return nil, e
					}
				}
				switch x := v.(type) {
				case nil:
					b.WriteByte(0)
				case string:
					b.WriteByte(1)
					put(x)
				case int64:
					b.WriteByte(2)
					_ = binary.Write(&b, binary.BigEndian, x)
				case float64:
					if math.IsNaN(x) || math.IsInf(x, 0) {
						return nil, fmt.Errorf("non-finite number")
					}
					b.WriteByte(3)
					if x == 0 {
						x = 0
					}
					_ = binary.Write(&b, binary.BigEndian, x)
				default:
					return nil, fmt.Errorf("unsupported SQL value %T", v)
				}
			}
		}
	}
	return b.Bytes(), nil
}
func (c Content) Digest(events bool) (string, error) {
	b, e := c.Canonical(events)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func DecodeContent(b []byte) (Content, error) {
	c := EmptyContent()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	e := d.Decode(&c)
	if e == nil {
		var extra any
		if d.Decode(&extra) != io.EOF {
			return c, fmt.Errorf("trailing JSON content")
		}
		for _, row := range c.Items {
			if row["ongoing"] == nil {
				row["ongoing"] = int64(0)
			}
		}
		e = c.Validate()
	}
	return c, e
}
func (c Content) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return ErrSchemaMismatch
	}
	items := map[string]Row{}
	edges := map[string][]string{}
	legacyIDs := map[string]bool{}
	for _, t := range c.Tables() {
		seen := map[string]bool{}
		for _, r := range t.Rows {
			key := RowKey(t, r)
			if seen[key] {
				return fmt.Errorf("duplicate %s key %s", t.Name, key)
			}
			seen[key] = true
			for _, k := range t.Columns[:t.Keys] {
				s, ok := r[k].(string)
				if !ok || s == "" {
					return fmt.Errorf("invalid %s.%s", t.Name, k)
				}
			}
			for k := range r {
				found := false
				for _, col := range t.Columns {
					if k == col {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("unknown %s.%s", t.Name, k)
				}
			}
			for _, k := range t.Columns {
				if r[k] == nil {
					continue
				}
				numeric := t.Name == "items" && (k == "ongoing" || k == "legacy_id" || k == "remaining_duration" || k == "cost_left" || k == "min_session_duration" || k == "total_duration") || t.Name == "ratings" && k == "value"
				if numeric {
					if _, e := Number(r[k]); e != nil {
						return fmt.Errorf("invalid %s.%s: %w", t.Name, k, e)
					}
				} else {
					if _, ok := r[k].(string); !ok {
						return fmt.Errorf("%s.%s must be text or null", t.Name, k)
					}
				}
			}
			switch t.Name {
			case "items":
				id := r["id"].(string)
				if _, e := uuid.Parse(id); e != nil {
					return e
				}
				title, _ := r["title"].(string)
				kind, _ := r["kind"].(string)
				status, _ := r["status"].(string)
				if title == "" || !model.Kind(kind).Valid() || !model.Status(status).Valid() {
					return fmt.Errorf("invalid item %s", id)
				}
				for _, k := range []string{"created_at", "updated_at", "deadline"} {
					if r[k] == nil && k == "deadline" {
						continue
					}
					s, ok := r[k].(string)
					if !ok {
						return fmt.Errorf("invalid %s", k)
					}
					if _, e := parseTime(s); e != nil {
						return e
					}
				}
				for _, k := range []string{"remaining_duration", "cost_left", "min_session_duration", "total_duration"} {
					if r[k] != nil {
						n, e := Number(r[k])
						if e != nil || n < 0 {
							return fmt.Errorf("invalid %s", k)
						}
					}
				}
				if r["ongoing"] != nil {
					var ongoing int64
					var err error
					switch v := r["ongoing"].(type) {
					case int64:
						ongoing = v
					case json.Number:
						ongoing, err = v.Int64()
					default:
						err = fmt.Errorf("ongoing must be an integer")
					}
					if err != nil || (ongoing != 0 && ongoing != 1) {
						return fmt.Errorf("ongoing must be 0 or 1")
					}
					if ongoing == 1 && (r["total_duration"] != nil || r["remaining_duration"] != nil) {
						return &model.OngoingDurationError{}
					}
				}
				if r["legacy_id"] != nil {
					var key string
					switch n := r["legacy_id"].(type) {
					case int64:
						key = fmt.Sprint(n)
					case json.Number:
						v, e := n.Int64()
						if e != nil {
							return e
						}
						key = fmt.Sprint(v)
					default:
						return fmt.Errorf("legacy_id must be an integer")
					}
					if legacyIDs[key] {
						return fmt.Errorf("duplicate legacy_id %s", key)
					}
					legacyIDs[key] = true
				}
				items[id] = r
			case "events":
				if _, e := uuid.Parse(r["id"].(string)); e != nil {
					return e
				}
				s, _ := r["at"].(string)
				if _, e := time.Parse(time.RFC3339Nano, s); e != nil {
					return e
				}
				switch r["type"] {
				case "suggested", "started", "completed", "dropped", "logged":
				default:
					return fmt.Errorf("invalid event type")
				}
				if r["type"] == "logged" {
					if r["item_id"] == nil || r["item_id"] == "" {
						return fmt.Errorf("logged event requires item_id")
					}
					raw, ok := r["activity"].(string)
					if !ok {
						return fmt.Errorf("logged event requires activity")
					}
					if _, err := model.DecodeLoggedActivity(raw); err != nil {
						return err
					}
				} else if r["activity"] != nil {
					return fmt.Errorf("activity is only valid for logged events")
				}
				if r["item_ids"] != nil {
					var ids []string
					if e := json.Unmarshal([]byte(r["item_ids"].(string)), &ids); e != nil {
						return fmt.Errorf("invalid event candidate ids: %w", e)
					}
				}
				if r["query"] != nil {
					var query map[string]any
					if e := json.Unmarshal([]byte(r["query"].(string)), &query); e != nil {
						return fmt.Errorf("invalid event query: %w", e)
					}
				}
				for _, k := range []string{"item_ids", "query"} {
					if r[k] != nil {
						s, ok := r[k].(string)
						if !ok || !json.Valid([]byte(s)) {
							return fmt.Errorf("invalid event %s", k)
						}
					}
				}
			case "config":
				if r["updated_at"] != nil {
					if _, e := parseTime(r["updated_at"].(string)); e != nil {
						return e
					}
				}
				if _, ok := r["value"].(string); !ok {
					return fmt.Errorf("invalid config value")
				}
				if e := config.ValidateValue(r["key"].(string), r["value"].(string)); e != nil {
					return e
				}
			case "item_modes":
				if !model.Mode(r["mode"].(string)).Valid() {
					return fmt.Errorf("invalid mode %s", r["mode"])
				}
			case "item_engagements":
				if !model.Engagement(r["engagement"].(string)).Valid() {
					return fmt.Errorf("invalid engagement %s", r["engagement"])
				}
			}
		}
	}
	for _, r := range c.Ratings {
		it := items[r["item_id"].(string)]
		p := model.Property(r["property"].(string))
		n, e := Number(r["value"])
		if it == nil || !p.IsRating() || !model.Kind(it["kind"].(string)).ApplicableProperty(p) || e != nil || n < 0 || n > 1 {
			return fmt.Errorf("invalid rating")
		}
		if r["rated_at"] != nil {
			if _, e := parseTime(fmt.Sprint(r["rated_at"])); e != nil {
				return e
			}
		}
	}
	for _, r := range c.Tags {
		if items[r["item_id"].(string)] == nil {
			return fmt.Errorf("unknown tag item")
		}
	}
	for _, r := range c.Modes {
		if items[r["item_id"].(string)] == nil {
			return fmt.Errorf("unknown mode item")
		}
	}
	for _, r := range c.Engagements {
		if items[r["item_id"].(string)] == nil {
			return fmt.Errorf("unknown engagement item")
		}
	}
	for _, r := range c.Dependencies {
		a, b := r["item_id"].(string), r["depends_on"].(string)
		if items[a] == nil || items[b] == nil {
			return fmt.Errorf("unknown dependency endpoint")
		}
		edges[a] = append(edges[a], b)
	}
	visited := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] == 1 {
			return ErrCycle
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		for _, dep := range edges[id] {
			if e := visit(dep); e != nil {
				return e
			}
		}
		visited[id] = 2
		return nil
	}
	for id := range items {
		if e := visit(id); e != nil {
			return e
		}
	}
	return nil
}
func Number(v any) (float64, error) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int64:
		n = float64(x)
	case json.Number:
		var e error
		n, e = x.Float64()
		if e != nil {
			return 0, e
		}
	default:
		return 0, fmt.Errorf("not a number")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("non-finite number")
	}
	return n, nil
}

// ApplyContent installs a validated resolved state in one WAL transaction. Readers
// retain their snapshots; database identity is never rewritten.
func (s *Store) ApplyContent(c Content) error { return s.applyContent(c, "") }
func (s *Store) applyContent(c Content, identity string) error {
	if e := c.Validate(); e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, name := range []string{"ratings", "item_tags", "item_modes", "item_engagements", "dependencies", "items", "config", "events"} {
		if _, e = tx.Exec(`DELETE FROM "` + name + `"`); e != nil {
			return e
		}
	}
	for _, t := range c.Tables() {
		for _, r := range t.Rows {
			args := make([]any, len(t.Columns))
			for i, k := range t.Columns {
				v := r[k]
				if t.Name == "items" && k == "ongoing" && v == nil {
					v = int64(0)
				}
				if n, ok := v.(json.Number); ok {
					if k == "legacy_id" || k == "ongoing" {
						v, e = n.Int64()
					} else {
						v, e = n.Float64()
					}
					if e != nil {
						return e
					}
				}
				args[i] = v
			}
			q := `INSERT INTO "` + t.Name + `" ("` + strings.Join(t.Columns, `","`) + `") VALUES (` + strings.TrimRight(strings.Repeat("?,", len(args)), ",") + `)`
			if _, e = tx.Exec(q, args...); e != nil {
				return e
			}
		}
	}
	if identity != "" {
		if _, e = tx.Exec("UPDATE meta SET value=? WHERE key='database_uuid'", identity); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Acquire locks even a not-yet-created store; initialization and bootstrap must
// occur under the same lock as subsequent mutation and publication.
func Acquire(opts Options) (func(), error) {
	path, e := paths.Canonical(opts.DBPath)
	if e != nil {
		return nil, e
	}
	opts.DBPath = path
	s := &Store{dbPath: opts.DBPath, stateDir: opts.StateDir}
	if s.stateDir == "" {
		s.stateDir = filepath.Dir(opts.DBPath)
	}
	if e := s.Lock(); e != nil {
		return nil, e
	}
	return s.Unlock, nil
}

// CaptureSnapshot obtains a consistent private copy without altering the source.
func CaptureSnapshot(source, target string) error {
	db, e := openReadOnly(source)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = ValidateDB(db); e != nil {
		return e
	}
	s := &Store{db: db}
	return s.VacuumInto(target)
}
