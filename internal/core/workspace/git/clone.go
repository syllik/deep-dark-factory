package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupportedURL is a clone source other than an absolute local path or a
// file:// URL.
var ErrUnsupportedURL = errors.New("unsupported repository url")

// ErrInvalidSHA is a commit that is not a full lowercase hex SHA.
var ErrInvalidSHA = errors.New("invalid commit sha")

// CleanClone makes the daemon's clean clone of url in dir: a bare repository
// with no hooks or template files and no checkout. url must be an absolute
// local path or a file:// URL; dir must be absolute and must not exist.
func (r *Runner) CleanClone(ctx context.Context, url, dir string) error {
	if !filepath.IsAbs(url) && !strings.HasPrefix(url, "file://") {
		return fmt.Errorf("clean clone of %q: %w", url, ErrUnsupportedURL)
	}
	if err := absent(dir); err != nil {
		return fmt.Errorf("clean clone: %w", err)
	}
	_, err := r.Run(ctx, filepath.Dir(dir), nil,
		"clone", "--bare", "--no-local", "--no-tags", "--template="+r.emptyDir(), "--", url, dir)
	if err != nil {
		return fmt.Errorf("clean clone: %w", err)
	}
	return nil
}

// Fetch refreshes the clean clone in dir so its branches equal the source's.
func (r *Runner) Fetch(ctx context.Context, dir string) error {
	_, err := r.Run(ctx, dir, nil,
		"fetch", "--prune", "--no-tags", "--no-write-fetch-head", "origin", "+refs/heads/*:refs/heads/*")
	if err != nil {
		return fmt.Errorf("fetch clean clone: %w", err)
	}
	return nil
}

// NewRunCopy makes the per-run copy of commit from the clean clone src in
// dst: its own repository with no remote, credential config, FETCH_HEAD or
// reflog, core.symlinks=false, and branch ddf-run checked out at commit. On
// any error dst is removed.
func (r *Runner) NewRunCopy(ctx context.Context, src, commit, dst string) (err error) {
	if !validSHA(commit) {
		return fmt.Errorf("run copy of %q: %w", commit, ErrInvalidSHA)
	}
	if !filepath.IsAbs(src) {
		return fmt.Errorf("run copy: source %q is not absolute", src)
	}
	if !filepath.IsAbs(dst) {
		return fmt.Errorf("run copy: %q is not absolute", dst)
	}
	// Mkdir fails on an existing dst, so the cleanup below never removes a
	// directory this call did not create.
	if err := os.Mkdir(dst, 0o700); err != nil {
		return fmt.Errorf("run copy: %w", err)
	}
	defer func() {
		if err != nil {
			if rmErr := os.RemoveAll(dst); rmErr != nil {
				err = errors.Join(err, fmt.Errorf("remove run copy: %w", rmErr))
			}
		}
	}()
	steps := [][]string{
		{"config", "core.symlinks", "false"},
		{"config", "core.logAllRefUpdates", "false"},
		{"-c", "core.logAllRefUpdates=false", "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", src, commit},
		{"-c", "core.logAllRefUpdates=false", "checkout", "-B", "ddf-run", commit},
	}
	if _, err := r.Run(ctx, filepath.Dir(dst), nil, "init", "--template="+r.emptyDir(), "--initial-branch=ddf-run", dst); err != nil {
		return fmt.Errorf("run copy: %w", err)
	}
	for _, args := range steps {
		if _, err := r.Run(ctx, dst, nil, args...); err != nil {
			return fmt.Errorf("run copy: %w", err)
		}
	}
	return nil
}

// absent returns an error unless dir is absolute and does not exist.
func absent(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%q is not absolute", dir)
	}
	_, err := os.Lstat(dir)
	if err == nil {
		return fmt.Errorf("%s: %w", dir, fs.ErrExist)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// validSHA accepts only a full lowercase hex SHA-1 or SHA-256, so a commit
// argument is never a ref or an option.
func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
