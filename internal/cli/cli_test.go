package cli

import (
	"bytes"
	"dosomething/internal/model"
	"dosomething/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func invoke(t *testing.T, db string, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errout bytes.Buffer
	in, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	argv := append([]string{"--db", db, "--format", "json", "--no-input"}, args...)
	code := Run(argv, in, &out, &errout)
	var v map[string]any
	if out.Len() > 0 {
		if e = json.Unmarshal(out.Bytes(), &v); e != nil {
			t.Fatalf("invalid JSON output %s: %v", out.String(), e)
		}
	}
	return code, v, errout.String()
}
func success(t *testing.T, db string, args ...string) map[string]any {
	t.Helper()
	code, v, err := invoke(t, db, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, err)
	}
	return v
}
func TestCLIWorkflow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	code, _, err := invoke(t, db, "list")
	if code != 1 || !bytes.Contains([]byte(err), []byte("NO_STORE")) {
		t.Fatalf("%d %s", code, err)
	}
	schema := success(t, db, "schema")
	if schema["schema_version"] != float64(1) {
		t.Fatal(schema)
	}
	if _, e := os.Stat(db); !os.IsNotExist(e) {
		t.Fatal("schema created store")
	}
	a := success(t, db, "add", "--title", "A book", "--kind", "media", "--type", "book", "--quality", "1", "--interest", "0.5", "--tag", "CaseSensitive")
	id := a["id"].(string)
	r := success(t, db, "--peek", "--type", "book", "--kind", "media")
	items := r["items"].([]any)
	if len(items) != 1 || r["recorded"] != false {
		t.Fatal(r)
	}
	it := items[0].(map[string]any)
	states := it["property_states"].(map[string]any)
	if states["career"] != "not_applicable" || states["intensity"] != "unknown" {
		t.Fatal(states)
	}
	r = success(t, db, "--effort", "low", "--kind", "media", "--peek")
	if len(r["items"].([]any)) != 1 {
		t.Fatal(r)
	}
	r = success(t, db, "--effort", "low", "--kind", "media", "--unknown", "exclude", "--peek")
	if len(r["items"].([]any)) != 0 {
		t.Fatal(r)
	}
	r = success(t, db, "suggest")
	if r["recorded"] != true {
		t.Fatal(r)
	}
	success(t, db, "edit", id, "--unset-rating", "quality", "--clear-min-session-duration")
	r = success(t, db, "show", id)
	states = r["items"].([]any)[0].(map[string]any)["property_states"].(map[string]any)
	if states["quality"] != "unknown" || states["min_session_duration"] != "unknown" {
		t.Fatal(states)
	}
	success(t, db, "done", id)
	r = success(t, db, "done", id)
	if r["changed"] != false {
		t.Fatal(r)
	}
	code, _, _ = invoke(t, db, "--peek", "--fail-empty")
	if code != 3 {
		t.Fatalf("empty code=%d", code)
	}
	r = success(t, db, "list")
	if len(r["items"].([]any)) != 1 {
		t.Fatal(r)
	}
	// Activity modes: stored on the item, reported in the query, a soft
	// re-rank (never an exclusion), and clearable.
	w := success(t, db, "add", "--title", "Workout", "--kind", "project", "--modes", "movement", "--modes", "hands_on", "--engagements", "focused")
	wid := w["id"].(string)
	r = success(t, db, "list", "--mode", "movement", "--engagement", "focused", "--kind", "both", "--explain", "--peek")
	items = r["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("mode must re-rank, never exclude: %v", items)
	}
	var witem, bitem map[string]any
	for _, x := range items {
		m := x.(map[string]any)
		if m["id"] == wid {
			witem = m
		} else {
			bitem = m
		}
	}
	if witem == nil || bitem == nil {
		t.Fatal(items)
	}
	modes := witem["modes"].([]any)
	if len(modes) != 2 || modes[0] != "hands_on" || modes[1] != "movement" {
		t.Fatalf("item modes = %v", modes)
	}
	if bm := bitem["modes"].([]any); len(bm) != 0 {
		t.Fatalf("unclassified item modes = %v", bm)
	}
	if fm := r["filters"].(map[string]any)["mode"]; fm != "movement" {
		t.Fatalf("reported query mode = %v", fm)
	}
	if fe := r["filters"].(map[string]any)["engagement"]; fe != "focused" {
		t.Fatalf("reported query engagement = %v", fe)
	}
	termV := func(it map[string]any, name string) (float64, bool) {
		bd, ok := it["score_breakdown"].(map[string]any)
		if !ok {
			return 0, false
		}
		terms, _ := bd["terms"].([]any)
		for _, x := range terms {
			term := x.(map[string]any)
			if term["name"] == name {
				return term["v"].(float64), true
			}
		}
		return 0, false
	}
	if v, ok := termV(witem, "mode_match"); !ok || v != 1 {
		t.Fatalf("workout mode_match = %v (present=%v), want 1", v, ok)
	}
	if v, ok := termV(bitem, "mode_match"); !ok || v != 0.5 {
		t.Fatalf("book mode_match = %v (present=%v), want 0.5 (unclassified)", v, ok)
	}
	if v, ok := termV(witem, "engagement_match"); !ok || v != 1 {
		t.Fatalf("workout engagement_match = %v (present=%v), want 1", v, ok)
	}
	if v, ok := termV(bitem, "engagement_match"); !ok || v != 0.5 {
		t.Fatalf("book engagement_match = %v (present=%v), want 0.5 (unclassified)", v, ok)
	}
	// An invalid mode is an invalid argument.
	code, _, err = invoke(t, db, "--mode", "zen", "--peek")
	if code != 2 || !bytes.Contains([]byte(err), []byte("INVALID_ARGUMENT")) {
		t.Fatalf("%d %s", code, err)
	}
	// An invalid engagement is an invalid argument.
	code, _, err = invoke(t, db, "--engagement", "zen", "--peek")
	if code != 2 || !bytes.Contains([]byte(err), []byte("INVALID_ARGUMENT")) {
		t.Fatalf("%d %s", code, err)
	}
	// --clear-modes / --clear-engagements empty the sets; the item then ranks
	// as unclassified on both axes.
	success(t, db, "edit", wid, "--clear-modes", "--clear-engagements")
	r = success(t, db, "list", "--mode", "movement", "--engagement", "focused", "--kind", "both", "--explain", "--peek")
	items = r["items"].([]any)
	for _, x := range items {
		m := x.(map[string]any)
		if m["id"] != wid {
			continue
		}
		if ms := m["modes"].([]any); len(ms) != 0 {
			t.Fatalf("modes after clear = %v", ms)
		}
		if es := m["engagements"].([]any); len(es) != 0 {
			t.Fatalf("engagements after clear = %v", es)
		}
		if v, _ := termV(m, "mode_match"); v != 0.5 {
			t.Fatalf("mode_match after clear = %v, want 0.5", v)
		}
		if v, _ := termV(m, "engagement_match"); v != 0.5 {
			t.Fatalf("engagement_match after clear = %v, want 0.5", v)
		}
	}
	success(t, db, "doctor")
}
func TestSuggestKindDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	success(t, db, "add", "--title", "P", "--kind", "project")
	success(t, db, "add", "--title", "M", "--kind", "media")
	kinds := func(r map[string]any) []string {
		items := r["items"].([]any)
		ks := make([]string, len(items))
		for i, it := range items {
			ks[i] = it.(map[string]any)["kind"].(string)
		}
		return ks
	}
	// suggest defaults to projects and reports the applied default in filters.
	r := success(t, db, "--peek")
	if k := r["filters"].(map[string]any)["kind"]; k != "project" {
		t.Fatal(k)
	}
	if got := kinds(r); !slices.Equal(got, []string{"project"}) {
		t.Fatal(got)
	}
	// --kind both ranks projects and media in one list.
	r = success(t, db, "--peek", "--kind", "both")
	if k := r["filters"].(map[string]any)["kind"]; k != "both" {
		t.Fatal(k)
	}
	if got := kinds(r); !slices.Equal(got, []string{"project", "media"}) {
		t.Fatal(got)
	}
	// list shows every kind.
	r = success(t, db, "list")
	if got := kinds(r); !slices.Equal(got, []string{"project", "media"}) {
		t.Fatal(got)
	}
}
func TestCLINoninteractiveAndAtomicStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	code, _, _ := invoke(t, db, "add")
	if code != 2 {
		t.Fatal(code)
	}
	if _, e := os.Stat(db); !os.IsNotExist(e) {
		t.Fatal("invalid add created store")
	}
	a := success(t, db, "add", "--title", "task", "--kind", "project")
	id := a["id"].(string)
	code, _, _ = invoke(t, db, "done", id, "missing")
	if code != 2 {
		t.Fatal(code)
	}
	r := success(t, db, "show", id)
	if r["items"].([]any)[0].(map[string]any)["status"] != "not_started" {
		t.Fatal(r)
	}
}

