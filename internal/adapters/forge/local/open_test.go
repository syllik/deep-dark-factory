package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
)

func TestOpen_CreatesMissingStorage(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	b, err := os.ReadFile(e.path("format"))
	if err != nil || string(b) != "ddf.forge-local/v1\n" {
		t.Fatalf("format = %q, %v", b, err)
	}
	for _, d := range []string{"inbox", "inbox/rejected", "tasks", "answers"} {
		info, err := os.Lstat(e.path(d))
		if err != nil || !info.IsDir() {
			t.Errorf("%s: not a directory (%v)", d, err)
		}
	}
}

func TestOpen_InitialisesEmptyDir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
	}{
		{"empty", nil},
		{"only adapter temp files", []string{".ddf-tmp-123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if err := os.MkdirAll(e.root(), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range tc.files {
				e.writeFile(t, f, []byte("x"))
			}
			e.open(t)
			if _, err := os.Stat(e.path("format")); err != nil {
				t.Fatalf("format: %v", err)
			}
			for _, f := range tc.files {
				if _, err := os.Lstat(e.path(f)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s not removed: %v", f, err)
				}
			}
		})
	}
}

func TestOpen_RefusesFormat(t *testing.T) {
	for _, tc := range []struct {
		name, file, content string
	}{
		{"non-empty without format", "notes.txt", "x"},
		{"format holding another value", "format", "ddf.forge-local/v2\n"},
		{"format without newline", "format", "ddf.forge-local/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.writeFile(t, tc.file, []byte(tc.content))
			_, err := local.Open(context.Background(), e.cfg)
			if !errors.Is(err, local.ErrFormat) {
				t.Fatalf("err = %v, want ErrFormat", err)
			}
		})
	}
}

func TestOpen_RemovesStrayTempFiles(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	stray := []string{".ddf-tmp-a", "tasks/.ddf-tmp-b", "inbox/rejected/.ddf-tmp-c"}
	for _, s := range stray {
		e.writeFile(t, s, []byte("partial"))
	}
	// agentDefault 8: dot files in inbox/ and answers/<n>/ are in-flight CLI
	// writes and stay.
	kept := []string{"inbox/.ddf-tmp-ask", "answers/1/.ddf-tmp-answer"}
	for _, s := range kept {
		e.writeFile(t, s, []byte("in flight"))
	}
	e.open(t)
	for _, s := range stray {
		if _, err := os.Lstat(e.path(s)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s not removed: %v", s, err)
		}
	}
	for _, s := range kept {
		if _, err := os.Lstat(e.path(s)); err != nil {
			t.Errorf("%s removed: %v", s, err)
		}
	}
}

func TestOpen_RefusesUnsupportedRemote(t *testing.T) {
	nonBare := t.TempDir()
	for _, tc := range []struct {
		name, url string
	}{
		{"ssh", "ssh://h/r.git"},
		{"http", "http://h/r.git"},
		{"relative path", "relative/r.git"},
		{"plain absolute path", "/srv/r.git"},
		{"file url with host", "file://host/srv/r.git"},
		{"file url to a missing path", fileURL(filepath.Join(t.TempDir(), "missing.git"))},
		{"file url to a non-bare directory", fileURL(nonBare)},
		{"file url with query", fileURL(nonBare) + "?x=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.cfg.RemoteURL = tc.url
			_, err := local.Open(context.Background(), e.cfg)
			if !errors.Is(err, local.ErrUnsupportedRemote) {
				t.Fatalf("err = %v, want ErrUnsupportedRemote", err)
			}
		})
	}
}

func TestOpen_AcceptsBareRemote(t *testing.T) {
	e := newEnv(t)
	if _, err := local.Open(context.Background(), e.cfg); err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestOpen_RefusesStorageOverlappingRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote func(e *env) string
	}{
		{"equal", func(e *env) string { return e.root() }},
		{"inside", func(e *env) string { return filepath.Join(e.dataDir, "forge-local") }},
		{"containing", func(e *env) string { return e.path("remote.git") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			remote := tc.remote(e)
			makeBare(t, remote)
			e.cfg.RemoteURL = fileURL(remote)
			if _, err := local.Open(context.Background(), e.cfg); err == nil {
				t.Fatal("open succeeded, want an overlap error")
			}
		})
	}
}

func TestOpen_RefusesSymlinkComponent(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(e.dataDir, "forge-local")); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), e.cfg); err == nil {
		t.Fatal("open succeeded through a symlinked storage component")
	}
}

func TestOpen_RefusesConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(c *local.Config)
	}{
		{"empty project id", func(c *local.Config) { c.ProjectID = "" }},
		{"upper-case project id", func(c *local.Config) { c.ProjectID = "Demo" }},
		{"project id with leading dash", func(c *local.Config) { c.ProjectID = "-demo" }},
		{"project id with underscore", func(c *local.Config) { c.ProjectID = "de_mo" }},
		{"project id with slash", func(c *local.Config) { c.ProjectID = "a/b" }},
		{"empty data dir", func(c *local.Config) { c.DataDir = "" }},
		{"relative data dir", func(c *local.Config) { c.DataDir = "data" }},
		{"unclean data dir", func(c *local.Config) { c.DataDir += "/../x" }},
		{"empty remote url", func(c *local.Config) { c.RemoteURL = "" }},
		{"nil clock", func(c *local.Config) { c.Clock = nil }},
		{"nil id source", func(c *local.Config) { c.IDs = nil }},
		{"nil logger", func(c *local.Config) { c.Logger = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.mutate(&e.cfg)
			if _, err := local.Open(context.Background(), e.cfg); err == nil {
				t.Fatal("open succeeded, want a config error")
			}
			if _, err := os.Lstat(filepath.Join(e.dataDir, "forge-local")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("storage written despite a config error: %v", err)
			}
		})
	}
}
