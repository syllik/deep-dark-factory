package ports

import "time"

// Clock is the only source of the current time for the core.
type Clock interface {
	Now() time.Time
}

// IDSource is the only source of new unique IDs for the core.
type IDSource interface {
	NewID() string
}
