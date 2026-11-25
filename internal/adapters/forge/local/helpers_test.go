package local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const projectID = "demo"

var testStart = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

// env is one test project: a data dir, a bare remote, a fixed clock and a
// captured logger.
type env struct {
	dataDir string
	remote  string
	clock   *fakes.Clock
	logs    *bytes.Buffer
	cfg     local.Config
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dataDir := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	makeBare(t, bare)
	e := &env{dataDir: dataDir, remote: bare, clock: fakes.NewClock(testStart), logs: &bytes.Buffer{}}
	e.cfg = local.Config{
		ProjectID: projectID,
		DataDir:   dataDir,
		RemoteURL: fileURL(bare),
		Clock:     e.clock,
		IDs:       fakes.NewIDs("id"),
		Logger:    slog.New(slog.NewTextHandler(e.logs, nil)),
	}
	return e
}

// makeBare creates the filesystem shape of a bare repository at p.
func makeBare(t *testing.T, p string) {
	t.Helper()
	for _, d := range []string{p, filepath.Join(p, "objects"), filepath.Join(p, "refs")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p, "HEAD"), []byte("ref: refs/heads/master\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileURL(p string) string { return (&url.URL{Scheme: "file", Path: p}).String() }

func (e *env) root() string { return filepath.Join(e.dataDir, "forge-local", projectID) }

func (e *env) path(elem ...string) string {
	return filepath.Join(append([]string{e.root()}, elem...)...)
}

func (e *env) open(t *testing.T) *local.Forge {
	t.Helper()
	f, err := local.Open(context.Background(), e.cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return f
}

// drop writes an inbox file as an operator would.
func (e *env) drop(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(e.path("inbox", name), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeFile writes a storage file with mode 0600, creating parents 0700.
func (e *env) writeFile(t *testing.T, rel string, content []byte) {
	t.Helper()
	p := e.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) ingest(t *testing.T, f *local.Forge) []local.Rejection {
	t.Helper()
	rej, err := f.Ingest(context.Background(), ports.WriteModeLive)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return rej
}

// taskFiles returns the names under tasks/.
func (e *env) taskFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.path("tasks"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		out = append(out, en.Name())
	}
	return out
}

// record decodes tasks/<n>.json into raw fields.
func (e *env) record(t *testing.T, n int) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(e.path("tasks", strconv.Itoa(n)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func rawString(t *testing.T, m map[string]json.RawMessage, key string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		t.Fatalf("field %s: %v", key, err)
	}
	return s
}

// snapshot captures every path under dir with its mode and bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		v := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			v += " -> " + target
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += " " + string(b)
		}
		out[p] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed: before %q, after %q", k, v, after[k])
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s appeared", k)
		}
	}
}

func source(kind string, n int) model.TaskSource {
	return model.TaskSource{Forge: "local", Repository: projectID, SourceKind: kind, SourceID: strconv.Itoa(n)}
}

func testKey(t *testing.T, id string) model.TaskKey {
	t.Helper()
	k, err := model.NewTaskKey(model.TaskSource{Forge: "github", Repository: "o/r", SourceKind: "issue", SourceID: id})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func taskText(title, body string) []byte { return []byte(fmt.Sprintf("# %s\n\n%s", title, body)) }
