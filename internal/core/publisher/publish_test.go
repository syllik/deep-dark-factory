package publisher_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

// recorder is a fake Pusher and ChangeWriter that records every call in
// order with its mode.
type recorder struct {
	calls   []string
	pushErr error
	info    ports.ChangeInfo
	push    publisher.PushRequest
	change  ports.PublishRequest
}

func (r *recorder) Push(_ context.Context, mode ports.WriteMode, p publisher.PushRequest) error {
	r.calls = append(r.calls, "push:"+string(mode))
	r.push = p
	return r.pushErr
}

func (r *recorder) PublishChange(_ context.Context, mode ports.WriteMode, p ports.PublishRequest) (ports.ChangeInfo, error) {
	r.calls = append(r.calls, "change:"+string(mode))
	r.change = p
	return r.info, nil
}

func readyFixture() publisher.Ready {
	head := strings.Repeat("b", 40)
	return publisher.Ready{
		Commit: head,
		Tree:   strings.Repeat("c", 40),
		Push: publisher.PushRequest{Clone: "/var/ddf/clone", Branch: "feat/issue-1-x",
			OldSHA: "", NewSHA: head},
		Change: ports.PublishRequest{Key: "gh-1", Branch: "feat/issue-1-x", BaseBranch: "master",
			HeadSHA: head, Title: "t", Body: "b"},
	}
}

func TestPublish(t *testing.T) {
	info := ports.ChangeInfo{ID: "7", Branch: "feat/issue-1-x", HeadSHA: strings.Repeat("b", 40), Open: true}
	pushErr := errors.New("remote moved")
	cases := []struct {
		name      string
		mode      ports.WriteMode
		mutate    func(*publisher.Ready)
		pushErr   error
		wantCalls []string
		wantErr   bool
	}{
		{name: "live", mode: ports.WriteModeLive, wantCalls: []string{"push:live", "change:live"}},
		{name: "dry-run passed through", mode: ports.WriteModeDryRun,
			wantCalls: []string{"push:dry-run", "change:dry-run"}},
		{name: "correction revision", mode: ports.WriteModeLive,
			mutate:    func(r *publisher.Ready) { r.Push.OldSHA = strings.Repeat("a", 64) },
			wantCalls: []string{"push:live", "change:live"}},
		{name: "empty mode", mode: "", wantErr: true},
		{name: "unknown mode", mode: "force", wantErr: true},
		{name: "push error", mode: ports.WriteModeLive, pushErr: pushErr,
			wantCalls: []string{"push:live"}, wantErr: true},
		{name: "new sha differs from change head", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Change.HeadSHA = strings.Repeat("d", 40) }},
		{name: "branch mismatch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Change.Branch = "feat/other" }},
		{name: "empty branch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.Branch, r.Change.Branch = "", "" }},
		{name: "push branch is the base branch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.Branch, r.Change.Branch = "master", "master" }},
		{name: "empty base branch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Change.BaseBranch = "" }},
		{name: "invalid base branch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Change.BaseBranch = "a//b" }},
		{name: "invalid push branch", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.Branch, r.Change.Branch = "feat/.x", "feat/.x" }},
		{name: "short new sha", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.NewSHA, r.Change.HeadSHA = "abc123", "abc123" }},
		{name: "uppercase new sha", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) {
				s := strings.Repeat("B", 40)
				r.Push.NewSHA, r.Change.HeadSHA = s, s
			}},
		{name: "bad old sha", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.OldSHA = "HEAD" }},
		{name: "relative clone", mode: ports.WriteModeLive, wantErr: true,
			mutate: func(r *publisher.Ready) { r.Push.Clone = "clone" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := readyFixture()
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			rec := &recorder{pushErr: tc.pushErr, info: info}
			got, err := publisher.Publish(context.Background(), tc.mode, rec, rec, r)
			if !reflect.DeepEqual(rec.calls, tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", rec.calls, tc.wantCalls)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Publish = %+v, nil; want error", got)
				}
				if tc.pushErr != nil && !errors.Is(err, tc.pushErr) {
					t.Fatalf("error %v does not wrap %v", err, tc.pushErr)
				}
				if got != (ports.ChangeInfo{}) {
					t.Fatalf("Publish returned %+v with an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if got != info {
				t.Fatalf("ChangeInfo = %+v, want %+v", got, info)
			}
			if rec.push != r.Push || !reflect.DeepEqual(rec.change, r.Change) {
				t.Fatalf("recorded push %+v change %+v, want %+v %+v", rec.push, rec.change, r.Push, r.Change)
			}
		})
	}
}

func TestPublishNilWriters(t *testing.T) {
	rec := &recorder{}
	cases := []struct {
		name string
		pu   publisher.Pusher
		cw   publisher.ChangeWriter
	}{
		{name: "nil pusher", cw: rec},
		{name: "nil change writer", pu: rec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := publisher.Publish(context.Background(), ports.WriteModeLive, tc.pu, tc.cw, readyFixture()); err == nil {
				t.Fatal("Publish with a nil writer: want error")
			}
			if len(rec.calls) != 0 {
				t.Fatalf("calls = %v, want none", rec.calls)
			}
		})
	}
}
