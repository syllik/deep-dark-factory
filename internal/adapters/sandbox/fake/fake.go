package fake

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var _ ports.Sandbox = (*Sandbox)(nil)

// Resource kinds the fake creates for each cell.
const (
	KindContainer = "container"
	KindVolume    = "volume"
)

var (
	// ErrNoCapacity means the cell's limits exceed the remaining host
	// capacity in at least one dimension.
	ErrNoCapacity = errors.New("no capacity")
	// ErrUnknownCell means no live or killed cell has the given ID.
	ErrUnknownCell = errors.New("unknown cell")
	// ErrCellKilled means the cell was killed and accepts no content or run.
	ErrCellKilled = errors.New("cell killed")
	// ErrNoContent means Run was called before PutContent.
	ErrNoContent = errors.New("no content in cell")
	// ErrTeardownUnverified means resources labelled with the task key remain
	// after DestroyByTaskKey.
	ErrTeardownUnverified = errors.New("teardown unverified")
)

// RunStep is one scripted result of Run for a task key: Err, when set, is
// returned instead of Outcome; otherwise Files are recorded on the cell.
type RunStep struct {
	Outcome ports.CellRunOutcome
	Files   map[string][]byte
	Err     error
}

// RunRecord is one recorded Run call.
type RunRecord struct {
	CellID string
	Argv   []string
}

// CellContentRecord is what PutContent stored for a cell: the brief and the
// sorted file paths of the workspace and the role bundle.
type CellContentRecord struct {
	Brief      []byte
	Workspace  []string
	RoleBundle []string
}

// Stats counts cells (containers): created, killed, destroyed, and live
// ones (created and neither killed nor destroyed).
type Stats struct {
	Created, Killed, Destroyed, Live int
}

type cell struct {
	instance string
	key      model.TaskKey
	runID    string
	limits   ports.CellLimits
	killed   bool
	content  *CellContentRecord
	files    map[string][]byte
}

// Host is the state that survives an instance restart: resources, scripted
// run steps, counters and total capacity. It is safe for concurrent use.
type Host struct {
	mu          sync.Mutex
	total       ports.Capacity
	next        int
	cells       map[string]*cell
	resources   map[string]ports.Resource
	scripts     map[model.TaskKey][]RunStep
	failDestroy map[model.TaskKey]int
	runs        []RunRecord
	stats       Stats
}

// NewHost returns an empty host with the given total capacity.
func NewHost(total ports.Capacity) *Host {
	return &Host{
		total:       total,
		cells:       map[string]*cell{},
		resources:   map[string]ports.Resource{},
		scripts:     map[model.TaskKey][]RunStep{},
		failDestroy: map[model.TaskKey]int{},
	}
}

// Sandbox returns a ports.Sandbox instance named instance on h. Instances on
// one host share every resource.
func (h *Host) Sandbox(instance string) *Sandbox {
	return &Sandbox{host: h, instance: instance}
}

// ScriptRun appends steps to the FIFO of run results for key.
func (h *Host) ScriptRun(key model.TaskKey, steps ...RunStep) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[key] = append(h.scripts[key], steps...)
}

// FailNextDestroy makes the next DestroyByTaskKey for key leave the volume in
// place, so its verification fails.
func (h *Host) FailNextDestroy(key model.TaskKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failDestroy[key]++
}

// CellInstance reports the instance that created the cell.
func (h *Host) CellInstance(cellID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.cells[cellID]
	if !ok {
		return "", false
	}
	return c.instance, true
}

// Content reports a copy of what PutContent stored for the cell.
func (h *Host) Content(cellID string) (CellContentRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.cells[cellID]
	if !ok || c.content == nil {
		return CellContentRecord{}, false
	}
	return CellContentRecord{
		Brief:      slices.Clone(c.content.Brief),
		Workspace:  slices.Clone(c.content.Workspace),
		RoleBundle: slices.Clone(c.content.RoleBundle),
	}, true
}

// Runs returns a copy of every recorded Run call, in call order.
func (h *Host) Runs() []RunRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RunRecord, len(h.runs))
	for i, r := range h.runs {
		out[i] = RunRecord{CellID: r.CellID, Argv: slices.Clone(r.Argv)}
	}
	return out
}

