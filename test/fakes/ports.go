package fakes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var (
	_ ports.Notify   = (*Notify)(nil)
	_ ports.Secrets  = (*Secrets)(nil)
	_ ports.Clock    = (*Clock)(nil)
	_ ports.IDSource = (*IDs)(nil)
	_ ports.Lock     = (*Lock)(nil)
)

// ErrSecretNotFound is wrapped by Secrets.Read for an unknown name.
var ErrSecretNotFound = errors.New("secret not found")

// ErrLockReleased is returned by a release function called a second time.
var ErrLockReleased = errors.New("lock already released")

// Notify is an in-memory ports.Notify: it records sent notifications and
// returns queued owner messages. The zero value is ready to use.
type Notify struct {
	mu      sync.Mutex
	sent    []ports.Notification
	sendErr error
	inbox   []ports.OwnerMessage
}

// NewNotify returns an empty Notify.
func NewNotify() *Notify { return &Notify{} }

// Send records n, or returns the error set by SetSendError without recording.
func (n *Notify) Send(ctx context.Context, msg ports.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sendErr != nil {
		return n.sendErr
	}
	n.sent = append(n.sent, msg)
	return nil
}

// Sent returns the recorded notifications in send order.
func (n *Notify) Sent() []ports.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ports.Notification(nil), n.sent...)
}

// SetSendError makes every following Send fail with err; nil restores
// success.
func (n *Notify) SetSendError(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sendErr = err
}

// Deliver queues owner messages for the next Receive.
func (n *Notify) Deliver(msgs ...ports.OwnerMessage) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.inbox = append(n.inbox, msgs...)
}

// Receive returns the queued owner messages in delivery order and clears the
// queue.
func (n *Notify) Receive(ctx context.Context) ([]ports.OwnerMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	msgs := n.inbox
	n.inbox = nil
	return msgs, nil
}

// Secrets is an in-memory ports.Secrets keyed by name.
type Secrets struct {
	mu     sync.Mutex
	values map[string]string
}

// NewSecrets returns Secrets holding a copy of values.
func NewSecrets(values map[string]string) *Secrets {
	s := &Secrets{values: make(map[string]string, len(values))}
	for k, v := range values {
		s.values[k] = v
	}
	return s
}

// Set stores value under name.
func (s *Secrets) Set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[name] = value
}

// Read implements ports.Secrets; an unknown name wraps ErrSecretNotFound.
func (s *Secrets) Read(ctx context.Context, name string) (ports.Secret, error) {
	if err := ctx.Err(); err != nil {
		return ports.Secret{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[name]
	if !ok {
		return ports.Secret{}, fmt.Errorf("read secret %q: %w", name, ErrSecretNotFound)
	}
	return ports.NewSecret([]byte(v)), nil
}

// Clock is a manual ports.Clock: time moves only by Advance and Set.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock reading start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now implements ports.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d; a negative d is refused and the
// clock stays.
func (c *Clock) Advance(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("advance clock: negative duration %s", d)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

// Set sets the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// IDs is a deterministic ports.IDSource yielding prefix-1, prefix-2, ...
type IDs struct {
	prefix string

	mu sync.Mutex
	n  int
}

// NewIDs returns an IDs with prefix.
func NewIDs(prefix string) *IDs { return &IDs{prefix: prefix} }

// NewID implements ports.IDSource.
func (s *IDs) NewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("%s-%d", s.prefix, s.n)
}

// Lock is an in-memory ports.Lock. One *Lock stands for one lock file shared
// by every instance that holds it (plan OD-9). The zero value is ready to
// use.
type Lock struct {
	mu   sync.Mutex
	held bool
}

// NewLock returns a free Lock.
func NewLock() *Lock { return &Lock{} }

// TryAcquire implements ports.Lock. The returned release frees the lock once;
// a second call returns ErrLockReleased.
func (l *Lock) TryAcquire(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return nil, ports.ErrLocked
	}
	l.held = true
	released := false
	return func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return ErrLockReleased
		}
		released = true
		l.held = false
		return nil
	}, nil
}
