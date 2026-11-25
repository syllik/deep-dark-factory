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

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
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
	changesDir    = "changes"
	reviewsDir    = "reviews"
	checksDir     = "checks"
)

// Forge is a forge-local project opened on its storage directory.
type Forge struct {
	cfg  Config
	root string

	// mu serialises storage access within this instance, and guards
	// pending; a second instance is excluded by the daemon's instance lock.
	mu sync.Mutex
	// pending are the running check attempts this instance started, in
	// start order (Task 10b agentDefault 1). Guarded by mu.
	pending []pendingAttempt

	// runMu serialises RunChecks; it is taken before mu and never held by
	// mu's holders (Task 10b agentDefault 9).
	runMu sync.Mutex
}

// Open validates cfg, checks the remote and opens (initialising when empty)
// the storage directory DataDir/forge-local/ProjectID. It creates RunDir,
// makes or fetches the clean clone in CloneDir, removes stray adapter temp
// files, checks the counter, deletes inbox files that were already ingested
// and recovers check attempts a previous instance left running.
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
	if err := checkOverlap(root, remote, cfg.CloneDir, cfg.RunDir); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	for _, d := range []struct{ name, path string }{{"clone dir", cfg.CloneDir}, {"run dir", cfg.RunDir}} {
		if err := requireDir(filepath.Dir(d.path)); err != nil {
			return nil, fmt.Errorf("open forge-local: parent of %s: %w", d.name, err)
		}
	}
	if err := refFormatOK(ctx, cfg.Git, remote, cfg.IntegrationBranch); err != nil {
		return nil, fmt.Errorf("open forge-local: integration branch: %w", err)
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
		filepath.Join(root, changesDir),
		filepath.Join(root, reviewsDir),
		filepath.Join(root, checksDir),
	} {
		if err := mkdirNew(d); err != nil {
			return nil, fmt.Errorf("open forge-local: %w", err)
		}
	}
	if err := mkdirNew(cfg.RunDir); err != nil {
		return nil, fmt.Errorf("open forge-local: run dir: %w", err)
	}
	if err := openClone(ctx, cfg); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
	}
	f := &Forge{cfg: cfg, root: root}
	if err := f.removeAllTemps(); err != nil {
		return nil, fmt.Errorf("open forge-local: %w", err)
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
	if err := f.recoverAttempts(ctx); err != nil {
		return nil, fmt.Errorf("open forge-local: recover check attempts: %w", err)
	}
	return f, nil
}

// removeAllTemps removes stray adapter temp files from the directories the
// daemon alone writes: the root, tasks/, inbox/rejected/, changes/,
// checks/<sha>/ and reviews/<id>/<sha>/ (agentDefault 8 of Task 10a). It
// descends only into real directories; other entries are left for the
// record listings to refuse.
func (f *Forge) removeAllTemps() error {
	dirs := []string{f.root, f.path(tasksDir), f.path(inboxDir, rejectedDir), f.path(changesDir)}
	dirs = append(dirs, subdirs(f.path(checksDir))...)
	for _, d := range subdirs(f.path(reviewsDir)) {
		dirs = append(dirs, subdirs(d)...)
	}
	for _, d := range dirs {
		if err := removeTemps(d); err != nil {
			return err
		}
	}
	return nil
}

// subdirs returns the paths of the real directories in dir; an unreadable
// dir gives none.
func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// openClone makes the clean clone when CloneDir is absent; a present one
// must be a real directory holding a bare repository whose origin is
// RemoteURL, and is fetched (Task 10b agentDefault 5).
func openClone(ctx context.Context, cfg Config) error {
	info, err := os.Lstat(cfg.CloneDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := cfg.Git.CleanClone(ctx, cfg.RemoteURL, cfg.CloneDir); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("clone dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("clone dir %s is not a directory", cfg.CloneDir)
	}
	bare, err := cfg.Git.Run(ctx, cfg.CloneDir, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		return fmt.Errorf("clone dir %s: %w", cfg.CloneDir, err)
	}
	if strings.TrimSpace(string(bare)) != "true" {
		return fmt.Errorf("clone dir %s is not a bare repository", cfg.CloneDir)
	}
	origin, err := cfg.Git.Run(ctx, cfg.CloneDir, nil, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("clone dir %s: remote.origin.url: %w", cfg.CloneDir, err)
	}
	if strings.TrimSuffix(string(origin), "\n") != cfg.RemoteURL {
		return fmt.Errorf("clone dir %s: remote.origin.url is not the configured remote", cfg.CloneDir)
	}
	return cfg.Git.Fetch(ctx, cfg.CloneDir)
}

// refFormatOK refuses a branch for which `git check-ref-format
// refs/heads/<branch>` fails; dir is any existing absolute directory, such
// as the clean clone once Open has returned.
func refFormatOK(ctx context.Context, r *git.Runner, dir, branch string) error {
	if branch == "" {
		return errors.New("empty branch name")
	}
	if _, err := r.Run(ctx, dir, nil, "check-ref-format", "refs/heads/"+branch); err != nil {
		var ce *git.CommandError
		if errors.As(err, &ce) {
			return fmt.Errorf("invalid branch name %q", branch)
		}
		return fmt.Errorf("check branch name %q: %w", branch, err)
	}
	return nil
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

// checkOverlap refuses a storage dir, remote, clone dir and run dir of
// which any two are equal or nested, compared after resolving symlinks
// (Task 10b agentDefault 5).
func checkOverlap(root, remote, cloneDir, runDir string) error {
	r, err := filepath.EvalSymlinks(remote)
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	named := []struct{ name, path string }{{"storage dir", root}, {"remote", r}, {"clone dir", cloneDir}, {"run dir", runDir}}
	resolved := make([]string, len(named))
	resolved[1] = r
	for i, n := range named {
		if i == 1 {
			continue
		}
		p, err := resolveExisting(n.path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", n.name, err)
		}
		resolved[i] = p
	}
	for i := range named {
		for j := i + 1; j < len(named); j++ {
			if within(resolved[i], resolved[j]) || within(resolved[j], resolved[i]) {
				return fmt.Errorf("%s %s overlaps the %s %s", named[i].name, named[i].path, named[j].name, named[j].path)
			}
		}
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