// TestCLIAddEditStatus: add/edit set the status directly. A change records the
// same event as the matching status command (started / completed / dropped);
// not_started records none. Done items leave suggest; a repeated no-op edit
// writes no second event.
func TestCLIAddEditStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	doneID := success(t, db, "add", "--title", "Old film", "--kind", "media", "--status", "done")["id"].(string)
	wipID := success(t, db, "add", "--title", "In flight", "--kind", "project", "--status", "in_progress")["id"].(string)
	freshID := success(t, db, "add", "--title", "Backlog", "--kind", "project")["id"].(string)

	evCount := func(itemID, typ string) int {
		n := 0
		for _, raw := range success(t, db, "export", "json")["events"].([]any) {
			ev := raw.(map[string]any)
			if ev["type"] == typ && ev["item_id"] == itemID {
				n++
			}
		}
		return n
	}
	if evCount(doneID, "completed") != 1 {
		t.Fatalf("add --status done must record completed: %d", evCount(doneID, "completed"))
	}
	if evCount(wipID, "started") != 1 {
		t.Fatalf("add --status in_progress must record started: %d", evCount(wipID, "started"))
	}
	if n := evCount(freshID, "started") + evCount(freshID, "completed") + evCount(freshID, "dropped"); n != 0 {
		t.Fatalf("default add must record no status event: %d", n)
	}
	// Done items leave suggest; not_started and in_progress remain.
	r := success(t, db, "suggest", "--kind", "both", "--peek")
	ids := map[string]bool{}
	for _, raw := range r["items"].([]any) {
		ids[raw.(map[string]any)["id"].(string)] = true
	}
	if !ids[wipID] || !ids[freshID] || ids[doneID] {
		t.Fatalf("suggest ids: %v", ids)
	}
	// edit --status records the event; repeating it is a no-op without a
	// duplicate event.
	r = success(t, db, "edit", wipID, "--status", "done")
	if r["changed"] != true {
		t.Fatal(r)
	}
	if evCount(wipID, "completed") != 1 {
		t.Fatalf("edit --status done must record completed: %d", evCount(wipID, "completed"))
	}
	r = success(t, db, "edit", wipID, "--status", "done")
	if r["changed"] != false {
		t.Fatal(r)
	}
	if evCount(wipID, "completed") != 1 {
		t.Fatalf("no-op edit must not duplicate the event: %d", evCount(wipID, "completed"))
	}
	// Back to not_started changes status but records no event, like reopen.
	r = success(t, db, "edit", doneID, "--status", "not_started")
	if r["changed"] != true {
		t.Fatal(r)
	}
	if evCount(doneID, "started") != 0 || evCount(doneID, "completed") != 1 {
		t.Fatalf("reopen-like edit must record no new event: %d %d", evCount(doneID, "started"), evCount(doneID, "completed"))
	}
}

