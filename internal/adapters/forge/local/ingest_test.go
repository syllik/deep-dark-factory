package local_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
)

func TestIngest_Rules(t *testing.T) {
	big := append([]byte("# Big\n"), bytes.Repeat([]byte("x"), local.MaxTaskFileBytes+1-len("# Big\n"))...)
	for _, tc := range []struct {
		name    string
		file    string
		content []byte
		symlink bool
		kind    string // record kind when ingested
		reason  string // rejection reason when rejected
		ignored bool
	}{
		{name: "valid task file", file: "a.task.md", content: taskText("Do it", "Body"), kind: "task"},
		{name: "valid request file", file: "b.request.md", content: taskText("Ask", "Q"), kind: "request"},
		{name: "exactly 64 KiB", file: "c.task.md", content: big[:local.MaxTaskFileBytes], kind: "task"},
		{name: "bad UTF-8", file: "d.task.md", content: []byte("# T\n\xff\xfe"), reason: local.ReasonBadEncoding},
		{name: "no title line", file: "e.task.md", content: []byte("Title\nbody"), reason: local.ReasonNoTitle},
		{name: "hash without space", file: "f.task.md", content: []byte("#Title\n"), reason: local.ReasonNoTitle},
		{name: "title empty after trimming", file: "g.task.md", content: []byte("#    \nbody"), reason: local.ReasonEmptyTitle},
		{name: "64 KiB + 1 byte", file: "h.task.md", content: big, reason: local.ReasonTooLarge},
		{name: "unknown suffix", file: "i.md", content: taskText("T", "B"), reason: local.ReasonUnknownSuffix},
		{name: "bare suffix name", file: "x.task", content: taskText("T", "B"), reason: local.ReasonUnknownSuffix},
		{name: "symlink entry", file: "j.task.md", symlink: true, reason: local.ReasonNotRegular},
		{name: "dot file", file: ".k.task.md", content: taskText("T", "B"), ignored: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.open(t)
			target := filepath.Join(t.TempDir(), "target.task.md")
			targetContent := taskText("Target", "untouched")
			if tc.symlink {
				if err := os.WriteFile(target, targetContent, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, e.path("inbox", tc.file)); err != nil {
					t.Fatal(err)
				}
			} else {
				e.drop(t, tc.file, tc.content)
			}
			rej := e.ingest(t, f)
			switch {
			case tc.kind != "":
				if len(rej) != 0 {
					t.Fatalf("rejections = %v", rej)
				}
				m := e.record(t, 1)
				sum := sha256.Sum256(tc.content)
				if got := rawString(t, m, "kind"); got != tc.kind {
					t.Errorf("kind = %q, want %q", got, tc.kind)
				}
				if got := rawString(t, m, "state"); got != "open" {
					t.Errorf("state = %q", got)
				}
				if got := rawString(t, m, "sourceFile"); got != tc.file {
					t.Errorf("sourceFile = %q", got)
				}
				if got := rawString(t, m, "digest"); got != hex.EncodeToString(sum[:]) {
					t.Errorf("digest = %q", got)
				}
				if got := rawString(t, m, "createdBy"); got != "local-operator" {
					t.Errorf("createdBy = %q", got)
				}
				if _, err := os.Lstat(e.path("inbox", tc.file)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("inbox file not deleted: %v", err)
				}
				if info, err := os.Lstat(e.path("answers", "1")); err != nil || !info.IsDir() {
					t.Errorf("answers/1 missing: %v", err)
				}
			case tc.reason != "":
				want := []local.Rejection{{Name: tc.file, RejectedName: tc.file, Reason: tc.reason}}
				if len(rej) != 1 || rej[0] != want[0] {
					t.Fatalf("rejections = %v, want %v", rej, want)
				}
				if len(e.taskFiles(t)) != 0 {
					t.Errorf("records written: %v", e.taskFiles(t))
				}
				if _, err := os.Lstat(e.path("inbox", tc.file)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("inbox entry still present: %v", err)
				}
				moved, err := os.Lstat(e.path("inbox", "rejected", tc.file))
				if err != nil {
					t.Fatalf("rejected entry: %v", err)
				}
				reason, err := os.ReadFile(e.path("inbox", "rejected", tc.file+".reason"))
				if err != nil || string(reason) != tc.reason+"\n" {
					t.Errorf("reason file = %q, %v", reason, err)
				}
				if tc.symlink {
					if moved.Mode()&os.ModeSymlink == 0 {
						t.Errorf("rejected entry is not the moved link: %v", moved.Mode())
					}
					b, err := os.ReadFile(target)
					if err != nil || !bytes.Equal(b, targetContent) {
						t.Errorf("symlink target changed: %q, %v", b, err)
					}
				}
			case tc.ignored:
				if len(rej) != 0 || len(e.taskFiles(t)) != 0 {
					t.Fatalf("dot file handled: rejections %v, records %v", rej, e.taskFiles(t))
				}
				if _, err := os.Lstat(e.path("inbox", tc.file)); err != nil {
					t.Errorf("dot file removed: %v", err)
				}
			}
		})
	}
}

