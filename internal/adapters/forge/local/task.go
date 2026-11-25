package local

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// loadTasks decodes every task record, ordered by number.
func (f *Forge) loadTasks(ctx context.Context) ([]taskRecord, error) {
	entries, err := os.ReadDir(f.path(tasksDir))
	if err != nil {
		return nil, fmt.Errorf("list task records: %w", err)
	}
	var out []taskRecord
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		num, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !decimalPattern.MatchString(num) {
			return nil, fmt.Errorf("list task records: unexpected entry %q", e.Name())
		}
		n, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("list task records: %w", err)
		}
		r, err := f.loadTask(n)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b taskRecord) int { return cmp.Compare(a.Number, b.Number) })
	return out, nil
}

func (f *Forge) loadTask(n uint64) (taskRecord, error) {
	b, err := readRegular(f.path(tasksDir, formatUint(n)+".json"), maxRecordBytes)
	if err != nil {
		return taskRecord{}, fmt.Errorf("read task record %d: %w", n, err)
	}
	return decodeTask(b, n)
}

func (f *Forge) storeTask(r taskRecord) error {
	b, err := encodeRecord(r)
	if err != nil {
		return fmt.Errorf("encode task record %d: %w", r.Number, err)
	}
	if err := writeAtomic(f.path(tasksDir), formatUint(r.Number)+".json", b); err != nil {
		return fmt.Errorf("write task record %d: %w", r.Number, err)
	}
	return nil
}

// createRecord allocates the next number and writes r under it, then its
// answers directory (agentDefault 5).
func (f *Forge) createRecord(r taskRecord) (taskRecord, error) {
	n, err := f.allocate()
	if err != nil {
		return taskRecord{}, err
	}
	r.Schema = taskSchema
	r.Number = n
	if err := r.validate(n); err != nil {
		return taskRecord{}, fmt.Errorf("new task record %d: %w", n, err)
	}
	b, err := encodeRecord(r)
	if err != nil {
		return taskRecord{}, fmt.Errorf("encode task record %d: %w", n, err)
	}
	if err := writeNew(f.path(tasksDir), formatUint(n)+".json", b); err != nil {
		return taskRecord{}, fmt.Errorf("write task record %d: %w", n, err)
	}
	if err := mkdirNew(f.path(answersDir, formatUint(n))); err != nil {
		return taskRecord{}, fmt.Errorf("create answers dir %d: %w", n, err)
	}
	return r, nil
}

func (f *Forge) source(r taskRecord) model.TaskSource {
	return model.TaskSource{
		Forge:      ForgeName,
		Repository: f.cfg.ProjectID,
		SourceKind: r.Kind,
		SourceID:   formatUint(r.Number),
	}
}

// resolve returns the task record a source names, or an error for a
// source that is not this project's or names no record.
func (f *Forge) resolve(src model.TaskSource) (taskRecord, error) {
	if src.Forge != ForgeName {
		return taskRecord{}, fmt.Errorf("task source: forge %q, want %q", src.Forge, ForgeName)
	}
	if src.Repository != f.cfg.ProjectID {
		return taskRecord{}, fmt.Errorf("task source: repository %q, want %q", src.Repository, f.cfg.ProjectID)
	}
	kind, err := parseTaskKind(src.SourceKind)
	if err != nil {
		return taskRecord{}, fmt.Errorf("task source: %w", err)
	}
	if !decimalPattern.MatchString(src.SourceID) {
		return taskRecord{}, fmt.Errorf("task source: id %q is not a canonical decimal", src.SourceID)
	}
	n, err := strconv.ParseUint(src.SourceID, 10, 64)
	if err != nil {
		return taskRecord{}, fmt.Errorf("task source: %w", err)
	}
	r, err := f.loadTask(n)
	if err != nil {
		return taskRecord{}, fmt.Errorf("task source %s: %w", src.SourceID, err)
	}
	if r.Kind != string(kind) {
		return taskRecord{}, fmt.Errorf("task source %s: kind %q, record has %q", src.SourceID, kind, r.Kind)
	}
	return r, nil
}

// ListCandidateTasks returns the open task records ordered by number.
func (f *Forge) ListCandidateTasks(ctx context.Context) ([]ports.CandidateTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	records, err := f.loadTasks(ctx)
	if err != nil {
		return nil, err
	}
	out := []ports.CandidateTask{}
	for _, r := range records {
		if r.State != string(stateOpen) {
			continue
		}
		out = append(out, ports.CandidateTask{
			Source: f.source(r),
			Title:  r.Title,
			Body:   r.Body,
			Labels: slices.Clone(r.Labels),
		})
	}
	return out, nil
}

