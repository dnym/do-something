package store

import (
	"bytes"
	"dosomething/internal/model"
	"fmt"
	"modernc.org/sqlite"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var registerCrashCollation sync.Once
var vacuumArmed atomic.Bool

func crashCollation(t *testing.T) {
	t.Helper()
	registerCrashCollation.Do(func() {
		err := sqlite.RegisterCollationUtf8("test_vacuum_crash", func(a, b string) int {
			if vacuumArmed.Load() {
				crashReady(os.Getenv("DO_SOMETHING_TEST_DB"), "vacuum_active")
			}
			return strings.Compare(a, b)
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
func crashReady(db, phase string) {
	if err := os.WriteFile(db+".ready", []byte(phase), 0600); err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestPublishCrashHelper(t *testing.T) {
	phase := os.Getenv("DO_SOMETHING_TEST_CRASH")
	if phase == "" {
		return
	}
	db := os.Getenv("DO_SOMETHING_TEST_DB")
	crashCollation(t)
	unlock, e := Acquire(Options{DBPath: db})
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	s, e := Open(OpenWrite, Options{DBPath: db, Locked: true})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	snapshotHook = func(at string) {
		// Stop arming before validation: the marker must originate inside VACUUM.
		if at == "vacuum_complete" {
			vacuumArmed.Store(false)
		}
		if at == phase {
			crashReady(db, at)
		}
	}
	vacuumArmed.Store(phase == "vacuum_active")
	if e = s.VacuumInto(db + ".published"); e != nil {
		t.Fatal(e)
	}
}
func TestKillDuringPublishAndReplace(t *testing.T) {
	for _, phase := range []string{"vacuum_active", "vacuum_complete", "before_replace", "after_replace"} {
		t.Run(phase, func(t *testing.T) {
			crashCollation(t)
			db := filepath.Join(t.TempDir(), "list.db")
			s, e := Open(OpenWrite, Options{DBPath: db})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Save(ItemSpec{Title: "previous", Kind: model.KindProject}, true); e != nil {
				t.Fatal(e)
			}
			// VACUUM rebuilds this index and invokes its collation inside SQLite.
			for _, sql := range []string{
				"CREATE TABLE crash_fixture(value TEXT COLLATE test_vacuum_crash)",
				"INSERT INTO crash_fixture VALUES ('b'),('a'),('c')",
				"CREATE INDEX crash_fixture_index ON crash_fixture(value)",
			} {
				if _, err := s.db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			if e = s.VacuumInto(db + ".published"); e != nil {
				t.Fatal(e)
			}
			previous, err := os.ReadFile(db + ".published")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Save(ItemSpec{Title: "newer", Kind: model.KindProject}, true); err != nil {
				t.Fatal(err)
			}
			s.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestPublishCrashHelper$")
			cmd.Env = append(os.Environ(), "DO_SOMETHING_TEST_CRASH="+phase, "DO_SOMETHING_TEST_DB="+db)
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			deadline := time.Now().Add(15 * time.Second)
			for {
				if _, e = os.Stat(db + ".ready"); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("helper never reached crash point")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e = cmd.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			_ = cmd.Wait()
			if e = ValidatePath(db + ".published"); e != nil {
				t.Fatalf("snapshot invalid at %s: %v", phase, e)
			}
			unlock, e := Acquire(Options{DBPath: db})
			if e != nil {
				t.Fatal(fmt.Errorf("dead process retained lock: %w", e))
			}
			unlock()
			if phase != "after_replace" {
				current, err := os.ReadFile(db + ".published")
				if err != nil || !bytes.Equal(previous, current) {
					t.Fatalf("previous snapshot changed before replacement: %v", err)
				}
			}
			recovered, err := Open(OpenWrite, Options{DBPath: db})
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err = recovered.VacuumInto(db + ".published"); err != nil {
				t.Fatal(err)
			}
			content, _, err := ReadSnapshot(db + ".published")
			if err != nil || len(content.Items) != 2 {
				t.Fatalf("recovery lost changes: %v", err)
			}
			leftovers, _ := filepath.Glob(db + ".published.tmp-*")
			if len(leftovers) > 0 {
				t.Fatalf("leftover temp files: %v", leftovers)
			}
		})
	}
}