func TestIngest_ParsesTitleAndBody(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", []byte("#   Fix the thing  \r\n\r\n  \nLine 1\r\nLine 2\n"))
	e.ingest(t, f)
	tasks, err := f.ListCandidateTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Fix the thing" || tasks[0].Body != "Line 1\r\nLine 2\n" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestIngest_RejectedNameCollision(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.md", []byte("first"))
	first := e.ingest(t, f)
	e.drop(t, "a.md", []byte("second"))
	second := e.ingest(t, f)
	if len(first) != 1 || first[0].RejectedName != "a.md" {
		t.Fatalf("first = %v", first)
	}
	if len(second) != 1 || second[0].RejectedName != "a.md.1" || second[0].Name != "a.md" {
		t.Fatalf("second = %v", second)
	}
	for name, want := range map[string]string{
		"a.md": "first", "a.md.1": "second",
		"a.md.reason": local.ReasonUnknownSuffix + "\n", "a.md.1.reason": local.ReasonUnknownSuffix + "\n",
	} {
		b, err := os.ReadFile(e.path("inbox", "rejected", name))
		if err != nil || string(b) != want {
			t.Errorf("rejected/%s = %q, %v; want %q", name, b, err, want)
		}
	}
	// A file named like an existing reason file takes the next free name.
	e.drop(t, "a.md.reason", []byte("third"))
	third := e.ingest(t, f)
	if len(third) != 1 || third[0].RejectedName != "a.md.reason.1" {
		t.Fatalf("third = %v", third)
	}
	if b, _ := os.ReadFile(e.path("inbox", "rejected", "a.md.reason")); string(b) != local.ReasonUnknownSuffix+"\n" {
		t.Errorf("reason file overwritten: %q", b)
	}
}

func TestIngest_CrashSafety(t *testing.T) {
	content := taskText("Once", "only")
	for _, tc := range []struct {
		name  string
		rerun func(t *testing.T, e *env, f *local.Forge)
	}{
		{"through Ingest", func(t *testing.T, e *env, f *local.Forge) {
			if rej := e.ingest(t, f); len(rej) != 0 {
				t.Fatalf("rejections = %v", rej)
			}
		}},
		{"through Open", func(t *testing.T, e *env, _ *local.Forge) { e.open(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.open(t)
			e.drop(t, "a.task.md", content)
			e.ingest(t, f)
			// The daemon crashed after the record, before the deletion.
			e.drop(t, "a.task.md", content)
			tc.rerun(t, e, f)
			if _, err := os.Lstat(e.path("inbox", "a.task.md")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("inbox file not deleted: %v", err)
			}
			if got := e.taskFiles(t); len(got) != 1 {
				t.Errorf("records = %v, want one", got)
			}
		})
	}
}

func TestIngest_SameNameNewContentIsNewTask(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("One", ""))
	e.ingest(t, f)
	e.drop(t, "a.task.md", taskText("Two", ""))
	e.ingest(t, f)
	if got := e.taskFiles(t); len(got) != 2 {
		t.Fatalf("records = %v, want two", got)
	}
}