// ReadIntakeActor returns LocalOperator for an inbox record and Daemon for
// a record DDF created.
func (f *Forge) ReadIntakeActor(ctx context.Context, source model.TaskSource) (ports.Identity, error) {
	if err := ctx.Err(); err != nil {
		return ports.Identity{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.resolve(source)
	if err != nil {
		return ports.Identity{}, fmt.Errorf("read intake actor: %w", err)
	}
	return actorID(r.CreatedBy).identity(), nil
}

// ReadAnswers returns the answer files of a task ordered by time, then id
// (agentDefault 12).
func (f *Forge) ReadAnswers(ctx context.Context, source model.TaskSource) ([]ports.Answer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.resolve(source)
	if err != nil {
		return nil, fmt.Errorf("read answers: %w", err)
	}
	dir := f.path(answersDir, formatUint(r.Number))
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []ports.Answer{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read answers: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read answers: %s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read answers: %w", err)
	}
	out := []ports.Answer{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || !answerIDPattern.MatchString(id) || !e.Type().IsRegular() {
			return nil, fmt.Errorf("read answers: unexpected entry %q", name)
		}
		b, err := readRegular(dir+string(os.PathSeparator)+name, maxRecordBytes)
		if err != nil {
			return nil, fmt.Errorf("read answers: %w", err)
		}
		a, err := decodeAnswer(b)
		if err != nil {
			return nil, fmt.Errorf("read answers: decode %s: %w", name, err)
		}
		out = append(out, ports.Answer{ID: id, Body: a.Body, Author: actorID(a.Author).identity(), At: a.Time})
	}
	slices.SortFunc(out, func(a, b ports.Answer) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// CreateTask writes an open task record carrying the ddf:task-key marker of
// t.Key, or returns the source of the existing record with that marker
// (agentDefault 11).
func (f *Forge) CreateTask(ctx context.Context, mode ports.WriteMode, t ports.NewTask) (model.TaskSource, error) {
	live, err := f.guard(mode, "CreateTask", "key", string(t.Key))
	if err != nil || !live {
		return model.TaskSource{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.TaskSource{}, err
	}
	key, err := model.ParseTaskKey(string(t.Key))
	if err != nil {
		return model.TaskSource{}, fmt.Errorf("create task: %w", err)
	}
	if strings.TrimSpace(t.Title) == "" {
		return model.TaskSource{}, errors.New("create task: empty title")
	}
	if err := checkLabels(t.Labels); err != nil {
		return model.TaskSource{}, fmt.Errorf("create task: %w", err)
	}
	marker := markerPrefix + string(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	records, err := f.loadTasks(ctx)
	if err != nil {
		return model.TaskSource{}, fmt.Errorf("create task: %w", err)
	}
	for _, r := range records {
		if r.Marker == marker {
			return f.source(r), nil
		}
	}
	r, err := f.createRecord(taskRecord{
		Kind:      string(kindTask),
		Title:     t.Title,
		Body:      t.Body,
		State:     string(stateOpen),
		Labels:    normLabels(t.Labels),
		Marker:    marker,
		CreatedBy: string(actorDaemon),
		Comments:  []comment{},
	})
	if err != nil {
		return model.TaskSource{}, fmt.Errorf("create task: %w", err)
	}
	return f.source(r), nil
}

// CloseTask sets the record state to closed and appends the daemon's
// comment (agentDefault 10).
func (f *Forge) CloseTask(ctx context.Context, mode ports.WriteMode, source model.TaskSource, comment string) error {
	return f.setState(ctx, mode, "CloseTask", source, stateClosed, comment)
}

// ReopenTask sets the record state to open and appends the daemon's
// comment (agentDefault 10).
func (f *Forge) ReopenTask(ctx context.Context, mode ports.WriteMode, source model.TaskSource, comment string) error {
	return f.setState(ctx, mode, "ReopenTask", source, stateOpen, comment)
}

func (f *Forge) setState(ctx context.Context, mode ports.WriteMode, method string, source model.TaskSource, state taskState, body string) error {
	live, err := f.guard(mode, method, "sourceKind", source.SourceKind, "sourceId", source.SourceID)
	if err != nil || !live {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.resolve(source)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	r.State = string(state)
	if body != "" {
		r.Comments = append(r.Comments, comment{Body: body, Author: string(actorDaemon), Time: f.cfg.Clock.Now()})
	}
	if err := f.storeTask(r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

// SetProjectionLabels replaces the DDF-owned labels of a task record,
// stored sorted and de-duplicated.
func (f *Forge) SetProjectionLabels(ctx context.Context, mode ports.WriteMode, source model.TaskSource, labels []string) error {
	live, err := f.guard(mode, "SetProjectionLabels", "sourceKind", source.SourceKind, "sourceId", source.SourceID)
	if err != nil || !live {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkLabels(labels); err != nil {
		return fmt.Errorf("set projection labels: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.resolve(source)
	if err != nil {
		return fmt.Errorf("set projection labels: %w", err)
	}
	r.Labels = normLabels(labels)
	if err := f.storeTask(r); err != nil {
		return fmt.Errorf("set projection labels: %w", err)
	}
	return nil
}
