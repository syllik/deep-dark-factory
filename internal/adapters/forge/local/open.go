package local

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Storage layout names (docs/adapters/forge-local.md#storage).
const (
	storageParent = "forge-local"
	formatFile    = "format"
	counterFile   = "counter"
	inboxDir      = "inbox"
	rejectedDir   = "rejected"
	tasksDir      = "tasks"
	answersDir    = "answers"
)

// Forge is a forge-local project opened on its storage directory.
type Forge struct {
	cfg  Config
	root string

	// mu serialises storage access within this instance; a second instance
	// is excluded by the daemon's instance lock.
	mu sync.Mutex
}

// Open validates cfg, checks the remote and opens (initialising when empty)
// the storage directory DataDir/forge-local/ProjectID. It removes stray
// adapter temp files, checks the counter and deletes inbox files that were
// already ingested.
func Open(ctx context.Context, cfg Config) (*Forge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	remote, err := checkRemote(cfg.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	parent := filepath.Join(cfg.DataDir, storageParent)
	root := filepath.Join(parent, cfg.ProjectID)
	if err := checkOverlap(root, remote); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	if err := requireDir(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("open forge-local: data dir: %w", err)
	}
	for _, d := range []string{parent, root} {
		if err := mkdirNew(d); err != nil {
			return nil, fmt.Errorf("open forge-local: %w", err)
		}
	}
	if err := checkFormat(root); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	for _, d := range []string{
		filepath.Join(root, inboxDir),
		filepath.Join(root, inboxDir, rejectedDir),
		filepath.Join(root, tasksDir),
		filepath.Join(root, answersDir),
	} {
		if err := mkdirNew(d); err != nil {
			return nil, fmt.Errorf("open forge-local: %w", err)
		}
	}
	f := &Forge{cfg: cfg, root: root}
	// agentDefault 8: only directories the daemon alone writes.
	for _, d := range []string{root, f.path(tasksDir), f.path(inboxDir, rejectedDir)} {
		if err := removeTemps(d); err != nil {
			return nil, fmt.Errorf("open forge-local: %w", err)
		}
	}
	counter, err := f.readCounter()
	if err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	records, err := f.loadTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	for _, r := range records {
		if r.Number > counter {
			return nil, fmt.Errorf("open forge-local: counter %d is lower than task record %d", counter, r.Number)
		}
	}
	if err := f.dropIngested(ctx, records); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	return f, nil
}

func (f *Forge) path(elem ...string) string {
	return filepath.Join(append([]string{f.root}, elem...)...)
}

// checkRemote returns the path of a file: URL naming an existing bare
// repository, else an error wrapping ErrUnsupportedRemote (agentDefault 1).
func checkRemote(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: unparsable url", ErrUnsupportedRemote)
	}
	if u.Scheme != "file" || u.Opaque != "" || u.Host != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", fmt.Errorf("%w: want a file: url with an empty host", ErrUnsupportedRemote)
	}
	p := u.Path
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", fmt.Errorf("%w: path is not absolute and clean", ErrUnsupportedRemote)
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not an existing directory", ErrUnsupportedRemote, p)
	}
	checks := []struct {
		name string
		dir  bool
	}{{"HEAD", false}, {"objects", true}, {"refs", true}}
	for _, c := range checks {
		info, err := os.Lstat(filepath.Join(p, c.name))
		ok := err == nil && ((c.dir && info.IsDir()) || (!c.dir && info.Mode().IsRegular()))
		if !ok {
			return "", fmt.Errorf("%w: %s is not a bare repository", ErrUnsupportedRemote, p)
		}
	}
	return p, nil
}

// checkOverlap refuses a storage dir equal to, inside or containing the
// remote, compared after resolving symlinks.
func checkOverlap(root, remote string) error {
	r, err := filepath.EvalSymlinks(remote)
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	s, err := resolveExisting(root)
	if err != nil {
		return fmt.Errorf("resolve storage dir: %w", err)
	}
	if within(s, r) || within(r, s) {
		return fmt.Errorf("storage dir %s overlaps the remote %s", root, remote)
	}
	return nil
}

// resolveExisting resolves symlinks on the deepest existing ancestor of p
// and appends the rest.
func resolveExisting(p string) (string, error) {
	var rest []string
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{resolved}, rest...)...), nil
}

// within reports whether p equals dir or lies inside it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

func requireDir(p string) error {
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", p)
	}
	return nil
}

// checkFormat verifies the format marker, writing it into a directory that
// is empty or holds only adapter temp files.
func checkFormat(root string) error {
	p := filepath.Join(root, formatFile)
	if _, err := os.Lstat(p); err == nil {
		b, err := readRegular(p, int64(len(formatMarker)+1))
		if err != nil || string(b) != formatMarker+"\n" {
			return fmt.Errorf("%w: %s does not hold %s", ErrFormat, p, formatMarker)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tempPrefix) || !e.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not empty and has no format file", ErrFormat, root)
		}
	}
	return writeAtomic(root, formatFile, []byte(formatMarker+"\n"))
}

// removeTemps removes stray adapter temp files from dir.
func removeTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	removed := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) && e.Type().IsRegular() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
			removed = true
		}
	}
	if removed {
		return syncDir(dir)
	}
	return nil
}

// readCounter returns the last allocated task number; a missing counter
// is 0.
func (f *Forge) readCounter() (uint64, error) {
	b, err := readRegular(f.path(counterFile), 32)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read counter: %w", err)
	}
	if !counterPattern.Match(b) {
		return 0, errors.New("read counter: not a canonical decimal")
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(string(b), "\n"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("read counter: %w", err)
	}
	return n, nil
}

// allocate advances the counter and returns the new task number; the
// counter is written before the record (agentDefault 5).
func (f *Forge) allocate() (uint64, error) {
	n, err := f.readCounter()
	if err != nil {
		return 0, err
	}
	n++
	if err := writeAtomic(f.root, counterFile, []byte(formatUint(n)+"\n")); err != nil {
		return 0, fmt.Errorf("write counter: %w", err)
	}
	return n, nil
}
