package ports

import (
	"context"
	"io/fs"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// CellLimits bounds the resources of a run cell.
type CellLimits struct {
	CPUMillis, MemoryBytes, DiskBytes int64
}

// NetworkRules lists the egress destinations a cell may reach; empty means
// none.
type NetworkRules struct {
	AllowedEgress []string
}

// CellSpec describes a run cell, labelled with its task key and run ID.
type CellSpec struct {
	Key     model.TaskKey
	RunID   string
	Limits  CellLimits
	Network NetworkRules
}

// CellContent is what goes into a cell. No field is a host path; how
// content moves is the adapter's matter (spec §8.3).
type CellContent struct {
	Brief                 []byte
	RoleBundle, Workspace fs.FS
}

// CellRun is a command run inside a cell.
type CellRun struct {
	Argv []string
}

// CellRunOutcome is how a command in a cell ended.
type CellRunOutcome struct {
	ExitCode int
	TimedOut bool
}

// ResourceFilter selects resources by label; an empty field matches any
// value.
type ResourceFilter struct {
	Key   model.TaskKey
	RunID string
}

// Resource is one sandbox resource (container, volume, network) with its
// labels.
type Resource struct {
	ID, Kind string
	Key      model.TaskKey
	RunID    string
}

// Capacity is the host capacity available to new cells.
type Capacity struct {
	CPUMillis, MemoryBytes, DiskBytes int64
}

// Sandbox is the sandbox port (spec §4.2, §8.4). No operation takes a host
// path.
type Sandbox interface {
	// CreateCell returns the new cell's ID.
	CreateCell(ctx context.Context, spec CellSpec) (string, error)
	PutContent(ctx context.Context, cellID string, c CellContent) error
	Run(ctx context.Context, cellID string, r CellRun) (CellRunOutcome, error)
	// GetFiles returns the named files inside the cell, such as result.json
	// and change.patch, keyed by name.
	GetFiles(ctx context.Context, cellID string, names []string) (map[string][]byte, error)
	Kill(ctx context.Context, cellID string) error
	ListResources(ctx context.Context, f ResourceFilter) ([]Resource, error)
	// DestroyByTaskKey destroys every resource labelled key, then verifies
	// by listing that none remains; an unverifiable teardown is an error.
	DestroyByTaskKey(ctx context.Context, key model.TaskKey) error
	HostCapacity(ctx context.Context) (Capacity, error)
}
