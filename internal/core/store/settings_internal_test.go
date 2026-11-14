package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "ddf.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func exec(t *testing.T, s *Store, q string, args ...any) error {
	t.Helper()
	_, err := s.db.ExecContext(t.Context(), q, args...)
	return err
}

func mustExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if err := exec(t, s, q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func insertTask(t *testing.T, s *Store, key string) {
	t.Helper()
	mustExec(t, s, `INSERT INTO tasks (task_key, forge, repository, source_kind, source_id, kind, state, created_at, updated_at, payload)
		VALUES (?, 'local', 'repo', 'issue', ?, 'task', 'inbox', 0, 0, '{}')`, key, key)
}

// spec §4.5 SQLite settings: read back after open.
func TestOpen_SettingsReadBack(t *testing.T) {
	s := newStore(t)
	if err := checkSettings(t.Context(), s.db); err != nil {
		t.Fatalf("checkSettings: %v", err)
	}
	if got := s.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestCheckSettings_MismatchFails(t *testing.T) {
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	err = checkSettings(t.Context(), db)
	var se *SettingError
	if !errors.As(err, &se) {
		t.Fatalf("checkSettings error = %v, want *SettingError", err)
	}
	if se.Name != "journal_mode" || se.Want != "wal" || se.Got != "delete" {
		t.Fatalf("SettingError = %+v, want journal_mode wal/delete", se)
	}
}

// spec §4.5 SQL rules: events has no UPDATE or DELETE path.
func TestSchema_EventsAppendOnly(t *testing.T) {
	s := newStore(t)
	mustExec(t, s, "INSERT INTO events (task_key, type, at, payload) VALUES (NULL, 'paused', 1, '{}')")
	for _, q := range []string{"UPDATE events SET type = 'x'", "DELETE FROM events"} {
		if err := exec(t, s, q); err == nil {
			t.Errorf("%q succeeded, want abort", q)
		}
	}
}

// spec §4.5 Tables: tasks.hit_stop is sticky.
func TestSchema_HitStopSticky(t *testing.T) {
	s := newStore(t)
	insertTask(t, s, "k1")
	mustExec(t, s, "UPDATE tasks SET hit_stop = 1 WHERE task_key = 'k1'")
	if err := exec(t, s, "UPDATE tasks SET hit_stop = 0 WHERE task_key = 'k1'"); err == nil {
		t.Fatal("clearing hit_stop succeeded, want abort")
	}
	mustExec(t, s, "UPDATE tasks SET state = 'planning' WHERE task_key = 'k1'")
}

func TestSchema_ForeignKeysEnforced(t *testing.T) {
	s := newStore(t)
	cases := []struct{ name, q string }{
		{"runs.task_key", `INSERT INTO runs (run_id, task_key, role, purpose, attempt, status, provider, started_at, payload)
			VALUES ('r1', 'missing', 'executor', 'initial', 1, 'running', 'p', 0, '{}')`},
		{"tasks.parent_key", `INSERT INTO tasks (task_key, forge, repository, source_kind, source_id, kind, parent_key, state, created_at, updated_at, payload)
			VALUES ('k2', 'local', 'repo', 'issue', '2', 'followup', 'missing', 'inbox', 0, 0, '{}')`},
		{"events.task_key", "INSERT INTO events (task_key, type, at, payload) VALUES ('missing', 't', 0, '{}')"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := exec(t, s, tc.q); err == nil {
				t.Fatal("insert with unknown task key succeeded")
			}
		})
	}
}

func TestInTx_RollsBackOnError(t *testing.T) {
	s := newStore(t)
	sentinel := errors.New("stop")
	err := s.inTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO pause_reasons (reason, set_at, payload) VALUES ('owner', 0, '{}')"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("inTx error = %v, want sentinel", err)
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM pause_reasons").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("pause_reasons rows = %d after rollback, want 0", n)
	}
}
