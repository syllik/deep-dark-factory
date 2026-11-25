package local_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// readReview decodes reviews/<change>/<head>/<reviewer>.json.
func (e *env) readReview(t *testing.T, changeID, head, reviewerID string) map[string]any {
	t.Helper()
	p := e.path("reviews", changeID, head, reviewerID+".json")
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("%s mode = %v, want 0600", p, info.Mode().Perm())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPostReview_WritesAndReplaces(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	info := publish(t, f, publishReq(testKey(t, "1"), "feat/a", head))
	post := ports.ReviewPost{
		ChangeID: info.ID, HeadSHA: head, Reviewer: ports.Identity{ID: "rev-a", Name: "Reviewer A"},
		Verdict: model.ReviewVerdictApprove, Body: "Looks good",
	}
	if err := f.PostReview(ctx, ports.WriteModeLive, post); err != nil {
		t.Fatalf("post review: %v", err)
	}
	m := e.readReview(t, info.ID, head, "rev-a")
	want := map[string]any{
		"schema": "ddf.forge-local.review/v1", "changeId": info.ID, "headSha": head,
		"reviewer": map[string]any{"id": "rev-a", "name": "Reviewer A"},
		"verdict":  "APPROVE", "body": "Looks good",
	}
	if mustString(t, m) != mustString(t, want) {
		t.Errorf("projection = %v, want %v", m, want)
	}
	for _, d := range []string{e.path("reviews", info.ID), e.path("reviews", info.ID, head)} {
		st, err := os.Lstat(d)
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v, want a 0700 directory", d, st, err)
		}
	}

	post.Verdict, post.Body = model.ReviewVerdictRequestChanges, "Fix it"
	if err := f.PostReview(ctx, ports.WriteModeLive, post); err != nil {
		t.Fatalf("second post: %v", err)
	}
	m = e.readReview(t, info.ID, head, "rev-a")
	if m["verdict"] != "REQUEST_CHANGES" || m["body"] != "Fix it" {
		t.Errorf("replaced projection = %v", m)
	}
	entries, err := os.ReadDir(e.path("reviews", info.ID, head))
	if err != nil || len(entries) != 1 {
		t.Errorf("reviews dir = %v, %v; want one file", entries, err)
	}

	// Another reviewer adds a projection; an older head is accepted too.
	post.Reviewer = ports.Identity{ID: "rev-b", Name: "Reviewer B"}
	if err := f.PostReview(ctx, ports.WriteModeLive, post); err != nil {
		t.Fatalf("post by rev-b: %v", err)
	}
	post.HeadSHA = e.mainSHA
	if err := f.PostReview(ctx, ports.WriteModeLive, post); err != nil {
		t.Fatalf("post on older head: %v", err)
	}
	if m := e.readReview(t, info.ID, e.mainSHA, "rev-b"); m["headSha"] != e.mainSHA {
		t.Errorf("older head projection = %v", m)
	}
}

func TestPostReview_Refused(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	info := publish(t, f, publishReq(testKey(t, "1"), "feat/a", head))
	valid := func() ports.ReviewPost {
		return ports.ReviewPost{
			ChangeID: info.ID, HeadSHA: head, Reviewer: ports.Identity{ID: "rev-a", Name: "Reviewer A"},
			Verdict: model.ReviewVerdictApprove, Body: "ok",
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ports.ReviewPost)
	}{
		{"unknown change id", func(p *ports.ReviewPost) { p.ChangeID = "unknown" }},
		{"path-unsafe change id", func(p *ports.ReviewPost) { p.ChangeID = "../x" }},
		{"reviewer id dot-dot", func(p *ports.ReviewPost) { p.Reviewer.ID = ".." }},
		{"reviewer id with slash", func(p *ports.ReviewPost) { p.Reviewer.ID = "a/b" }},
		{"empty reviewer id", func(p *ports.ReviewPost) { p.Reviewer.ID = "" }},
		{"invalid verdict", func(p *ports.ReviewPost) { p.Verdict = "MAYBE" }},
		{"empty verdict", func(p *ports.ReviewPost) { p.Verdict = "" }},
		{"short head sha", func(p *ports.ReviewPost) { p.HeadSHA = head[:12] }},
		{"uppercase head sha", func(p *ports.ReviewPost) { p.HeadSHA = "ABCDEF0123456789ABCDEF0123456789ABCDEF01" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid()
			tc.mutate(&p)
			before := snapshot(t, e.root())
			if err := f.PostReview(ctx, ports.WriteModeLive, p); err == nil {
				t.Fatal("post review: want error")
			}
			sameSnapshot(t, before, snapshot(t, e.root()))
		})
	}
}

func mustString(t *testing.T, v any) string {
	t.Helper()
	return string(mustJSON(t, v))
}
