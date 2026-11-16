package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// stderrLimit bounds the stderr excerpt kept in a CommandError.
const stderrLimit = 2048

// Runner runs the git CLI with a fixed environment and fixed global options,
// so no system, global or inherited configuration, hook, fsmonitor or filter
// driver outside DDF's control takes effect (spec §8.3).
type Runner struct {
	gitPath  string
	stateDir string
}

// CommandError is a git run that exited non-zero.
type CommandError struct {
	Args     []string
	ExitCode int
	// Stderr is the first 2048 bytes of git's stderr.
	Stderr string
}

// Error names the git subcommand and the exit code.
func (e *CommandError) Error() string {
	return fmt.Sprintf("git %s: exit code %d", subcommand(e.Args), e.ExitCode)
}

// NewRunner prepares stateDir (with the empty hooks and template directory
// stateDir/empty and the home directory stateDir/home) and checks that the
// git at gitPath is at least MinVersion. Both paths must be absolute: the
// core does no PATH lookup and reads no environment.
func NewRunner(ctx context.Context, gitPath, stateDir string) (*Runner, error) {
	if !filepath.IsAbs(gitPath) {
		return nil, fmt.Errorf("git path %q is not absolute", gitPath)
	}
	if !filepath.IsAbs(stateDir) {
		return nil, fmt.Errorf("git state dir %q is not absolute", stateDir)
	}
	r := &Runner{gitPath: filepath.Clean(gitPath), stateDir: filepath.Clean(stateDir)}
	for _, d := range []string{r.stateDir, r.emptyDir(), r.homeDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create git state dir: %w", err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, fmt.Errorf("restrict git state dir: %w", err)
		}
	}
	out, err := r.Run(ctx, r.homeDir(), nil, "version")
	if err != nil {
		return nil, err
	}
	v, err := ParseVersion(string(out))
	if err != nil {
		return nil, fmt.Errorf("check git version: %w", err)
	}
	if v.Less(MinVersion()) {
		return nil, &VersionError{Found: v, Min: MinVersion()}
	}
	return r, nil
}

// Run runs git with args in dir (absolute) and returns its stdout. A nil
// stdin is empty. A non-zero exit is a *CommandError; a cancelled context
// gives an error wrapping ctx.Err().
func (r *Runner) Run(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, error) {
	sub := subcommand(args)
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("git %s: dir %q is not absolute", sub, dir)
	}
	if err := r.checkEmptyDir(); err != nil {
		return nil, fmt.Errorf("git %s: %w", sub, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("git %s: %w", sub, err)
	}
	cmd := exec.CommandContext(ctx, r.gitPath, append(r.globalOptions(), args...)...)
	cmd.Dir = dir
	cmd.Env = r.env(dir)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	stderr := &headWriter{max: stderrLimit}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("git %s: %w", sub, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, &CommandError{Args: slices.Clone(args), ExitCode: exitErr.ExitCode(), Stderr: string(stderr.buf)}
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", sub, err)
	}
	return stdout.Bytes(), nil
}

// env is the whole child environment; nothing is inherited from the daemon.
// GIT_CEILING_DIRECTORIES keeps discovery from reaching an enclosing
// repository.
func (r *Runner) env(dir string) []string {
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir),
		"HOME=" + r.homeDir(), "XDG_CONFIG_HOME=" + r.homeDir(), "LC_ALL=C", "PATH=/usr/bin:/bin"}
}

// globalOptions override any repository-local value: command-line config has
// the highest precedence (spec §8.3: no hooks, fsmonitor or filter drivers).
func (r *Runner) globalOptions() []string {
	return []string{"-c", "core.hooksPath=" + r.emptyDir(), "-c", "core.fsmonitor=false",
		"-c", "core.attributesFile=/dev/null", "-c", "core.symlinks=false",
		"-c", "core.protectHFS=true", "-c", "core.protectNTFS=true",
		"-c", "protocol.allow=never", "-c", "protocol.file.allow=always",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false"}
}

func (r *Runner) emptyDir() string { return filepath.Join(r.stateDir, "empty") }
func (r *Runner) homeDir() string  { return filepath.Join(r.stateDir, "home") }

// checkEmptyDir refuses to start git when the hooks and template directory
// holds anything, since git would run or copy it.
func (r *Runner) checkEmptyDir() error {
	entries, err := os.ReadDir(r.emptyDir())
	if err != nil {
		return fmt.Errorf("read empty hooks dir: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("empty hooks dir %s holds %q", r.emptyDir(), entries[0].Name())
	}
	return nil
}

// subcommand is the first argument that is neither an option nor the value
// of a -c or -C option.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-c" || a == "-C":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return "(no subcommand)"
}

// headWriter keeps the first max bytes written to it and drops the rest.
type headWriter struct {
	buf []byte
	max int
}

func (w *headWriter) Write(p []byte) (int, error) {
	if n := w.max - len(w.buf); n > 0 {
		w.buf = append(w.buf, p[:min(n, len(p))]...)
	}
	return len(p), nil
}
