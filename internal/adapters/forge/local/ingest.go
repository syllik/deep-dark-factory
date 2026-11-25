package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// MaxTaskFileBytes is the size bound of an inbox task file
// (docs/adapters/forge-local.md#task-files).
const MaxTaskFileBytes = 64 << 10

const (
	taskSuffix    = ".task.md"
	requestSuffix = ".request.md"
	reasonSuffix  = ".reason"
)

// Rejection reasons: fixed texts that never contain file content.
const (
	ReasonNotRegular    = "not a regular file"
	ReasonUnknownSuffix = "unknown suffix: want .task.md or .request.md"
	ReasonTooLarge      = "larger than 64 KiB"
	ReasonBadEncoding   = "not valid UTF-8"
	ReasonNoTitle       = "first line is not a '# ' title line"
	ReasonEmptyTitle    = "title is empty"
)

// Rejection is an inbox entry moved to inbox/rejected/: Name is its inbox
// name, RejectedName its name under inbox/rejected/ and Reason one of the
// Reason texts.
type Rejection struct {
	Name, RejectedName, Reason string
}

// inboxKind returns the task kind a task file name's suffix stands for.
func inboxKind(name string) (taskKind, bool) {
	switch {
	case strings.HasSuffix(name, taskSuffix) && len(name) > len(taskSuffix):
		return kindTask, true
	case strings.HasSuffix(name, requestSuffix) && len(name) > len(requestSuffix):
		return kindRequest, true
	}
	return "", false
}

// taskFile is a parsed inbox task file.
type taskFile struct {
	kind        taskKind
	title, body string
	digest      string
}

// readTaskFile reads and parses inbox/name; reason is non-empty for an
// invalid file, err for an I/O failure.
func (f *Forge) readTaskFile(name string, info os.FileInfo) (tf taskFile, reason string, err error) {
	if !info.Mode().IsRegular() {
		return taskFile{}, ReasonNotRegular, nil
	}
	kind, ok := inboxKind(name)
	if !ok {
		return taskFile{}, ReasonUnknownSuffix, nil
	}
	file, err := os.Open(f.path(inboxDir, name))
	if err != nil {
		return taskFile{}, "", err
	}
	defer func() { _ = file.Close() }() // read-only file; a close error loses nothing
	opened, err := file.Stat()
	if err != nil {
		return taskFile{}, "", err
	}
	if !os.SameFile(info, opened) {
		return taskFile{}, "", fmt.Errorf("inbox file %s changed while opening", name)
	}
	b, err := io.ReadAll(io.LimitReader(file, MaxTaskFileBytes+1))
	if err != nil {
		return taskFile{}, "", err
	}
	if len(b) > MaxTaskFileBytes {
		return taskFile{}, ReasonTooLarge, nil
	}
	if !utf8.Valid(b) {
		return taskFile{}, ReasonBadEncoding, nil
	}
	sum := sha256.Sum256(b)
	text := string(b)
	first, rest, _ := strings.Cut(text, "\n")
	first = strings.TrimSuffix(first, "\r")
	title, ok := strings.CutPrefix(first, "# ")
	if !ok {
		return taskFile{}, ReasonNoTitle, nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return taskFile{}, ReasonEmptyTitle, nil
	}
	return taskFile{kind: kind, title: title, body: trimLeadingBlankLines(rest), digest: hex.EncodeToString(sum[:])}, "", nil
}

func trimLeadingBlankLines(s string) string {
	for s != "" {
		line, rest, found := strings.Cut(s, "\n")
		if strings.TrimSpace(line) != "" {
			break
		}
		if !found {
			return ""
		}
		s = rest
	}
	return s
}