// TestCLIDepsTitleReferences verifies that dependency references accept
// exact titles: the deps command (target and prerequisites), --depends-on
// (comma-separated, backslash-escaped commas), id-over-title priority, and
// the ambiguous-title failure with candidates.
func TestCLIDepsTitleReferences(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	p1 := success(t, db, "add", "--title", "Prepare the stage", "--kind", "project")["id"].(string)
	p2 := success(t, db, "add", "--title", "Paint the set", "--kind", "project")["id"].(string)
	show := success(t, db, "add", "--title", "Perform the show", "--kind", "project")["id"].(string)

	// Multiple prerequisites as separate arguments, all by title.
	r := success(t, db, "deps", "add", "Perform the show", "Prepare the stage", "Paint the set")
	if r["id"] != show || r["changed"] != true {
		t.Fatalf("deps add by title: %v", r)
	}
	after := r["after"].([]any)
	if len(after) != 2 || after[0] != p1 || after[1] != p2 {
		t.Fatalf("after: %v (want %v %v)", after, p1, p2)
	}
	// Re-adding an existing prerequisite is a no-op.
	r = success(t, db, "deps", "add", "Perform the show", "Prepare the stage")
	if r["changed"] != false {
		t.Fatal(r)
	}
	// A title that is also an existing item's id resolves as the id.
	_ = success(t, db, "add", "--title", show, "--kind", "project")
	r = success(t, db, "deps", "list", show)
	if r["id"] != show {
		t.Fatalf("id must win over a lookalike title: %v", r)
	}
	// An ambiguous title fails INVALID_ID and lists its candidates.
	success(t, db, "add", "--title", "Twin", "--kind", "project")
	success(t, db, "add", "--title", "Twin", "--kind", "project")
	code, _, errout := invoke(t, db, "deps", "add", "Perform the show", "Twin")
	if code != 2 {
		t.Fatalf("ambiguous title: exit %d: %s", code, errout)
	}
	var body map[string]any
	if e := json.Unmarshal([]byte(errout), &body); e != nil {
		t.Fatalf("error JSON: %v: %s", e, errout)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "INVALID_ID" {
		t.Fatalf("error: %v", errBody)
	}
	details, _ := errBody["details"].(map[string]any)
	if details["title"] != "Twin" {
		t.Fatalf("details: %v", details)
	}
	if cands := details["candidates"].([]any); len(cands) != 2 {
		t.Fatalf("candidates: %v", cands)
	}
	// The failed add must not have changed the graph.
	r = success(t, db, "deps", "list", "Perform the show")
	if got := r["after"].([]any); len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("graph after failed add: %v", got)
	}
	// deps remove by title.
	r = success(t, db, "deps", "remove", "Perform the show", "Paint the set")
	if r["changed"] != true || len(r["after"].([]any)) != 1 || r["after"].([]any)[0] != p1 {
		t.Fatal(r)
	}
	// --depends-on: comma-separated titles, one value per flag use and one
	// combined value; a comma inside a title is escaped with a backslash.
	weird := success(t, db, "add", "--title", `Weird, title`, "--kind", "project")["id"].(string)
	z := success(t, db, "add", "--title", "Combine", "--kind", "project", "--depends-on", "Prepare the stage,Paint the set")
	zid := z["id"].(string)
	r = success(t, db, "deps", "list", zid)
	if got := r["after"].([]any); len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("combined --depends-on: %v", got)
	}
	weirdRef := `Weird\, title`
	w := success(t, db, "add", "--title", "Escaped", "--kind", "project", "--depends-on", weirdRef)
	r = success(t, db, "deps", "list", w["id"].(string))
	if got := r["after"].([]any); len(got) != 1 || got[0] != weird {
		t.Fatalf("escaped comma: %v", got)
	}
	// A reference matching no id, prefix, or title fails INVALID_ID.
	code, _, errout = invoke(t, db, "deps", "add", "Perform the show", "No such thing")
	if code != 2 || !bytes.Contains([]byte(errout), []byte("INVALID_ID")) {
		t.Fatalf("unknown reference: exit %d: %s", code, errout)
	}
}

