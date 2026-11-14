package store_test

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/store"
)

func openStore(t *testing.T, path string) {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// rawExec runs statements on a plain database/sql handle (rollback journal,
// no store settings) and closes it.
func rawExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("raw close: %v", err)
		}
	}()
	for _, q := range stmts {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("raw exec %q: %v", q, err)
		}
	}
}

func rawVersion(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("raw close: %v", err)
		}
	}()
	var v int64
	if err := db.QueryRowContext(t.Context(), "SELECT version FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("read version: %v", err)
	}
	return v
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// assertRefusedUnchanged opens path, expects a *VersionError and checks that
// the main file bytes are identical and no -wal or -shm side file appeared.
func assertRefusedUnchanged(t *testing.T, path string) *store.VersionError {
	t.Helper()
	before := readFile(t, path)
	_, err := store.Open(t.Context(), path)
	var ve *store.VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("Open error = %v, want *store.VersionError", err)
	}
	if !bytes.Equal(before, readFile(t, path)) {
		t.Fatal("database file changed by a refused open")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("side file %s after refused open: stat err = %v", suffix, err)
		}
	}
	return ve
}

func TestOpen_EmptyGetsSchemaAndVersion(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"missing file", func(*testing.T, string) {}},
		{"zero-length file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"database with no tables", func(t *testing.T, path string) {
			rawExec(t, path, "PRAGMA user_version = 7")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ddf.db")
			tc.setup(t, path)
			openStore(t, path)
			if got := rawVersion(t, path); got != store.SchemaVersion {
				t.Fatalf("schema_version = %d, want %d", got, store.SchemaVersion)
			}
		})
	}
}

func TestOpen_EqualVersionReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ddf.db")
	openStore(t, path)
	openStore(t, path)
	if got := rawVersion(t, path); got != store.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", got, store.SchemaVersion)
	}
}

func TestOpen_RefusesOtherVersionUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ddf.db")
	openStore(t, path)
	rawExec(t, path, "UPDATE schema_version SET version = 2")
	ve := assertRefusedUnchanged(t, path)
	if ve.Found != 2 || ve.Want != store.SchemaVersion {
		t.Fatalf("VersionError = %+v, want Found 2 Want %d", ve, store.SchemaVersion)
	}
	if msg := ve.Error(); !strings.Contains(msg, "2") || !strings.Contains(msg, "1") {
		t.Fatalf("error %q does not name both versions", msg)
	}
}

func TestOpen_RefusesBadVersionRows(t *testing.T) {
	cases := []struct {
		name  string
		stmts []string
	}{
		{"no schema_version rows", []string{"DELETE FROM schema_version"}},
		{"two schema_version rows", []string{"INSERT INTO schema_version (version) VALUES (1)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ddf.db")
			openStore(t, path)
			rawExec(t, path, tc.stmts...)
			if ve := assertRefusedUnchanged(t, path); ve.Reason == "" {
				t.Fatalf("VersionError %+v has no reason", ve)
			}
		})
	}
}

func TestOpen_RefusesForeignDatabaseUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	rawExec(t, path, "CREATE TABLE notes (body TEXT)", "INSERT INTO notes VALUES ('keep')")
	ve := assertRefusedUnchanged(t, path)
	if ve.Want != store.SchemaVersion || ve.Reason == "" {
		t.Fatalf("VersionError = %+v, want Want %d with a reason", ve, store.SchemaVersion)
	}
}

func TestSchema_NoMigrationsDirectory(t *testing.T) {
	if _, err := os.Stat("migrations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrations directory: stat err = %v, want not exist", err)
	}
}