// Ingest turns the files in inbox/ into task records, in lexical name
// order, and moves invalid entries to inbox/rejected/ (agentDefaults 2, 4,
// 5). The returned rejections are for the daemon to notify.
func (f *Forge) Ingest(ctx context.Context, mode ports.WriteMode) ([]Rejection, error) {
	live, err := f.guard(mode, "Ingest")
	if err != nil || !live {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	records, err := f.loadTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	seen := ingestedSet(records)
	entries, err := os.ReadDir(f.path(inboxDir))
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	var rejections []Rejection
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return rejections, err
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == rejectedDir {
			continue
		}
		info, err := os.Lstat(f.path(inboxDir, name))
		if err != nil {
			return rejections, fmt.Errorf("ingest %s: %w", name, err)
		}
		tf, reason, err := f.readTaskFile(name, info)
		if err != nil {
			return rejections, fmt.Errorf("ingest %s: %w", name, err)
		}
		if reason != "" {
			rej, err := f.reject(name, reason)
			if err != nil {
				return rejections, fmt.Errorf("ingest %s: %w", name, err)
			}
			rejections = append(rejections, rej)
			continue
		}
		if seen[ingestedKey(name, tf.digest)] {
			if err := f.removeInbox(name, tf.digest); err != nil {
				return rejections, fmt.Errorf("ingest %s: %w", name, err)
			}
			continue
		}
		if _, err := f.createRecord(taskRecord{
			Kind:       string(tf.kind),
			Title:      tf.title,
			Body:       tf.body,
			State:      string(stateOpen),
			Labels:     []string{},
			CreatedBy:  string(actorLocalOperator),
			SourceFile: name,
			Digest:     tf.digest,
			Comments:   []comment{},
		}); err != nil {
			return rejections, fmt.Errorf("ingest %s: %w", name, err)
		}
		seen[ingestedKey(name, tf.digest)] = true
		if err := os.Remove(f.path(inboxDir, name)); err != nil {
			return rejections, fmt.Errorf("ingest %s: remove: %w", name, err)
		}
		if err := syncDir(f.path(inboxDir)); err != nil {
			return rejections, fmt.Errorf("ingest %s: %w", name, err)
		}
	}
	return rejections, nil
}

func ingestedKey(name, digest string) string { return name + "\x00" + digest }

func ingestedSet(records []taskRecord) map[string]bool {
	seen := map[string]bool{}
	for _, r := range records {
		if r.SourceFile != "" {
			seen[ingestedKey(r.SourceFile, r.Digest)] = true
		}
	}
	return seen
}

// dropIngested is the crash-safety rule run by Open: an inbox file whose
// name and digest already have a record is deleted.
func (f *Forge) dropIngested(ctx context.Context, records []taskRecord) error {
	seen := ingestedSet(records)
	entries, err := os.ReadDir(f.path(inboxDir))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == rejectedDir {
			continue
		}
		info, err := os.Lstat(f.path(inboxDir, name))
		if err != nil {
			return err
		}
		tf, reason, err := f.readTaskFile(name, info)
		if err != nil {
			return err
		}
		if reason == "" && seen[ingestedKey(name, tf.digest)] {
			if err := f.removeInbox(name, tf.digest); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeInbox deletes an already ingested inbox file, logging only its
// name and digest.
func (f *Forge) removeInbox(name, digest string) error {
	if err := os.Remove(f.path(inboxDir, name)); err != nil {
		return fmt.Errorf("remove ingested %s: %w", name, err)
	}
	if err := syncDir(f.path(inboxDir)); err != nil {
		return err
	}
	f.cfg.Logger.Info("forge-local removed already ingested inbox file", "name", name, "digest", digest)
	return nil
}

// reject moves inbox/name to inbox/rejected/ under the first free name and
// writes its reason file; nothing is overwritten (agentDefault 4).
func (f *Forge) reject(name, reason string) (Rejection, error) {
	dir := f.path(inboxDir, rejectedDir)
	target := ""
	for k := 0; ; k++ {
		cand := name
		if k > 0 {
			cand = name + "." + strconv.Itoa(k)
		}
		free, err := freeName(dir, cand)
		if err != nil {
			return Rejection{}, err
		}
		if free {
			target = cand
			break
		}
	}
	if err := os.Rename(f.path(inboxDir, name), filepath.Join(dir, target)); err != nil {
		return Rejection{}, fmt.Errorf("move to rejected: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return Rejection{}, err
	}
	if err := syncDir(f.path(inboxDir)); err != nil {
		return Rejection{}, err
	}
	if err := writeNew(dir, target+reasonSuffix, []byte(reason+"\n")); err != nil {
		return Rejection{}, fmt.Errorf("write reason: %w", err)
	}
	return Rejection{Name: name, RejectedName: target, Reason: reason}, nil
}

// freeName reports whether neither dir/cand nor its reason file exists.
func freeName(dir, cand string) (bool, error) {
	for _, n := range []string{cand, cand + reasonSuffix} {
		_, err := os.Lstat(filepath.Join(dir, n))
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return true, nil
}
