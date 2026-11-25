package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// Record schemas (docs/adapters/forge-local.md#records).
const (
	formatMarker = "ddf.forge-local/v1"
	taskSchema   = "ddf.forge-local.task/v1"
	answerSchema = "ddf.forge-local.answer/v1"
	markerPrefix = "ddf:task-key="
	tempPrefix   = ".ddf-tmp-"

	// maxRecordBytes bounds one record read.
	maxRecordBytes = 16 << 20

	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// taskKind is the kind of a task record and the source kind of its task
// source.
type taskKind string

const (
	kindTask    taskKind = "task"
	kindRequest taskKind = "request"
)

func parseTaskKind(s string) (taskKind, error) {
	for _, k := range []taskKind{kindTask, kindRequest} {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("parse task kind: unknown value %q", s)
}

// taskState is the state of a task record.
type taskState string

const (
	stateOpen   taskState = "open"
	stateClosed taskState = "closed"
)

func parseTaskState(s string) (taskState, error) {
	for _, v := range []taskState{stateOpen, stateClosed} {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse task state: unknown value %q", s)
}

// actorID is the stored ID of an identity: createdBy, comment and answer
// author.
type actorID string

const (
	actorLocalOperator actorID = "local-operator"
	actorDaemon        actorID = "ddf-daemon"
)

func parseActorID(s string) (actorID, error) {
	for _, v := range []actorID{actorLocalOperator, actorDaemon} {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse actor: unknown value %q", s)
}

// identity returns the identity a stored actor ID stands for.
func (a actorID) identity() ports.Identity {
	switch a {
	case actorLocalOperator:
		return ports.Identity{ID: string(actorLocalOperator), Name: localOperatorName}
	case actorDaemon:
		return ports.Identity{ID: string(actorDaemon), Name: daemonName}
	}
	return ports.Identity{ID: string(a)}
}

var (
	digestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	decimalPattern  = regexp.MustCompile(`^[1-9][0-9]*$`)
	answerIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	counterPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\n$`)
)

// comment is one comment on a task record.
type comment struct {
	Body   string    `json:"body"`
	Author string    `json:"author"`
	Time   time.Time `json:"time"`
}

// taskRecord is tasks/<n>.json.
type taskRecord struct {
	Schema     string    `json:"schema"`
	Number     uint64    `json:"number"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	State      string    `json:"state"`
	Labels     []string  `json:"labels"`
	ParentKey  string    `json:"parentKey,omitempty"`
	Marker     string    `json:"marker,omitempty"`
	CreatedBy  string    `json:"createdBy"`
	SourceFile string    `json:"sourceFile,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Comments   []comment `json:"comments"`
}

// taskWire is taskRecord with labels and comments as pointers, so that a
// missing or null array is refused on decode.
type taskWire struct {
	Schema     string     `json:"schema"`
	Number     uint64     `json:"number"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	State      string     `json:"state"`
	Labels     *[]string  `json:"labels"`
	ParentKey  string     `json:"parentKey,omitempty"`
	Marker     string     `json:"marker,omitempty"`
	CreatedBy  string     `json:"createdBy"`
	SourceFile string     `json:"sourceFile,omitempty"`
	Digest     string     `json:"digest,omitempty"`
	Comments   *[]comment `json:"comments"`
}

// answerRecord is answers/<n>/<id>.json.
type answerRecord struct {
	Schema string    `json:"schema"`
	Body   string    `json:"body"`
	Author string    `json:"author"`
	Time   time.Time `json:"time"`
}

// decodeStrict decodes exactly one JSON object from b into v: unknown
// fields and trailing data are refused.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the object")
	}
	return nil
}

func encodeRecord(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// decodeTask decodes and validates the record of task number n.
func decodeTask(b []byte, n uint64) (taskRecord, error) {
	var w taskWire
	if err := decodeStrict(b, &w); err != nil {
		return taskRecord{}, fmt.Errorf("decode task record %d: %w", n, err)
	}
	if w.Labels == nil || w.Comments == nil {
		return taskRecord{}, fmt.Errorf("decode task record %d: labels and comments must be arrays", n)
	}
	r := taskRecord{
		Schema: w.Schema, Number: w.Number, Kind: w.Kind, Title: w.Title, Body: w.Body,
		State: w.State, Labels: *w.Labels, ParentKey: w.ParentKey, Marker: w.Marker,
		CreatedBy: w.CreatedBy, SourceFile: w.SourceFile, Digest: w.Digest, Comments: *w.Comments,
	}
	if err := r.validate(n); err != nil {
		return taskRecord{}, fmt.Errorf("decode task record %d: %w", n, err)
	}
	return r, nil
}

func (r taskRecord) validate(n uint64) error {
	if r.Schema != taskSchema {
		return fmt.Errorf("schema %q, want %q", r.Schema, taskSchema)
	}
	if r.Number != n {
		return fmt.Errorf("number %d does not match file name %d", r.Number, n)
	}
	kind, err := parseTaskKind(r.Kind)
	if err != nil {
		return err
	}
	if _, err := parseTaskState(r.State); err != nil {
		return err
	}
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("empty title")
	}
	if err := checkLabels(r.Labels); err != nil {
		return err
	}
	if !slices.IsSorted(r.Labels) || len(slices.Compact(slices.Clone(r.Labels))) != len(r.Labels) {
		return errors.New("labels not sorted and unique")
	}
	if r.ParentKey != "" {
		if _, err := model.ParseTaskKey(r.ParentKey); err != nil {
			return fmt.Errorf("parent key: %w", err)
		}
	}
	createdBy, err := parseActorID(r.CreatedBy)
	if err != nil {
		return fmt.Errorf("created by: %w", err)
	}
	switch createdBy {
	case actorLocalOperator:
		if r.Marker != "" {
			return errors.New("inbox record carries a marker")
		}
		if !digestPattern.MatchString(r.Digest) {
			return errors.New("inbox record digest is not 64 lowercase hex")
		}
		k, ok := inboxKind(r.SourceFile)
		if !ok || strings.HasPrefix(r.SourceFile, ".") || strings.ContainsAny(r.SourceFile, `/\`) {
			return fmt.Errorf("invalid source file %q", r.SourceFile)
		}
		if k != kind {
			return errors.New("source file suffix does not match kind")
		}
	case actorDaemon:
		if _, err := parseMarker(r.Marker); err != nil {
			return err
		}
		if kind != kindTask {
			return errors.New("daemon record of kind request")
		}
		if r.SourceFile != "" || r.Digest != "" {
			return errors.New("daemon record carries a source file or digest")
		}
	}
	for i, c := range r.Comments {
		if c.Body == "" {
			return fmt.Errorf("comment %d: empty body", i)
		}
		if _, err := parseActorID(c.Author); err != nil {
			return fmt.Errorf("comment %d author: %w", i, err)
		}
		if c.Time.IsZero() {
			return fmt.Errorf("comment %d: missing time", i)
		}
	}
	return nil
}

// parseMarker returns the task key of a ddf:task-key marker.
func parseMarker(m string) (model.TaskKey, error) {
	rest, ok := strings.CutPrefix(m, markerPrefix)
	if !ok {
		return "", fmt.Errorf("marker %q lacks prefix %q", m, markerPrefix)
	}
	k, err := model.ParseTaskKey(rest)
	if err != nil {
		return "", fmt.Errorf("marker: %w", err)
	}
	return k, nil
}

func decodeAnswer(b []byte) (answerRecord, error) {
	var a answerRecord
	if err := decodeStrict(b, &a); err != nil {
		return answerRecord{}, err
	}
	if a.Schema != answerSchema {
		return answerRecord{}, fmt.Errorf("schema %q, want %q", a.Schema, answerSchema)
	}
	author, err := parseActorID(a.Author)
	if err != nil {
		return answerRecord{}, fmt.Errorf("author: %w", err)
	}
	if author != actorLocalOperator {
		return answerRecord{}, fmt.Errorf("author %q, want %q", a.Author, actorLocalOperator)
	}
	if a.Time.IsZero() {
		return answerRecord{}, errors.New("missing time")
	}
	return a, nil
}

// checkLabels refuses an empty label.
func checkLabels(labels []string) error {
	for _, l := range labels {
		if l == "" {
			return errors.New("empty label")
		}
	}
	return nil
}

// normLabels returns labels sorted and de-duplicated, never nil.
func normLabels(labels []string) []string {
	out := slices.Clone(labels)
	slices.Sort(out)
	out = slices.Compact(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// readRegular reads a regular file (never through a symlink), bounded by
// limit bytes.
func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only file; a close error loses nothing
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while opening", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return b, nil
}

// syncDir fsyncs a directory so a rename or removal in it is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close() // the sync error is the one reported
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return d.Close()
}

// writeAtomic writes data to dir/name: a temp file in dir, fsync, rename,
// fsync of dir. The file has mode 0600.
func writeAtomic(dir, name string, data []byte) (err error) {
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp) // best effort; Open removes stray temp files
		}
	}()
	if err = f.Chmod(fileMode); err != nil {
		_ = f.Close() // the chmod error is the one reported
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close() // the write error is the one reported
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close() // the sync error is the one reported
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

// writeNew writes dir/name atomically and refuses an existing file.
func writeNew(dir, name string, data []byte) error {
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return fmt.Errorf("%s already exists", filepath.Join(dir, name))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomic(dir, name, data)
}

// mkdirNew creates dir with mode 0700 if missing and fsyncs its parent; an
// existing entry must be a directory and not a symlink.
func mkdirNew(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(dir, dirMode); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dir))
}

func formatUint(n uint64) string { return strconv.FormatUint(n, 10) }
