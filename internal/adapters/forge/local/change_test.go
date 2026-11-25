package local_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

func publishReq(key model.TaskKey, branch, head string) ports.PublishRequest {
	return ports.PublishRequest{
		Key: key, Branch: branch, BaseBranch: integrationBranch, HeadSHA: head,
		Title: "Add a", Body: "Body of a",
	}
}

func publish(t *testing.T, f *local.Forge, r ports.PublishRequest) ports.ChangeInfo {
	t.Helper()
	info, err := f.PublishChange(ctx, ports.WriteModeLive, r)
	if err != nil {
		t.Fatalf("publish change: %v", err)
	}
	return info
}

// changeFiles returns the names under changes/.
func (e *env) changeFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.path("changes"))
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, en := range entries {
		out = append(out, en.Name())
	}
	return out
}

// readChange decodes changes/<id>.json into raw fields.
func (e *env) readChange(t *testing.T, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(e.path("changes", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// changeJSON is a change record as a test writes it.
func changeJSON(id string, key model.TaskKey, state, createdAt string) map[string]any {
	m := map[string]any{
		"schema": "ddf.forge-local.change/v1", "id": id, "kind": "change", "branch": "feat/" + id,
		"baseBranch": integrationBranch, "headSha": "0123456789abcdef0123456789abcdef01234567",
		"title": "T " + id, "body": "B " + id, "marker": "ddf:task-key=" + string(key), "state": state,
		"createdAt": createdAt,
	}
	if state == "merged" {
		m["mergeSha"] = "89abcdef0123456789abcdef0123456789abcdef"
	}
	return m
}

func TestPublishChange_New(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	key := testKey(t, "1")
	info := publish(t, f, publishReq(key, "feat/a", head))
	if info.ID == "" || info.Branch != "feat/a" || info.HeadSHA != head || info.Body != "Body of a" || !info.Open {
		t.Fatalf("info = %+v", info)
	}
	if got := e.changeFiles(t); len(got) != 1 || got[0] != info.ID+".json" {
		t.Fatalf("changes/ = %v", got)
	}
	m := e.readChange(t, info.ID)
	for k, want := range map[string]any{
		"marker": "ddf:task-key=" + string(key), "kind": "change", "state": "open", "branch": "feat/a",
		"baseBranch": integrationBranch, "headSha": head, "title": "Add a", "body": "Body of a",
		"createdAt": "2026-11-25T10:00:00Z",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	if n := e.attemptCount(t, head); n != 1 {
		t.Fatalf("attempts of head = %d, want 1", n)
	}
	a := e.readAttempt(t, head, 1)
	if a["status"] != "running" || a["marker"] != "ddf:task-key="+string(key) {
		t.Errorf("attempt = %v", a)
	}
	got, found, err := f.FindChangeByTaskKey(ctx, key)
	if err != nil || !found || got != info {
		t.Errorf("find = %+v, %v, %v; want %+v", got, found, err, info)
	}
}

func TestPublishChange_IdenticalSecondCallWritesNothing(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	key := testKey(t, "1")
	first := publish(t, f, publishReq(key, "feat/a", head))
	before := snapshot(t, e.root())
	if err := e.clock.Advance(3600e9); err != nil {
		t.Fatal(err)
	}
	second := publish(t, f, publishReq(key, "feat/a", head))
	if second != first {
		t.Errorf("second = %+v, want %+v", second, first)
	}
	sameSnapshot(t, before, snapshot(t, e.root()))
	if n := e.attemptCount(t, head); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

func TestPublishChange_NewHeadUpdatesRecord(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head1 := e.commitOn(t, "feat/a", "a.txt", "a\n")
	key := testKey(t, "1")
	first := publish(t, f, publishReq(key, "feat/a", head1))
	head2 := e.commitOn(t, "feat/a", "a.txt", "a2\n")
	req := publishReq(key, "feat/a", head2)
	req.Title, req.Body = "Add a, v2", "Body v2"
	second := publish(t, f, req)
	if second.ID != first.ID || second.HeadSHA != head2 || second.Body != "Body v2" || !second.Open {
		t.Fatalf("second = %+v (first %+v)", second, first)
	}
	if got := e.changeFiles(t); len(got) != 1 {
		t.Fatalf("changes/ = %v", got)
	}
	m := e.readChange(t, first.ID)
	if m["headSha"] != head2 || m["title"] != "Add a, v2" || m["body"] != "Body v2" || m["createdAt"] != "2026-11-25T10:00:00Z" {
		t.Errorf("record = %v", m)
	}
	if n := e.attemptCount(t, head2); n != 1 {
		t.Errorf("attempts of new head = %d, want 1", n)
	}
	if n := e.attemptCount(t, head1); n != 1 {
		t.Errorf("attempts of old head = %d, want 1", n)
	}
}

func TestPublishChange_TitleOnlyChangeStartsNoAttempt(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	key := testKey(t, "1")
	info := publish(t, f, publishReq(key, "feat/a", head))
	req := publishReq(key, "feat/a", head)
	req.Title = "Renamed"
	publish(t, f, req)
	if m := e.readChange(t, info.ID); m["title"] != "Renamed" {
		t.Errorf("title = %v", m["title"])
	}
	if n := e.attemptCount(t, head); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

func TestPublishChange_PublishesBranchPushedAfterOpen(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/late", "late.txt", "late\n")
	publish(t, f, publishReq(testKey(t, "1"), "feat/late", head))
	// A branch pushed after a reopen is fetched too.
	f2 := e.open(t)
	head2 := e.commitOn(t, "feat/later", "later.txt", "later\n")
	publish(t, f2, publishReq(testKey(t, "2"), "feat/later", head2))
}

func TestPublishChange_Refused(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	headA := e.commitOn(t, "feat/a", "a.txt", "a\n")
	headB := e.commitOn(t, "feat/b", "b.txt", "b\n")
	key := testKey(t, "1")
	other := testKey(t, "2")
	publish(t, f, publishReq(key, "feat/a", headA))
	for _, tc := range []struct {
		name string
		req  func() ports.PublishRequest
	}{
		{"different branch for key", func() ports.PublishRequest { return publishReq(key, "feat/b", headB) }},
		{"base branch not integration", func() ports.PublishRequest {
			r := publishReq(other, "feat/b", headB)
			r.BaseBranch = "develop"
			return r
		}},
		{"branch is integration branch", func() ports.PublishRequest {
			return publishReq(other, integrationBranch, e.mainSHA)
		}},
		{"head is not the tip", func() ports.PublishRequest { return publishReq(other, "feat/b", headA) }},
		{"branch missing on remote", func() ports.PublishRequest { return publishReq(other, "feat/none", headB) }},
		{"invalid branch name", func() ports.PublishRequest { return publishReq(other, "feat..b", headB) }},
		{"invalid key", func() ports.PublishRequest { return publishReq("nope", "feat/b", headB) }},
		{"short head sha", func() ports.PublishRequest { return publishReq(other, "feat/b", headB[:12]) }},
		{"empty title", func() ports.PublishRequest {
			r := publishReq(other, "feat/b", headB)
			r.Title = "  "
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot(t, e.root())
			if info, err := f.PublishChange(ctx, ports.WriteModeLive, tc.req()); err == nil {
				t.Fatalf("publish = %+v, want error", info)
			}
			sameSnapshot(t, before, snapshot(t, e.root()))
		})
	}
	if got := e.changeFiles(t); len(got) != 1 {
		t.Errorf("changes/ = %v, want one record", got)
	}
}

func TestPublishChange_NewChangeAfterClosed(t *testing.T) {
	e := newEnv(t)
	key := testKey(t, "1")
	f := e.open(t)
	e.writeFile(t, "changes/old.json", mustJSON(t, changeJSON("old", key, "closed", "2026-11-24T10:00:00Z")))
	head := e.commitOn(t, "feat/b", "b.txt", "b\n")
	info := publish(t, f, publishReq(key, "feat/b", head))
	if info.ID == "old" || !info.Open {
		t.Fatalf("info = %+v, want a new open change", info)
	}
	if got := e.changeFiles(t); len(got) != 2 {
		t.Errorf("changes/ = %v", got)
	}
}

func TestFindChangeByTaskKey(t *testing.T) {
	key := testKey(t, "1")
	other := testKey(t, "2")
	for _, tc := range []struct {
		name    string
		records []map[string]any
		wantID  string
		open    bool
		found   bool
		wantErr bool
	}{
		{name: "unknown key", records: []map[string]any{changeJSON("x", other, "open", "2026-11-25T10:00:00Z")}},
		{name: "open preferred over newer closed", records: []map[string]any{
			changeJSON("c1", key, "open", "2026-11-25T10:00:00Z"),
			changeJSON("c2", key, "closed", "2026-11-25T11:00:00Z"),
		}, wantID: "c1", open: true, found: true},
		{name: "most recent when none open", records: []map[string]any{
			changeJSON("c1", key, "closed", "2026-11-25T12:00:00Z"),
			changeJSON("c2", key, "merged", "2026-11-25T11:00:00Z"),
			changeJSON("x", other, "open", "2026-11-25T13:00:00Z"),
		}, wantID: "c1", found: true},
		{name: "createdAt tie goes to the greatest id", records: []map[string]any{
			changeJSON("c2", key, "merged", "2026-11-25T11:00:00Z"),
			changeJSON("c0", key, "closed", "2026-11-25T11:00:00Z"),
			changeJSON("c1", key, "closed", "2026-11-25T10:00:00Z"),
		}, wantID: "c2", found: true},
		{name: "two open changes", records: []map[string]any{
			changeJSON("c1", key, "open", "2026-11-25T10:00:00Z"),
			changeJSON("c2", key, "open", "2026-11-25T11:00:00Z"),
		}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.open(t)
			for _, r := range tc.records {
				e.writeFile(t, "changes/"+r["id"].(string)+".json", mustJSON(t, r))
			}
			info, found, err := f.FindChangeByTaskKey(ctx, key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("find = %+v, %v; want error", info, found)
				}
				return
			}
			if err != nil || found != tc.found {
				t.Fatalf("find = %+v, %v, %v; want found=%v", info, found, err, tc.found)
			}
			if !found {
				if info != (ports.ChangeInfo{}) {
					t.Errorf("info = %+v, want zero", info)
				}
				return
			}
			want := ports.ChangeInfo{
				ID: tc.wantID, Branch: "feat/" + tc.wantID, HeadSHA: "0123456789abcdef0123456789abcdef01234567",
				Body: "B " + tc.wantID, Open: tc.open,
			}
			if info != want {
				t.Errorf("info = %+v, want %+v", info, want)
			}
		})
	}
}

func TestFindChangeByTaskKey_InvalidKey(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	if _, _, err := f.FindChangeByTaskKey(ctx, "not-a-key"); err == nil {
		t.Fatal("find with an invalid key: want error")
	}
}

func TestUpdateChangeBody(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	key := testKey(t, "1")
	info := publish(t, f, publishReq(key, "feat/a", head))
	if err := f.UpdateChangeBody(ctx, ports.WriteModeLive, info.ID, "New body"); err != nil {
		t.Fatalf("update change body: %v", err)
	}
	if m := e.readChange(t, info.ID); m["body"] != "New body" || m["title"] != "Add a" || m["headSha"] != head {
		t.Errorf("record = %v", m)
	}
	got, _, err := f.FindChangeByTaskKey(ctx, key)
	if err != nil || got.Body != "New body" {
		t.Errorf("find = %+v, %v", got, err)
	}
	if n := e.attemptCount(t, head); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
	for _, id := range []string{"unknown", "..", "a/b", ""} {
		before := snapshot(t, e.root())
		if err := f.UpdateChangeBody(ctx, ports.WriteModeLive, id, "x"); err == nil {
			t.Errorf("update change body of %q: want error", id)
		}
		sameSnapshot(t, before, snapshot(t, e.root()))
	}
}
