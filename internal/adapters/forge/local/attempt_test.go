package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

// Internal tests: change and review records are not read by any exported
// method of this child, so their decoders are tested directly.

const (
	sha1Head = "0123456789abcdef0123456789abcdef01234567"
	sha2Head = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	otherSHA = "89abcdef0123456789abcdef0123456789abcdef"
)

func internalMarker(t *testing.T) string {
	t.Helper()
	k, err := model.NewTaskKey(model.TaskSource{Forge: "github", Repository: "o/r", SourceKind: "issue", SourceID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	return markerPrefix + string(k)
}

func toJSON(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func set(m map[string]any, kv ...any) map[string]any {
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func del(m map[string]any, k string) map[string]any {
	delete(m, k)
	return m
}

type decodeCase struct {
	name string
	b    func(t *testing.T) []byte
	ok   bool
}

func TestDecodeChange(t *testing.T) {
	marker := internalMarker(t)
	rec := func() map[string]any {
		return map[string]any{
			"schema": changeSchema, "id": "c1", "kind": "change", "branch": "feat/x", "baseBranch": "main",
			"headSha": sha1Head, "title": "T", "body": "B", "marker": marker, "state": "open",
			"createdAt": "2026-11-25T10:00:00Z",
		}
	}
	for _, tc := range []decodeCase{
		{"valid open change", func(t *testing.T) []byte { return toJSON(t, rec()) }, true},
		{"valid sha-256 head", func(t *testing.T) []byte { return toJSON(t, set(rec(), "headSha", sha2Head)) }, true},
		{"valid merged change", func(t *testing.T) []byte {
			return toJSON(t, set(rec(), "state", "merged", "mergeSha", otherSHA))
		}, true},
		{"valid revert", func(t *testing.T) []byte {
			return toJSON(t, set(rec(), "kind", "revert", "revertOf", otherSHA))
		}, true},
		{"unknown field", func(t *testing.T) []byte { return toJSON(t, set(rec(), "extra", 1)) }, false},
		{"another schema", func(t *testing.T) []byte { return toJSON(t, set(rec(), "schema", taskSchema)) }, false},
		{"trailing data", func(t *testing.T) []byte { return append(toJSON(t, rec()), '1') }, false},
		{"invalid kind", func(t *testing.T) []byte { return toJSON(t, set(rec(), "kind", "pr")) }, false},
		{"invalid state", func(t *testing.T) []byte { return toJSON(t, set(rec(), "state", "draft")) }, false},
		{"id not matching file name", func(t *testing.T) []byte { return toJSON(t, set(rec(), "id", "c2")) }, false},
		{"upper-case head sha", func(t *testing.T) []byte {
			return toJSON(t, set(rec(), "headSha", "0123456789ABCDEF0123456789abcdef01234567"))
		}, false},
		{"short head sha", func(t *testing.T) []byte { return toJSON(t, set(rec(), "headSha", sha1Head[:12])) }, false},
		{"empty title", func(t *testing.T) []byte { return toJSON(t, set(rec(), "title", " ")) }, false},
		{"empty branch", func(t *testing.T) []byte { return toJSON(t, set(rec(), "branch", "")) }, false},
		{"invalid marker", func(t *testing.T) []byte { return toJSON(t, set(rec(), "marker", "task-key")) }, false},
		{"merged without merge sha", func(t *testing.T) []byte { return toJSON(t, set(rec(), "state", "merged")) }, false},
		{"merge sha on an open change", func(t *testing.T) []byte { return toJSON(t, set(rec(), "mergeSha", otherSHA)) }, false},
		{"revert without revertOf", func(t *testing.T) []byte { return toJSON(t, set(rec(), "kind", "revert")) }, false},
		{"revertOf on a change", func(t *testing.T) []byte { return toJSON(t, set(rec(), "revertOf", otherSHA)) }, false},
		{"missing createdAt", func(t *testing.T) []byte { return toJSON(t, del(rec(), "createdAt")) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeChange(tc.b(t), "c1")
			if tc.ok != (err == nil) {
				t.Fatalf("decode: ok=%v, err=%v", tc.ok, err)
			}
		})
	}
	if _, err := decodeChange(toJSON(t, set(rec(), "id", "..")), ".."); err == nil {
		t.Error("decode accepted the path-unsafe id ..")
	}
}

func TestDecodeAttempt(t *testing.T) {
	marker := internalMarker(t)
	rec := func() map[string]any {
		return map[string]any{
			"schema": checkSchema, "name": "verify", "headSha": sha1Head, "attempt": 2, "status": "passed",
			"runId": "run-1", "marker": marker, "startedAt": "2026-11-25T10:00:00Z",
			"finishedAt": "2026-11-25T10:05:00Z",
		}
	}
	for _, tc := range []decodeCase{
		{"valid passed", func(t *testing.T) []byte { return toJSON(t, rec()) }, true},
		{"valid running", func(t *testing.T) []byte {
			return toJSON(t, del(set(rec(), "status", "running"), "finishedAt"))
		}, true},
		{"valid lost", func(t *testing.T) []byte { return toJSON(t, set(rec(), "status", "lost")) }, true},
		{"unknown field", func(t *testing.T) []byte { return toJSON(t, set(rec(), "extra", 1)) }, false},
		{"another schema", func(t *testing.T) []byte { return toJSON(t, set(rec(), "schema", changeSchema)) }, false},
		{"trailing data", func(t *testing.T) []byte { return append(toJSON(t, rec()), []byte("{}")...) }, false},
		{"invalid status", func(t *testing.T) []byte { return toJSON(t, set(rec(), "status", "ok")) }, false},
		{"other check name", func(t *testing.T) []byte { return toJSON(t, set(rec(), "name", "lint")) }, false},
		{"head not matching directory", func(t *testing.T) []byte { return toJSON(t, set(rec(), "headSha", otherSHA)) }, false},
		{"attempt not matching file name", func(t *testing.T) []byte { return toJSON(t, set(rec(), "attempt", 3)) }, false},
		{"path-unsafe run id", func(t *testing.T) []byte { return toJSON(t, set(rec(), "runId", "../x")) }, false},
		{"dot-dot run id", func(t *testing.T) []byte { return toJSON(t, set(rec(), "runId", "..")) }, false},
		{"invalid marker", func(t *testing.T) []byte { return toJSON(t, set(rec(), "marker", "")) }, false},
		{"missing startedAt", func(t *testing.T) []byte { return toJSON(t, del(rec(), "startedAt")) }, false},
		{"finished without finishedAt", func(t *testing.T) []byte { return toJSON(t, del(rec(), "finishedAt")) }, false},
		{"running with finishedAt", func(t *testing.T) []byte { return toJSON(t, set(rec(), "status", "running")) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeAttempt(tc.b(t), sha1Head, 2)
			if tc.ok != (err == nil) {
				t.Fatalf("decode: ok=%v, err=%v", tc.ok, err)
			}
		})
	}
	if _, err := decodeAttempt(toJSON(t, set(rec(), "attempt", 0)), sha1Head, 0); err == nil {
		t.Error("decode accepted attempt 0")
	}
}

func TestDecodeReview(t *testing.T) {
	rec := func() map[string]any {
		return map[string]any{
			"schema": reviewSchema, "changeId": "c1", "headSha": sha1Head,
			"reviewer": map[string]any{"id": "rev-1", "name": "Reviewer"}, "verdict": "APPROVE", "body": "ok",
		}
	}
	for _, tc := range []decodeCase{
		{"valid", func(t *testing.T) []byte { return toJSON(t, rec()) }, true},
		{"valid request changes", func(t *testing.T) []byte { return toJSON(t, set(rec(), "verdict", "REQUEST_CHANGES")) }, true},
		{"unknown field", func(t *testing.T) []byte { return toJSON(t, set(rec(), "extra", 1)) }, false},
		{"unknown reviewer field", func(t *testing.T) []byte {
			return toJSON(t, set(rec(), "reviewer", map[string]any{"id": "rev-1", "name": "R", "x": 1}))
		}, false},
		{"another schema", func(t *testing.T) []byte { return toJSON(t, set(rec(), "schema", checkSchema)) }, false},
		{"trailing data", func(t *testing.T) []byte { return append(toJSON(t, rec()), ' ', '[') }, false},
		{"invalid verdict", func(t *testing.T) []byte { return toJSON(t, set(rec(), "verdict", "lgtm")) }, false},
		{"change id not matching directory", func(t *testing.T) []byte { return toJSON(t, set(rec(), "changeId", "c2")) }, false},
		{"head not matching directory", func(t *testing.T) []byte { return toJSON(t, set(rec(), "headSha", otherSHA)) }, false},
		{"reviewer id not matching file name", func(t *testing.T) []byte {
			return toJSON(t, set(rec(), "reviewer", map[string]any{"id": "rev-2", "name": "R"}))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeReview(tc.b(t), "c1", sha1Head, "rev-1")
			if tc.ok != (err == nil) {
				t.Fatalf("decode: ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

func TestValidators(t *testing.T) {
	for s, want := range map[string]bool{
		sha1Head: true, sha2Head: true, "": false, sha1Head[:39]: false, sha1Head + "0": false,
		"0123456789ABCDEF0123456789abcdef01234567": false, "g123456789abcdef0123456789abcdef01234567": false,
	} {
		if got := validSHA(s); got != want {
			t.Errorf("validSHA(%q) = %v", s, got)
		}
	}
	for s, want := range map[string]bool{
		"c1": true, "a.b_c-D": true, ".x": true, "": false, ".": false, "..": false, "a/b": false,
		`a\b`: false, "a b": false, string(bytes.Repeat([]byte("a"), 65)): false,
	} {
		if got := safeID(s); got != want {
			t.Errorf("safeID(%q) = %v", s, got)
		}
	}
}

type fixedIDs []string

func (f *fixedIDs) NewID() string {
	id := (*f)[0]
	*f = (*f)[1:]
	return id
}

// bareForge is a Forge on a temp storage root with only checks/; enough for
// the attempt helpers.
func bareForge(t *testing.T, ids interface{ NewID() string }) *Forge {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, checksDir), dirMode); err != nil {
		t.Fatal(err)
	}
	return &Forge{root: root, cfg: Config{
		Clock: fakes.NewClock(time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)), IDs: ids,
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}}
}

func TestStartAttemptLocked(t *testing.T) {
	ctx := context.Background()
	ids := fixedIDs{"run-a", "run-b"}
	f := bareForge(t, &ids)
	marker := internalMarker(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	a1, err := f.startAttemptLocked(ctx, sha1Head, marker)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := f.startAttemptLocked(ctx, sha1Head, marker)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Attempt != 1 || a2.Attempt != 2 || a1.RunID != "run-a" || a2.RunID != "run-b" {
		t.Fatalf("attempts = %+v, %+v", a1, a2)
	}
	want := []pendingAttempt{{sha1Head, 1, "run-a"}, {sha1Head, 2, "run-b"}}
	if len(f.pending) != 2 || f.pending[0] != want[0] || f.pending[1] != want[1] {
		t.Fatalf("pending = %+v, want %+v", f.pending, want)
	}
	got, err := f.loadAttempt(sha1Head, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != statusRunning || !got.FinishedAt.IsZero() || got.Marker != marker || got.StartedAt.IsZero() {
		t.Fatalf("stored attempt = %+v", got)
	}
	info, err := os.Stat(f.path(checksDir, sha1Head, "2.json"))
	if err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("attempt file mode: %v, %v", info, err)
	}
	dir, err := os.Stat(f.path(checksDir, sha1Head))
	if err != nil || dir.Mode().Perm() != dirMode {
		t.Fatalf("attempt dir mode: %v, %v", dir, err)
	}
	latest, ok, err := f.latestAttempt(ctx, sha1Head)
	if err != nil || !ok || latest.Attempt != 2 {
		t.Fatalf("latest = %+v, %v, %v", latest, ok, err)
	}
	if none, err := f.listAttempts(ctx, otherSHA); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("attempts of an unknown head = %v, %v; want an empty, non-nil list", none, err)
	}
}

func TestStartAttemptLocked_RefusesUnsafeRunID(t *testing.T) {
	ids := fixedIDs{"../escape"}
	f := bareForge(t, &ids)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.startAttemptLocked(context.Background(), sha1Head, internalMarker(t)); err == nil {
		t.Fatal("unsafe run id accepted")
	}
	if len(f.pending) != 0 {
		t.Errorf("pending = %+v, want none", f.pending)
	}
	if _, err := os.Lstat(f.path(checksDir, sha1Head)); err == nil {
		t.Error("attempt dir written for a refused run id")
	}
}

func TestChangeAndReviewStorage(t *testing.T) {
	ctx := context.Background()
	f := bareForge(t, &fixedIDs{})
	for _, d := range []string{changesDir, reviewsDir} {
		if err := os.Mkdir(f.path(d), dirMode); err != nil {
			t.Fatal(err)
		}
	}
	c := changeRecord{ID: "c1", Kind: kindChange, Branch: "feat/x", BaseBranch: "main", HeadSHA: sha1Head,
		Title: "T", Marker: internalMarker(t), State: changeOpen, CreatedAt: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	if err := f.storeChange(c); err != nil {
		t.Fatal(err)
	}
	got, err := f.loadChange("c1")
	if err != nil || got.HeadSHA != sha1Head || got.Schema != changeSchema {
		t.Fatalf("load change = %+v, %v", got, err)
	}
	all, err := f.listChanges(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list changes = %v, %v", all, err)
	}
	if err := f.storeChange(changeRecord{ID: "../x"}); err == nil {
		t.Error("stored a change with an unsafe id")
	}
	if _, err := f.loadChange("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("load of a missing change: %v, want not-exist", err)
	}
	r := reviewRecord{ChangeID: "c1", HeadSHA: sha1Head, Reviewer: reviewer{ID: "rev-1", Name: "R"}, Verdict: string(model.ReviewVerdictApprove), Body: "one"}
	if err := f.storeReview(r); err != nil {
		t.Fatal(err)
	}
	r.Body = "two"
	if err := f.storeReview(r); err != nil {
		t.Fatal(err)
	}
	reviews, err := f.listReviews(ctx, "c1", sha1Head)
	if err != nil || len(reviews) != 1 || reviews[0].Body != "two" {
		t.Fatalf("list reviews = %+v, %v", reviews, err)
	}
	if none, err := f.listReviews(ctx, "c1", otherSHA); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("reviews of an unreviewed head = %v, %v", none, err)
	}
}
