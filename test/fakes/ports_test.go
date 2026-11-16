package fakes_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNotify_SendRecordsInOrder(t *testing.T) {
	n := fakes.NewNotify()
	ctx := context.Background()
	want := []ports.Notification{{Key: "T-1", Event: "merged", Text: "a"}, {Event: "paused", Text: "b"}}
	for _, m := range want {
		if err := n.Send(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got := n.Sent()
	if !slices.Equal(got, want) {
		t.Errorf("Sent = %+v, want %+v", got, want)
	}
	got[0].Text = "changed"
	if n.Sent()[0].Text != "a" {
		t.Error("Sent returned the internal slice")
	}
}

func TestNotify_SendError(t *testing.T) {
	n := fakes.NewNotify()
	ctx := context.Background()
	boom := errors.New("notify down")
	n.SetSendError(boom)
	if err := n.Send(ctx, ports.Notification{Event: "x"}); !errors.Is(err, boom) {
		t.Errorf("Send = %v, want %v", err, boom)
	}
	if len(n.Sent()) != 0 {
		t.Error("failed Send was recorded")
	}
	n.SetSendError(nil)
	if err := n.Send(ctx, ports.Notification{Event: "y"}); err != nil || len(n.Sent()) != 1 {
		t.Errorf("Send after clearing the error = %v, sent %d", err, len(n.Sent()))
	}
}

func TestNotify_ReceiveReturnsAndClears(t *testing.T) {
	n := fakes.NewNotify()
	ctx := context.Background()
	if got, err := n.Receive(ctx); err != nil || len(got) != 0 {
		t.Fatalf("Receive on empty queue = %v, %v; want empty, nil", got, err)
	}
	m1 := ports.OwnerMessage{Sender: ports.Identity{ID: "owner"}, Key: "T-1", Text: "yes"}
	m2 := ports.OwnerMessage{Text: "pause"}
	n.Deliver(m1)
	n.Deliver(m2)
	got, err := n.Receive(ctx)
	if err != nil || !slices.Equal(got, []ports.OwnerMessage{m1, m2}) {
		t.Errorf("Receive = %+v, %v", got, err)
	}
	if got, err := n.Receive(ctx); err != nil || len(got) != 0 {
		t.Errorf("second Receive = %v, %v; want empty, nil", got, err)
	}
}

func TestSecrets_Read(t *testing.T) {
	const value = "test-secret-value"
	s := fakes.NewSecrets(map[string]string{"review-key": value})
	ctx := context.Background()
	got, err := s.Read(ctx, "review-key")
	if err != nil || string(got.Bytes()) != value {
		t.Fatalf("Read = %v, %v", got, err)
	}
	_, err = s.Read(ctx, "missing")
	if !errors.Is(err, fakes.ErrSecretNotFound) || !strings.Contains(err.Error(), "missing") {
		t.Errorf("Read unknown = %v, want ErrSecretNotFound naming the secret", err)
	}
	s.Set("missing", "test-secret-other")
	if got, err := s.Read(ctx, "missing"); err != nil || string(got.Bytes()) != "test-secret-other" {
		t.Errorf("Read after Set = %v, %v", got, err)
	}
}

func TestSecrets_ConstructorCopiesMap(t *testing.T) {
	m := map[string]string{"a": "test-secret-a"}
	s := fakes.NewSecrets(m)
	delete(m, "a")
	if _, err := s.Read(context.Background(), "a"); err != nil {
		t.Errorf("Read after caller mutation = %v", err)
	}
}

func TestPorts_CancelledContext(t *testing.T) {
	ctx := cancelled()
	tests := []struct {
		name string
		call func() error
	}{
		{"notify send", func() error { return fakes.NewNotify().Send(ctx, ports.Notification{}) }},
		{"notify receive", func() error { _, err := fakes.NewNotify().Receive(ctx); return err }},
		{"secrets read", func() error { _, err := fakes.NewSecrets(map[string]string{"a": "b"}).Read(ctx, "a"); return err }},
		{"lock try-acquire (ctx checked first)", func() error { _, err := fakes.NewLock().TryAcquire(ctx); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
		})
	}
}

func TestClock(t *testing.T) {
	start := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	c := fakes.NewClock(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	if err := c.Advance(90 * time.Second); err != nil {
		t.Fatal(err)
	}
	if want := start.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Errorf("Now after Advance = %v, want %v", c.Now(), want)
	}
	if err := c.Advance(0); err != nil {
		t.Errorf("Advance(0) = %v", err)
	}
	before := c.Now()
	if err := c.Advance(-time.Second); err == nil {
		t.Error("Advance(-1s) = nil, want an error")
	}
	if !c.Now().Equal(before) {
		t.Error("refused Advance moved the clock")
	}
	c.Set(start)
	if !c.Now().Equal(start) {
		t.Errorf("Now after Set = %v, want %v", c.Now(), start)
	}
}

func TestIDs_Sequential(t *testing.T) {
	ids := fakes.NewIDs("run")
	for _, want := range []string{"run-1", "run-2", "run-3"} {
		if got := ids.NewID(); got != want {
			t.Errorf("NewID = %q, want %q", got, want)
		}
	}
	if got := fakes.NewIDs("run").NewID(); got != "run-1" {
		t.Errorf("fresh source NewID = %q, want run-1", got)
	}
}

func TestLock_OneHolder(t *testing.T) {
	l := fakes.NewLock() // one lock file shared by two instances
	ctx := context.Background()
	release, err := l.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("first TryAcquire = %v", err)
	}
	if _, err := l.TryAcquire(ctx); !errors.Is(err, ports.ErrLocked) {
		t.Errorf("second instance TryAcquire = %v, want ErrLocked", err)
	}
	if err := release(); err != nil {
		t.Errorf("release = %v", err)
	}
	if err := release(); !errors.Is(err, fakes.ErrLockReleased) {
		t.Errorf("second release = %v, want ErrLockReleased", err)
	}
	release2, err := l.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("TryAcquire after release = %v", err)
	}
	if err := release(); !errors.Is(err, fakes.ErrLockReleased) {
		t.Errorf("stale release = %v, want ErrLockReleased", err)
	}
	if _, err := l.TryAcquire(ctx); !errors.Is(err, ports.ErrLocked) {
		t.Errorf("stale release freed the new holder's lock: %v", err)
	}
	if err := release2(); err != nil {
		t.Errorf("release2 = %v", err)
	}
}
