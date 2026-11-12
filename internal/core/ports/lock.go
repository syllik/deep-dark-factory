package ports

import (
	"context"
	"errors"
)

// ErrLocked is returned by Lock.TryAcquire when another instance holds the
// lock.
var ErrLocked = errors.New("instance lock held by another instance")

// Lock is the instance lock that keeps a second daemon off the same data
// (plan OD-9).
type Lock interface {
	// TryAcquire takes the lock without waiting and returns the function
	// that releases it, or ErrLocked.
	TryAcquire(ctx context.Context) (release func() error, err error)
}