// TestCLIBlockedByTitle: suggest and list report each blocker's title in
// readiness.blocked_by. The query path builds blockers from the in-memory
// item map; a missing title there yields the silent 'blocked by ""' line.
func TestCLIBlockedByTitle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	pre := success(t, db, "add", "--title", "Order materials", "--kind", "project")["id"].(string)
	dep := success(t, db, "add", "--title", "Build a bookshelf", "--kind", "project", "--depends-on", "Order materials")["id"].(string)

	check := func(command string, args ...string) {
		r := success(t, db, append([]string{command, "--include-unready", "--peek"}, args...)...)
		found := false
		for _, raw := range r["items"].([]any) {
			it := raw.(map[string]any)
			if it["id"] != dep {
				continue
			}
			found = true
			readiness := it["readiness"].(map[string]any)
			blocked, _ := readiness["blocked_by"].([]any)
			if len(blocked) != 1 {
				t.Fatalf("%s: blocked_by %v", command, readiness)
			}
			b := blocked[0].(map[string]any)
			if b["id"] != pre || b["title"] != "Order materials" || b["status"] != "not_started" {
				t.Fatalf("%s: blocker %v", command, b)
			}
		}
		if !found {
			t.Fatalf("%s: dependent item missing from %v", command, r["items"])
		}
	}
	check("suggest")
	check("list")
}

func TestCLIJSONRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	db := filepath.Join(dir, "a.db")
	prerequisite := success(t, db, "add", "--title", "Preparation", "--kind", "project")["id"].(string)
	success(t, db, "add", "--title", "film", "--kind", "media", "--type", "film", "--quality", "1", "--tag", "watch", "--depends-on", prerequisite)
	success(t, db, "config", "set", "scoring.k.cost_left", "250")
	success(t, db, "suggest")
	export := success(t, db, "export", "json")
	b, e := json.Marshal(export)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "backup.json")
	if e = os.WriteFile(file, b, 0600); e != nil {
		t.Fatal(e)
	}
	other := filepath.Join(dir, "b.db")
	success(t, other, "import", file)
	left, e := store.Open(store.OpenRead, store.Options{DBPath: db})
	if e != nil {
		t.Fatal(e)
	}
	defer left.Close()
	right, e := store.Open(store.OpenRead, store.Options{DBPath: other})
	if e != nil {
		t.Fatal(e)
	}
	defer right.Close()
	l, _ := left.Content()
	r, _ := right.Content()
	ld, _ := l.Digest(true)
	rd, _ := r.Digest(true)
	if ld != rd {
		t.Fatalf("round trip changed content: %s %s", ld, rd)
	}
	rep := success(t, other, "import", file)
	if rep["changed"] != false {
		t.Fatal(rep)
	}
}

func TestCLIJSONImportStdinAndValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	success(t, source, "add", "--title", "Example", "--kind", "project")
	exported := success(t, source, "export", "json")
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	// A canonical JSON import does not depend on a filename extension.
	file := filepath.Join(dir, "backup")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.db")
	rep := success(t, target, "import", file, "--dry-run")
	if rep["added"] != float64(1) || rep["dry_run"] != true {
		t.Fatal(rep)
	}
	in, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	var out, errout bytes.Buffer
	code := Run([]string{"--db", target, "--format", "json", "--no-input", "import", "-"}, in, &out, &errout)
	if code != 0 {
		t.Fatalf("stdin import: exit %d: %s", code, errout.String())
	}
	if err = json.Unmarshal(out.Bytes(), &rep); err != nil || rep["added"] != float64(1) || rep["dry_run"] != false {
		t.Fatal(rep, err)
	}
	before, err := json.Marshal(success(t, target, "export", "json"))
	if err != nil {
		t.Fatal(err)
	}
	// Include a valid new item before an invalid row to catch partial application.
	success(t, source, "add", "--title", "New", "--kind", "project")
	invalid := success(t, source, "export", "json")
	invalid["items"] = append(invalid["items"].([]any), map[string]any{"title": "Missing required fields"})
	invalidData, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"tsv":               []byte("title\tkind\nExample\tproject\n"),
		"flat JSON":         []byte(`[{"title":"Example","kind":"project"}]`),
		"invalid document":  invalidData,
		"trailing document": append(append([]byte{}, data...), data...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(file, input, 0600); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := invoke(t, target, "import", file)
			if code != 2 || !bytes.Contains([]byte(stderr), []byte("INVALID_ARGUMENT")) {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			after, err := json.Marshal(success(t, target, "export", "json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected import changed content", err)
			}
		})
	}
}

func TestCLIBootstrapIdentityAndDryRuns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	success(t, source, "add", "--title", "A", "--kind", "project")
	snapshot := filepath.Join(dir, "peer.db")
	success(t, source, "snapshot", snapshot)
	target := filepath.Join(dir, "target.db")
	success(t, target, "sync", snapshot, "--take-remote", "--dry-run")
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("dry-run created working store")
	}
	success(t, target, "sync", snapshot, "--take-remote")
	_, sourceID, e := store.ReadSnapshot(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(store.OpenRead, store.Options{DBPath: target})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.DatabaseUUID()
	s.Close()
	if e != nil || id != sourceID {
		t.Fatal(id, sourceID, e)
	}
	backup := filepath.Join(dir, "backup.json")
	data, e := json.Marshal(success(t, source, "export", "json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(backup, data, 0600); e != nil {
		t.Fatal(e)
	}
	fresh := filepath.Join(dir, "fresh.db")
	r := success(t, fresh, "import", backup, "--dry-run")
	if r["added"] != float64(1) {
		t.Fatal(r)
	}
	if _, e = os.Stat(fresh); !os.IsNotExist(e) {
		t.Fatal("dry-run import created working store")
	}
}
func TestCLILocalizedJSONAndIdempotentEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "list.db")
	id := success(t, db, "add", "--title", "A", "--kind", "project")["id"].(string)
	r := success(t, db, "edit", id, "--title", "A")
	if r["changed"] != false {
		t.Fatal(r)
	}
	r = success(t, db, "--lang", "sv", "--effort", "low", "--peek")
	effort := r["filters"].(map[string]any)["effort"].(map[string]any)
	if effort["value"] != "low" || effort["max_intensity"] != model.LevelLow.Value() {
		t.Fatal(effort)
	}
	reasons := r["items"].([]any)[0].(map[string]any)["reasons"].([]any)
	for _, v := range reasons {
		if bytes.Contains([]byte(v.(map[string]any)["label"].(string)), []byte("okänd")) {
			t.Fatal("localized JSON")
		}
	}
}
