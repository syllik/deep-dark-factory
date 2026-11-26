package local

import (
	"bytes"
	"context"
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
	changeSchema = "ddf.forge-local.change/v1"
	checkSchema  = "ddf.forge-local.check/v1"
	reviewSchema = "ddf.forge-local.review/v1"
	// checkName is the one check of every head (docs/adapters/forge-local.md#checks).
	checkName    = "verify"
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
	shaPattern      = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// validSHA accepts only a full lowercase hex SHA-1 or SHA-256.
func validSHA(s string) bool { return shaPattern.MatchString(s) }

// safeID reports whether s may name a file or directory: a change, reviewer,
// answer or run id. It matches answerIDPattern and is not "." or "..".
func safeID(s string) bool { return answerIDPattern.MatchString(s) && s != "." && s != ".." }

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

// changeKind is the kind of a change record.
type changeKind string

const (
	kindChange changeKind = "change"
	kindRevert changeKind = "revert"
)

func parseChangeKind(s string) (changeKind, error) {
	for _, v := range []changeKind{kindChange, kindRevert} {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse change kind: unknown value %q", s)
}

// changeState is the state of a change record.
type changeState string

const (
	changeOpen   changeState = "open"
	changeMerged changeState = "merged"
	changeClosed changeState = "closed"
)

func parseChangeState(s string) (changeState, error) {
	for _, v := range []changeState{changeOpen, changeMerged, changeClosed} {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse change state: unknown value %q", s)
}

// checkStatus is the status of a check attempt.
type checkStatus string

const (
	statusRunning checkStatus = "running"
	statusPassed  checkStatus = "passed"
	statusFailed  checkStatus = "failed"
	statusLost    checkStatus = "lost"
)

func parseCheckStatus(s string) (checkStatus, error) {
	for _, v := range []checkStatus{statusRunning, statusPassed, statusFailed, statusLost} {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse check status: unknown value %q", s)
}

// changeRecord is changes/<id>.json.
type changeRecord struct {
	Schema     string      `json:"schema"`
	ID         string      `json:"id"`
	Kind       changeKind  `json:"kind"`
	Branch     string      `json:"branch"`
	BaseBranch string      `json:"baseBranch"`
	HeadSHA    string      `json:"headSha"`
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	Marker     string      `json:"marker"`
	State      changeState `json:"state"`
	MergeSHA   string      `json:"mergeSha,omitempty"`
	RevertOf   string      `json:"revertOf,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
}

// attemptRecord is checks/<head sha>/<attempt>.json, one attempt of the
// verify check of a head. Marker is the task-key marker of the change that
// started it (Task 10b agentDefault 4).
type attemptRecord struct {
	Schema     string      `json:"schema"`
	Name       string      `json:"name"`
	HeadSHA    string      `json:"headSha"`
	Attempt    uint64      `json:"attempt"`
	Status     checkStatus `json:"status"`
	RunID      string      `json:"runId"`
	Marker     string      `json:"marker"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt time.Time   `json:"finishedAt,omitzero"`
}

// reviewer is the reviewer of a review projection.
type reviewer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// reviewRecord is reviews/<change id>/<head sha>/<reviewer id>.json.
type reviewRecord struct {
	Schema   string   `json:"schema"`
	ChangeID string   `json:"changeId"`
	HeadSHA  string   `json:"headSha"`
	Reviewer reviewer `json:"reviewer"`
	Verdict  string   `json:"verdict"`
	Body     string   `json:"body"`
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

// decodeChange decodes and validates the change record of file id.
func decodeChange(b []byte, id string) (changeRecord, error) {
	var r changeRecord
	if err := decodeStrict(b, &r); err != nil {
		return changeRecord{}, fmt.Errorf("decode change record %s: %w", id, err)
	}
	if err := r.validate(id); err != nil {
		return changeRecord{}, fmt.Errorf("decode change record %s: %w", id, err)
	}
	return r, nil
}

// validate checks r as the record of file id (Task 10b agentDefault 7).
func (r changeRecord) validate(id string) error {
	if r.Schema != changeSchema {
		return fmt.Errorf("schema %q, want %q", r.Schema, changeSchema)
	}
	if !safeID(id) || r.ID != id {
		return fmt.Errorf("id %q does not match file name %q", r.ID, id)
	}
	kind, err := parseChangeKind(string(r.Kind))
	if err != nil {
		return err
	}
	state, err := parseChangeState(string(r.State))
	if err != nil {
		return err
	}
	if r.Branch == "" || r.BaseBranch == "" {
		return errors.New("empty branch or base branch")
	}
	if !validSHA(r.HeadSHA) {
		return fmt.Errorf("head sha %q is not a full lowercase sha", r.HeadSHA)
	}
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("empty title")
	}
	if _, err := parseMarker(r.Marker); err != nil {
		return err
	}
	switch state {
	case changeMerged:
		if !validSHA(r.MergeSHA) {
			return fmt.Errorf("merged change: merge sha %q is not a full lowercase sha", r.MergeSHA)
		}
	case changeOpen, changeClosed:
		if r.MergeSHA != "" {
			return errors.New("merge sha on a change that is not merged")
		}
	}
	switch kind {
	case kindRevert:
		if !validSHA(r.RevertOf) {
			return fmt.Errorf("revert: revertOf %q is not a full lowercase sha", r.RevertOf)
		}
	case kindChange:
		if r.RevertOf != "" {
			return errors.New("revertOf on a change that is not a revert")
		}
	}
	if r.CreatedAt.IsZero() {
		return errors.New("missing createdAt")
	}
	return nil
}

// decodeAttempt decodes and validates check attempt n of head.
func decodeAttempt(b []byte, head string, n uint64) (attemptRecord, error) {
	var r attemptRecord
	if err := decodeStrict(b, &r); err != nil {
		return attemptRecord{}, fmt.Errorf("decode check attempt %s/%d: %w", head, n, err)
	}
	if err := r.validate(head, n); err != nil {
		return attemptRecord{}, fmt.Errorf("decode check attempt %s/%d: %w", head, n, err)
	}
	return r, nil
}

// validate checks r as attempt n of head (Task 10b agentDefault 7).
func (r attemptRecord) validate(head string, n uint64) error {
	if r.Schema != checkSchema {
		return fmt.Errorf("schema %q, want %q", r.Schema, checkSchema)
	}
	if r.Name != checkName {
		return fmt.Errorf("check name %q, want %q", r.Name, checkName)
	}
	if !validSHA(head) || r.HeadSHA != head {
		return fmt.Errorf("head sha %q does not match directory %q", r.HeadSHA, head)
	}
	if n < 1 || r.Attempt != n {
		return fmt.Errorf("attempt %d does not match file name %d", r.Attempt, n)
	}
	status, err := parseCheckStatus(string(r.Status))
	if err != nil {
		return err
	}
	if !safeID(r.RunID) {
		return fmt.Errorf("run id %q is not path-safe", r.RunID)
	}
	if _, err := parseMarker(r.Marker); err != nil {
		return err
	}
	if r.StartedAt.IsZero() {
		return errors.New("missing startedAt")
	}
	switch status {
	case statusRunning:
		if !r.FinishedAt.IsZero() {
			return errors.New("running attempt has finishedAt")
		}
	case statusPassed, statusFailed, statusLost:
		if r.FinishedAt.IsZero() {
			return fmt.Errorf("%s attempt lacks finishedAt", status)
		}
	}
	return nil
}

// decodeReview decodes and validates the projection at
// reviews/<changeID>/<head>/<reviewerID>.json.
func decodeReview(b []byte, changeID, head, reviewerID string) (reviewRecord, error) {
	var r reviewRecord
	if err := decodeStrict(b, &r); err != nil {
		return reviewRecord{}, fmt.Errorf("decode review %s/%s/%s: %w", changeID, head, reviewerID, err)
	}
	if err := r.validate(changeID, head, reviewerID); err != nil {
		return reviewRecord{}, fmt.Errorf("decode review %s/%s/%s: %w", changeID, head, reviewerID, err)
	}
	return r, nil
}

// validate checks r against its path elements (Task 10b agentDefault 7).
func (r reviewRecord) validate(changeID, head, reviewerID string) error {
	if r.Schema != reviewSchema {
		return fmt.Errorf("schema %q, want %q", r.Schema, reviewSchema)
	}
	if !safeID(changeID) || r.ChangeID != changeID {
		return fmt.Errorf("change id %q does not match directory %q", r.ChangeID, changeID)
	}
	if !validSHA(head) || r.HeadSHA != head {
		return fmt.Errorf("head sha %q does not match directory %q", r.HeadSHA, head)
	}
	if !safeID(reviewerID) || r.Reviewer.ID != reviewerID {
		return fmt.Errorf("reviewer id %q does not match file name %q", r.Reviewer.ID, reviewerID)
	}
	if _, err := model.ParseReviewVerdict(r.Verdict); err != nil {
		return err
	}
	return nil
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

// jsonNames returns the sorted names, without ".json", of the entries of
// dir. Every entry must be a regular file <name>.json with valid(name);
// any other entry is an error (fail closed, Task 10b agentDefault 7). A
// missing dir gives an error wrapping os.ErrNotExist.
func jsonNames(dir string, valid func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !e.Type().IsRegular() || !valid(name) {
			return nil, fmt.Errorf("%s: unexpected entry %q", dir, e.Name())
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// subdirNames returns the sorted names of the entries of dir; every entry
// must be a directory (not a symlink) with valid(name). A missing dir gives
// an error wrapping os.ErrNotExist.
func subdirNames(dir string, valid func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !valid(e.Name()) {
			return nil, fmt.Errorf("%s: unexpected entry %q", dir, e.Name())
		}
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out, nil
}

// loadChange reads changes/<id>.json. A path-unsafe id is an error; a
// missing record gives an error wrapping os.ErrNotExist.
func (f *Forge) loadChange(id string) (changeRecord, error) {
	if !safeID(id) {
		return changeRecord{}, fmt.Errorf("change id %q is not path-safe", id)
	}
	b, err := readRegular(f.path(changesDir, id+".json"), maxRecordBytes)
	if err != nil {
		return changeRecord{}, fmt.Errorf("read change record %s: %w", id, err)
	}
	return decodeChange(b, id)
}

// storeChange validates r and writes changes/<id>.json atomically,
// replacing an existing record.
func (f *Forge) storeChange(r changeRecord) error {
	r.Schema = changeSchema
	if err := r.validate(r.ID); err != nil {
		return fmt.Errorf("store change record %s: %w", r.ID, err)
	}
	b, err := encodeRecord(r)
	if err != nil {
		return fmt.Errorf("encode change record %s: %w", r.ID, err)
	}
	if err := writeAtomic(f.path(changesDir), r.ID+".json", b); err != nil {
		return fmt.Errorf("write change record %s: %w", r.ID, err)
	}
	return nil
}

// listChanges decodes every change record, ordered by id.
func (f *Forge) listChanges(ctx context.Context) ([]changeRecord, error) {
	ids, err := jsonNames(f.path(changesDir), safeID)
	if err != nil {
		return nil, fmt.Errorf("list change records: %w", err)
	}
	out := make([]changeRecord, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := f.loadChange(id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// loadReview reads reviews/<changeID>/<head>/<reviewerID>.json; a missing
// projection gives an error wrapping os.ErrNotExist.
func (f *Forge) loadReview(changeID, head, reviewerID string) (reviewRecord, error) {
	if !safeID(changeID) || !validSHA(head) || !safeID(reviewerID) {
		return reviewRecord{}, fmt.Errorf("review path %q/%q/%q is not path-safe", changeID, head, reviewerID)
	}
	b, err := readRegular(f.path(reviewsDir, changeID, head, reviewerID+".json"), maxRecordBytes)
	if err != nil {
		return reviewRecord{}, fmt.Errorf("read review %s/%s/%s: %w", changeID, head, reviewerID, err)
	}
	return decodeReview(b, changeID, head, reviewerID)
}

// storeReview validates r and writes its projection atomically, creating
// reviews/<change id>/<head>/ (0700) when missing and replacing the same
// reviewer's projection for that head.
func (f *Forge) storeReview(r reviewRecord) error {
	r.Schema = reviewSchema
	if err := r.validate(r.ChangeID, r.HeadSHA, r.Reviewer.ID); err != nil {
		return fmt.Errorf("store review: %w", err)
	}
	b, err := encodeRecord(r)
	if err != nil {
		return fmt.Errorf("encode review: %w", err)
	}
	for _, d := range []string{f.path(reviewsDir, r.ChangeID), f.path(reviewsDir, r.ChangeID, r.HeadSHA)} {
		if err := mkdirNew(d); err != nil {
			return fmt.Errorf("store review: %w", err)
		}
	}
	if err := writeAtomic(f.path(reviewsDir, r.ChangeID, r.HeadSHA), r.Reviewer.ID+".json", b); err != nil {
		return fmt.Errorf("write review %s/%s/%s: %w", r.ChangeID, r.HeadSHA, r.Reviewer.ID, err)
	}
	return nil
}

// listReviews decodes the projections of one change head, ordered by
// reviewer id; none gives an empty list.
func (f *Forge) listReviews(ctx context.Context, changeID, head string) ([]reviewRecord, error) {
	if !safeID(changeID) || !validSHA(head) {
		return nil, fmt.Errorf("review path %q/%q is not path-safe", changeID, head)
	}
	ids, err := jsonNames(f.path(reviewsDir, changeID, head), safeID)
	if errors.Is(err, os.ErrNotExist) {
		return []reviewRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	out := make([]reviewRecord, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := f.loadReview(changeID, head, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
