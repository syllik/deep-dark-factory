package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// SchemaVersion is the version written into schema_version when the schema
// is created and required on every later open.
const SchemaVersion int64 = 1

//go:embed schema.sql
var schemaSQL string

const driverName = "sqlite"

// Store is the open SQLite store. It holds one database handle with at most
// one open connection.
type Store struct {
	db *sql.DB
}

// VersionError reports a database whose schema version is not SchemaVersion.
// Found is the stored version; Reason is set instead when no single version
// could be read.
type VersionError struct {
	Found  int64
	Want   int64
	Reason string
}

// Error names both versions.
func (e *VersionError) Error() string {
	found := strconv.FormatInt(e.Found, 10)
	if e.Reason != "" {
		found = "unknown (" + e.Reason + ")"
	}
	return "store schema version " + found + ", want " + strconv.FormatInt(e.Want, 10)
}

// SettingError reports a connection setting that did not read back as set.
type SettingError struct {
	Name string
	Want string
	Got  string
}

// Error names the setting, the expected and the actual value.
func (e *SettingError) Error() string {
	return "store setting " + e.Name + " = " + e.Got + ", want " + e.Want
}

// Open opens the store at path. An empty database (missing file, zero-length
// file or no tables) gets the schema and SchemaVersion; a database holding
// SchemaVersion opens; anything else is refused with a *VersionError and the
// file is not changed.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	empty, err := checkVersion(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	db, err := sql.Open(driverName, dsn(abs, false, false))
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	// spec §4.5 one writer: at most one open connection
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(ctx, empty); err != nil {
		if cerr := db.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	return s, nil
}

func (s *Store) init(ctx context.Context, empty bool) error {
	if err := checkSettings(ctx, s.db); err != nil {
		return err
	}
	if !empty {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := tableCount(ctx, tx)
		if err != nil {
			return err
		}
		if n != 0 {
			return errors.New("database gained tables during open")
		}
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", SchemaVersion); err != nil {
			return fmt.Errorf("write schema version: %w", err)
		}
		return nil
	})
}

// Close closes the database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

// dsn builds the file: URL for path. A read-only DSN carries no write pragma,
// so opening it never rewrites the file header. An immutable read-only DSN
// also takes no lock and creates no -wal or -shm file; it is used only when
// no -wal file exists, so the main file holds every committed page.
func dsn(path string, readOnly, immutable bool) string {
	q := url.Values{}
	if readOnly {
		q.Set("mode", "ro")
		if immutable {
			q.Set("immutable", "1")
		}
		q.Add("_pragma", "query_only(1)")
	} else {
		// spec §4.5 SQLite settings, applied on every connection
		q["_pragma"] = []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(1)"}
		q.Set("_txlock", "immediate")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

// checkVersion reports whether the database at path is empty, and refuses a
// non-empty one whose schema version is not SchemaVersion. It reads through
// a read-only connection, before any pragma or write.
//
// spec §4.5 schema file and version
func checkVersion(ctx context.Context, path string) (empty bool, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat: %w", err)
	}
	if fi.Size() == 0 {
		return true, nil
	}
	_, err = os.Stat(path + "-wal")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("stat wal: %w", err)
	}
	noWAL := err != nil
	db, err := sql.Open(driverName, dsn(path, true, noWAL))
	if err != nil {
		return false, fmt.Errorf("open read-only: %w", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close read-only: %w", cerr)
		}
	}()
	db.SetMaxOpenConns(1)
	n, err := tableCount(ctx, db)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return true, nil
	}
	var hasVersion int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'schema_version'").Scan(&hasVersion); err != nil {
		return false, fmt.Errorf("find schema_version: %w", err)
	}
	if hasVersion == 0 {
		return false, &VersionError{Want: SchemaVersion, Reason: "no schema_version table"}
	}
	versions, err := readVersions(ctx, db)
	if err != nil {
		return false, err
	}
	if len(versions) != 1 {
		return false, &VersionError{Want: SchemaVersion, Reason: strconv.Itoa(len(versions)) + " schema_version rows"}
	}
	if versions[0] != SchemaVersion {
		return false, &VersionError{Found: versions[0], Want: SchemaVersion}
	}
	return false, nil
}

func readVersions(ctx context.Context, db *sql.DB) (versions []int64, err error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_version")
	if err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close schema_version rows: %w", cerr)
		}
	}()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_version: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	return versions, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func tableCount(ctx context.Context, q queryer) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table'").Scan(&n); err != nil {
		return 0, fmt.Errorf("count tables: %w", err)
	}
	return n, nil
}

// checkSettings reads every setting back and fails on the first mismatch.
//
// spec §4.5 SQLite settings
func checkSettings(ctx context.Context, db *sql.DB) error {
	settings := []struct{ name, want string }{
		{"journal_mode", "wal"},
		{"synchronous", "2"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
	}
	for _, st := range settings {
		var got string
		// st.name comes from the constant table above, never from input.
		if err := db.QueryRowContext(ctx, "PRAGMA "+st.name).Scan(&got); err != nil {
			return fmt.Errorf("read setting %s: %w", st.name, err)
		}
		if got != st.want {
			return &SettingError{Name: st.name, Want: st.want, Got: got}
		}
	}
	return nil
}

// inTx runs fn in one transaction (BEGIN IMMEDIATE through the DSN) and
// commits, or rolls back when fn or the commit fails.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rerr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
