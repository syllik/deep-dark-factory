package local_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func inboxRecord() map[string]any {
	return map[string]any{
		"schema": "ddf.forge-local.task/v1", "number": 1, "kind": "task", "title": "T", "body": "B",
		"state": "open", "labels": []string{}, "createdBy": "local-operator",
		"sourceFile": "a.task.md", "digest": testDigest, "comments": []any{},
	}
}

func daemonRecord(marker string) map[string]any {
	return map[string]any{
		"schema": "ddf.forge-local.task/v1", "number": 1, "kind": "task", "title": "T", "body": "B",
		"state": "open", "labels": []string{"a"}, "createdBy": "ddf-daemon", "marker": marker,
		"comments": []any{map[string]any{"body": "c", "author": "ddf-daemon", "time": "2026-11-25T10:00:00Z"}},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func with(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}

func without(m map[string]any, k string) map[string]any {
	delete(m, k)
	return m
}

func TestDecodeTaskRecord(t *testing.T) {
	marker := "ddf:task-key=" + string(testKey(t, "1"))
	for _, tc := range []struct {
		name    string
		content func(t *testing.T) []byte
		ok      bool
	}{
		{"valid inbox record", func(t *testing.T) []byte { return mustJSON(t, inboxRecord()) }, true},
		{"valid daemon record", func(t *testing.T) []byte { return mustJSON(t, daemonRecord(marker)) }, true},
		{"unknown field", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "extra", 1)) }, false},
		{"another schema", func(t *testing.T) []byte {
			return mustJSON(t, with(inboxRecord(), "schema", "ddf.forge-local.task/v2"))
		}, false},
		{"trailing data", func(t *testing.T) []byte { return append(mustJSON(t, inboxRecord()), []byte(" {}")...) }, false},
		{"invalid kind", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "kind", "issue")) }, false},
		{"invalid state", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "state", "merged")) }, false},
		{"invalid createdBy", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "createdBy", "someone")) }, false},
		{"invalid comment author", func(t *testing.T) []byte {
			r := daemonRecord(marker)
			r["comments"] = []any{map[string]any{"body": "c", "author": "someone", "time": "2026-11-25T10:00:00Z"}}
			return mustJSON(t, r)
		}, false},
		{"marker without prefix", func(t *testing.T) []byte {
			return mustJSON(t, daemonRecord(strings.TrimPrefix(marker, "ddf:task-key=")))
		}, false},
		{"marker with an invalid key", func(t *testing.T) []byte {
			return mustJSON(t, daemonRecord("ddf:task-key="+strings.ToUpper(string(testKey(t, "1")))))
		}, false},
		{"invalid parent key", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "parentKey", "abc")) }, false},
		{"number not matching file name", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "number", 2)) }, false},
		{"labels missing", func(t *testing.T) []byte { return mustJSON(t, without(inboxRecord(), "labels")) }, false},
		{"comments null", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "comments", nil)) }, false},
		{"inbox record with marker", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "marker", marker)) }, false},
		{"inbox record with bad digest", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "digest", "ABC")) }, false},
		{"daemon record with source file", func(t *testing.T) []byte {
			return mustJSON(t, with(daemonRecord(marker), "sourceFile", "a.task.md"))
		}, false},
		{"daemon record of kind request", func(t *testing.T) []byte { return mustJSON(t, with(daemonRecord(marker), "kind", "request")) }, false},
		{"unsorted labels", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "labels", []string{"b", "a"})) }, false},
		{"empty title", func(t *testing.T) []byte { return mustJSON(t, with(inboxRecord(), "title", " ")) }, false},
		{"non-RFC 3339 comment time", func(t *testing.T) []byte {
			r := daemonRecord(marker)
			r["comments"] = []any{map[string]any{"body": "c", "author": "ddf-daemon", "time": "yesterday"}}
			return mustJSON(t, r)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.open(t)
			e.writeFile(t, "counter", []byte("1\n"))
			e.writeFile(t, "tasks/1.json", tc.content(t))
			_, err := local.Open(context.Background(), e.cfg)
			if tc.ok && err != nil {
				t.Fatalf("open: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("open succeeded, want a decode error")
			}
		})
	}
}

func answer() map[string]any {
	return map[string]any{
		"schema": "ddf.forge-local.answer/v1", "body": "yes", "author": "local-operator",
		"time": "2026-11-25T10:00:00Z",
	}
}

func TestDecodeAnswerRecord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content func(t *testing.T) []byte
		ok      bool
	}{
		{"valid", func(t *testing.T) []byte { return mustJSON(t, answer()) }, true},
		{"unknown field", func(t *testing.T) []byte { return mustJSON(t, with(answer(), "extra", true)) }, false},
		{"another schema", func(t *testing.T) []byte { return mustJSON(t, with(answer(), "schema", "ddf.forge-local.task/v1")) }, false},
		{"trailing data", func(t *testing.T) []byte { return append(mustJSON(t, answer()), '1') }, false},
		{"invalid author", func(t *testing.T) []byte { return mustJSON(t, with(answer(), "author", "someone")) }, false},
		{"daemon author", func(t *testing.T) []byte { return mustJSON(t, with(answer(), "author", "ddf-daemon")) }, false},
		{"missing time", func(t *testing.T) []byte { return mustJSON(t, without(answer(), "time")) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.open(t)
			e.drop(t, "a.task.md", taskText("T", "B"))
			e.ingest(t, f)
			e.writeFile(t, "answers/1/a1.json", tc.content(t))
			_, err := f.ReadAnswers(context.Background(), source("task", 1))
			if tc.ok && err != nil {
				t.Fatalf("read answers: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("read answers succeeded, want a decode error")
			}
		})
	}
}
