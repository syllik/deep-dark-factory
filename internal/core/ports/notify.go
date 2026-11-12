package ports

import (
	"context"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Notification is one event sent to the owner. Key is empty for a global
// event.
type Notification struct {
	Key         model.TaskKey
	Event, Text string
}

// OwnerMessage is one answer or command received from the owner.
type OwnerMessage struct {
	Sender Identity
	Key    model.TaskKey
	Text   string
	At     time.Time
}

// Notify sends events to the owner and receives the owner's answers and
// commands.
type Notify interface {
	Send(ctx context.Context, n Notification) error
	Receive(ctx context.Context) ([]OwnerMessage, error)
}