// Stats returns the cell counters.
func (h *Host) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}

// Sandbox is one instance of the fake sandbox on a Host.
type Sandbox struct {
	host     *Host
	instance string
}

// CreateCell creates a container and a volume labelled with spec's key and
// run ID and returns the container ID.
func (s *Sandbox) CreateCell(ctx context.Context, spec ports.CellSpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := model.ParseTaskKey(string(spec.Key)); err != nil {
		return "", fmt.Errorf("create cell: %w", err)
	}
	if spec.RunID == "" {
		return "", errors.New("create cell: empty run id")
	}
	l := spec.Limits
	if l.CPUMillis < 0 || l.MemoryBytes < 0 || l.DiskBytes < 0 {
		return "", fmt.Errorf("create cell: negative limits %+v", l)
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	free := h.freeLocked()
	if l.CPUMillis > free.CPUMillis || l.MemoryBytes > free.MemoryBytes || l.DiskBytes > free.DiskBytes {
		return "", fmt.Errorf("create cell for run %s: limits %+v, free %+v: %w", spec.RunID, l, free, ErrNoCapacity)
	}
	h.next++
	id := "cell-" + strconv.Itoa(h.next)
	h.cells[id] = &cell{
		instance: s.instance,
		key:      spec.Key,
		runID:    spec.RunID,
		limits:   l,
		files:    map[string][]byte{},
	}
	h.resources[id] = ports.Resource{ID: id, Kind: KindContainer, Key: spec.Key, RunID: spec.RunID}
	vol := id + "-volume"
	h.resources[vol] = ports.Resource{ID: vol, Kind: KindVolume, Key: spec.Key, RunID: spec.RunID}
	h.stats.Created++
	h.stats.Live++
	return id, nil
}

// PutContent records the brief and the file paths of the workspace and role
// bundle. It reads no file contents and executes nothing.
func (s *Sandbox) PutContent(ctx context.Context, cellID string, c ports.CellContent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace, err := paths(c.Workspace)
	if err != nil {
		return fmt.Errorf("put content in %s: workspace: %w", cellID, err)
	}
	bundle, err := paths(c.RoleBundle)
	if err != nil {
		return fmt.Errorf("put content in %s: role bundle: %w", cellID, err)
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	cl, err := h.usableLocked(cellID)
	if err != nil {
		return fmt.Errorf("put content: %w", err)
	}
	cl.content = &CellContentRecord{Brief: slices.Clone(c.Brief), Workspace: workspace, RoleBundle: bundle}
	return nil
}

// Run returns the next scripted step for the cell's task key, or exit code 0
// when none is scripted. It executes nothing.
func (s *Sandbox) Run(ctx context.Context, cellID string, r ports.CellRun) (ports.CellRunOutcome, error) {
	if err := ctx.Err(); err != nil {
		return ports.CellRunOutcome{}, err
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	cl, err := h.usableLocked(cellID)
	if err != nil {
		return ports.CellRunOutcome{}, fmt.Errorf("run: %w", err)
	}
	if cl.content == nil {
		return ports.CellRunOutcome{}, fmt.Errorf("run in %s: %w", cellID, ErrNoContent)
	}
	if len(r.Argv) == 0 {
		return ports.CellRunOutcome{}, fmt.Errorf("run in %s: empty argv", cellID)
	}
	h.runs = append(h.runs, RunRecord{CellID: cellID, Argv: slices.Clone(r.Argv)})
	queue := h.scripts[cl.key]
	if len(queue) == 0 {
		return ports.CellRunOutcome{ExitCode: 0}, nil
	}
	step := queue[0]
	h.scripts[cl.key] = queue[1:]
	if step.Err != nil {
		return ports.CellRunOutcome{}, step.Err
	}
	for name, data := range step.Files {
		cl.files[name] = slices.Clone(data)
	}
	return step.Outcome, nil
}

// GetFiles returns copies of the requested files present in the cell;
// missing names are omitted. It works on killed cells.
func (s *Sandbox) GetFiles(ctx context.Context, cellID string, names []string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	cl, ok := h.cells[cellID]
	if !ok {
		return nil, fmt.Errorf("get files from %s: %w", cellID, ErrUnknownCell)
	}
	out := map[string][]byte{}
	for _, n := range names {
		if data, ok := cl.files[n]; ok {
			out[n] = slices.Clone(data)
		}
	}
	return out, nil
}

// Kill marks the cell killed; it stays listed until destroyed and frees its
// capacity. Killing a killed cell is not an error.
func (s *Sandbox) Kill(ctx context.Context, cellID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	cl, ok := h.cells[cellID]
	if !ok {
		return fmt.Errorf("kill %s: %w", cellID, ErrUnknownCell)
	}
	if !cl.killed {
		cl.killed = true
		h.stats.Killed++
		h.stats.Live--
	}
	return nil
}

// ListResources returns every resource matching f, sorted by ID.
func (s *Sandbox) ListResources(ctx context.Context, f ports.ResourceFilter) ([]ports.Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listLocked(f), nil
}

// DestroyByTaskKey removes every resource labelled key, then verifies by
// listing that none remains.
func (s *Sandbox) DestroyByTaskKey(ctx context.Context, key model.TaskKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := model.ParseTaskKey(string(key)); err != nil {
		return fmt.Errorf("destroy by task key: %w", err)
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	keepVolumes := h.failDestroy[key] > 0
	if keepVolumes {
		h.failDestroy[key]--
		if h.failDestroy[key] == 0 {
			delete(h.failDestroy, key)
		}
	}
	for _, r := range h.listLocked(ports.ResourceFilter{Key: key}) {
		if keepVolumes && r.Kind == KindVolume {
			continue
		}
		delete(h.resources, r.ID)
		if cl, ok := h.cells[r.ID]; ok {
			if !cl.killed {
				h.stats.Live--
			}
			h.stats.Destroyed++
			delete(h.cells, r.ID)
		}
	}
	if left := h.listLocked(ports.ResourceFilter{Key: key}); len(left) > 0 {
		ids := make([]string, len(left))
		for i, r := range left {
			ids[i] = r.ID
		}
		return fmt.Errorf("destroy by task key %s: remaining %s: %w", key, strings.Join(ids, ", "), ErrTeardownUnverified)
	}
	return nil
}

// HostCapacity returns the total capacity minus the limits of live cells,
// never below zero.
func (s *Sandbox) HostCapacity(ctx context.Context) (ports.Capacity, error) {
	if err := ctx.Err(); err != nil {
		return ports.Capacity{}, err
	}
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.freeLocked(), nil
}

func (h *Host) usableLocked(cellID string) (*cell, error) {
	cl, ok := h.cells[cellID]
	if !ok {
		return nil, fmt.Errorf("cell %s: %w", cellID, ErrUnknownCell)
	}
	if cl.killed {
		return nil, fmt.Errorf("cell %s: %w", cellID, ErrCellKilled)
	}
	return cl, nil
}

func (h *Host) freeLocked() ports.Capacity {
	free := h.total
	for _, cl := range h.cells {
		if cl.killed {
			continue
		}
		free.CPUMillis -= cl.limits.CPUMillis
		free.MemoryBytes -= cl.limits.MemoryBytes
		free.DiskBytes -= cl.limits.DiskBytes
	}
	free.CPUMillis = max(free.CPUMillis, 0)
	free.MemoryBytes = max(free.MemoryBytes, 0)
	free.DiskBytes = max(free.DiskBytes, 0)
	return free
}

func (h *Host) listLocked(f ports.ResourceFilter) []ports.Resource {
	out := []ports.Resource{}
	for _, id := range slices.Sorted(maps.Keys(h.resources)) {
		r := h.resources[id]
		if f.Key != "" && r.Key != f.Key {
			continue
		}
		if f.RunID != "" && r.RunID != f.RunID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// paths walks fsys and returns its file paths, sorted; a nil fsys is empty.
func paths(fsys fs.FS) ([]string, error) {
	out := []string{}
	if fsys == nil {
		return out, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
