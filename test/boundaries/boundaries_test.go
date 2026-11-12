// Package boundaries enforces the module import rules of spec §4.4 and plan
// Task 2 Step 4 with go list over the four verify targets.
package boundaries

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const mainModulePath = "github.com/syllik/deep-dark-factory"

var targets = []struct{ goos, goarch string }{
	{"linux", "arm64"},
	{"linux", "amd64"},
	{"darwin", "arm64"},
	{"darwin", "amd64"},
}

// thirdPartyAllowlist is the only table of third-party imports allowed from
// internal/core/**. Each entry covers its subtree on both sides. A new or
// changed entry is a sensitive change (plan OD-16, decision log Issue #39).
var thirdPartyAllowlist = []struct{ pkg, module string }{
	{"internal/core/store", "modernc.org/sqlite"},
	{"internal/core/model", "github.com/santhosh-tekuri/jsonschema/v6"},
	{"internal/core/policy", "github.com/bmatcuk/doublestar/v4"},
}

type goListError struct{ Err string }

type listedPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *struct {
		Path string
		Main bool
	}
	Imports        []string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	Error          *goListError
	DepsErrors     []*goListError
}

// entry is one violation: rule ID, package path relative to the module and
// the offending import path (for core-build: empty package, file path).
type entry struct{ rule, pkg, imp string }

func (e entry) String() string { return fmt.Sprintf("%s %s -> %s", e.rule, e.pkg, e.imp) }

// under reports whether p is prefix or a slash-separated path below it.
func under(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

func TestUnder(t *testing.T) {
	tests := []struct {
		p, prefix string
		want      bool
	}{
		{"internal/core/workspace", "internal/core/workspace", true},
		{"internal/core/workspace/git", "internal/core/workspace", true},
		{"internal/core/workspacex", "internal/core/workspace", false},
		{"modernc.org/sqlite", "modernc.org/sqlite", true},
		{"modernc.org/sqlite/lib", "modernc.org/sqlite", true},
		{"modernc.org/sqlitex", "modernc.org/sqlite", false},
	}
	for _, tt := range tests {
		if got := under(tt.p, tt.prefix); got != tt.want {
			t.Errorf("under(%q, %q) = %v, want %v", tt.p, tt.prefix, got, tt.want)
		}
	}
}

func TestBoundaries_RealTree(t *testing.T) {
	root := filepath.Join("..", "..")
	touchSources(t, root)
	mainPath, runs := listAll(t, root)
	if mainPath != mainModulePath {
		t.Fatalf("main module path %q, want %q", mainPath, mainModulePath)
	}
	core := false
	for _, run := range runs {
		for _, p := range run {
			if p.Module != nil && p.Module.Main && under(rel(mainPath, p.ImportPath), "internal/core") {
				core = true
			}
		}
	}
	if !core {
		t.Fatal("go list listed no internal/core package")
	}
	compare(t, check(t, root, mainPath, runs), nil)
}

func TestBoundaries_Fixtures(t *testing.T) {
	touchSources(t, "testdata")
	const v = "example.com/boundaries/violations/"
	violations := []entry{
		{"core-layers", "internal/core/toadapter", v + "internal/adapters/forge/a"},
		{"core-layers", "internal/core/todaemon", v + "internal/daemon"},
		{"core-layers", "internal/core/tocmd", v + "cmd/tool"},
		{"core-layers", "internal/core/totest", v + "test/fakes"},
		{"test-tree", "internal/core/totest", v + "test/fakes"},
		{"core-net", "internal/core/netdirect", "net"},
		{"core-net", "internal/core/tlsdirect", "crypto/tls"},
		{"core-net", "internal/core/nethelper", "net/http"},
		{"core-net", "internal/core/netlinux", "net"},
		{"core-net", "internal/core/netdarwin", "net"},
		{"core-net", "internal/core/netarm64", "net"},
		{"core-net", "internal/core/netamd64", "net"},
		{"core-exec", "internal/core/workspacex", "os/exec"},
		{"sql", "internal/adapters/forge/a", "database/sql"},
		{"sql", "internal/core/model", "modernc.org/sqlite"},
		{"core-thirdparty", "internal/core/model", "modernc.org/sqlite"},
		{"adapter-adapter", "internal/adapters/notify/b", v + "internal/adapters/forge/a"},
		{"adapter-store", "internal/adapters/agent/c", v + "internal/core/store/schema"},
		{"test-tree", "internal/daemon", v + "test/fakes"},
		{"core-thirdparty", "internal/core/ids", "github.com/google/uuid"},
		{"core-thirdparty", "internal/core/model", "github.com/bmatcuk/doublestar/v4"},
		{"core-thirdparty", "internal/core/config", "go.yaml.in/yaml/v3"},
		{"core-build", "", "internal/core/tagged/tagnet.go"},
		{"core-build", "", "internal/core/withcgo/cgo.go"},
		{"core-build", "", "internal/core/tagonly/only.go"},
		{"core-build", "", "internal/core/win/win_windows.go"},
	}
	for _, tt := range []struct {
		module string
		want   []entry
	}{
		{"violations", violations},
		{"clean", nil},
	} {
		t.Run(tt.module, func(t *testing.T) {
			root := filepath.Join("testdata", tt.module)
			mainPath, runs := listAll(t, root)
			if want := "example.com/boundaries/" + tt.module; mainPath != want {
				t.Fatalf("main module path %q, want %q", mainPath, want)
			}
			compare(t, check(t, root, mainPath, runs), tt.want)
		})
	}
}

// touchSources reads every .go, go.mod and go.sum file under root so that
// go test's cache, which tracks only files this process opens, is invalidated
// when the tree or a fixture changes.
func touchSources(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if n := d.Name(); strings.HasSuffix(n, ".go") || n == "go.mod" || n == "go.sum" {
			_, err := os.ReadFile(path)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read sources under %s: %v", root, err)
	}
}

func goEnv(goos, goarch string) []string {
	return append(os.Environ(),
		"GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=-mod=readonly",
		"GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
}

// listAll checks the go toolchain, then runs go list once per target and
// returns the main module path and the packages of each run.
func listAll(t *testing.T, root string) (string, [][]*listedPackage) {
	t.Helper()
	ver := runGo(t, root, goEnv(runtime.GOOS, runtime.GOARCH), "env", "GOVERSION")
	if got := strings.TrimSpace(string(ver)); got != runtime.Version() {
		t.Fatalf("go env GOVERSION = %q, test built with %q; run this test via go test", got, runtime.Version())
	}
	mainPath := ""
	var runs [][]*listedPackage
	for _, tg := range targets {
		out := runGo(t, root, goEnv(tg.goos, tg.goarch), "list", "-deps", "-json", "./...")
		var run []*listedPackage
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			p := new(listedPackage)
			if err := dec.Decode(p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("decode go list output for %s/%s: %v", tg.goos, tg.goarch, err)
			}
			var msgs []string
			if p.Error != nil {
				msgs = append(msgs, p.Error.Err)
			}
			for _, e := range p.DepsErrors {
				msgs = append(msgs, e.Err)
			}
			if len(msgs) > 0 {
				msg := strings.Join(msgs, "\n")
				t.Fatalf("go list %s/%s: package %s: %s%s", tg.goos, tg.goarch, p.ImportPath, msg, downloadHint(msg))
			}
			if p.Module != nil && p.Module.Main {
				if mainPath != "" && mainPath != p.Module.Path {
					t.Fatalf("two main modules: %s and %s", mainPath, p.Module.Path)
				}
				mainPath = p.Module.Path
			}
			run = append(run, p)
		}
		runs = append(runs, run)
	}
	if mainPath == "" {
		t.Fatalf("go list under %s listed no main module package", root)
	}
	return mainPath, runs
}

func runGo(t *testing.T, dir string, env []string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s in %s: %v\nstderr:\n%s%s", strings.Join(args, " "), dir, err, stderr.String(), downloadHint(stderr.String()))
	}
	return out
}

func downloadHint(msg string) string {
	for _, s := range []string{"GOPROXY=off", "missing go.sum entry", "module lookup disabled", "cannot find module"} {
		if strings.Contains(msg, s) {
			return "\nA module is missing from the module cache; run `go mod download` and retry."
		}
	}
	return ""
}

func rel(mainPath, p string) string {
	if p == mainPath {
		return "."
	}
	return strings.TrimPrefix(p, mainPath+"/")
}

func coreNetForbidden(p string) bool {
	return under(p, "crypto/tls") || (under(p, "net") && p != "net/url" && p != "net/netip")
}

// check applies every rule to the union of the runs. _test.go files are
// exempt: their imports are not in Imports.
func check(t *testing.T, root, mainPath string, runs [][]*listedPackage) []entry {
	t.Helper()
	pkgs := map[string]*listedPackage{}
	imports := map[string]map[string]bool{}
	for _, run := range runs {
		for _, p := range run {
			pkgs[p.ImportPath] = p
			if p.Module != nil && p.Module.Main {
				if imports[p.ImportPath] == nil {
					imports[p.ImportPath] = map[string]bool{}
				}
				for _, q := range p.Imports {
					imports[p.ImportPath][q] = true
				}
			}
		}
	}
	isModule := func(q string) bool {
		p := pkgs[q]
		if p == nil {
			t.Fatalf("import %s not listed by go list", q)
		}
		return p.Module != nil && p.Module.Main
	}
	found := map[entry]bool{}
	for path, imps := range imports {
		r := rel(mainPath, path)
		for q := range imps {
			mod := isModule(q)
			std := pkgs[q].Standard
			rq := ""
			if mod {
				rq = rel(mainPath, q)
			}
			add := func(rule string) { found[entry{rule, r, q}] = true }
			if under(r, "internal/core") && mod && (under(rq, "internal/adapters") || under(rq, "internal/daemon") || under(rq, "cmd") || under(rq, "test")) {
				add("core-layers")
			}
			if under(r, "internal/core") && q == "os/exec" && !under(r, "internal/core/workspace") {
				add("core-exec")
			}
			if (q == "database/sql" || (!mod && !std && under(q, "modernc.org/sqlite"))) && !under(r, "internal/core/store") {
				add("sql")
			}
			if under(r, "internal/adapters") && mod && under(rq, "internal/adapters") && !under(rq, adapterRoot(r)) {
				add("adapter-adapter")
			}
			if under(r, "internal/adapters") && mod && under(rq, "internal/core/store") && rq != "internal/core/store" {
				add("adapter-store")
			}
			if !under(r, "test") && mod && under(rq, "test") {
				add("test-tree")
			}
			if under(r, "internal/core") && !mod && !std && !allowedThirdParty(r, q) {
				add("core-thirdparty")
			}
		}
	}
	for _, run := range runs {
		for _, e := range coreNet(mainPath, run) {
			found[e] = true
		}
	}
	for _, e := range coreBuild(t, root, runs) {
		found[e] = true
	}
	var out []entry
	for e := range found {
		out = append(out, e)
	}
	return out
}

// adapterRoot is internal/adapters/<port>/<name> for r, or r itself when r
// has fewer than two segments below internal/adapters.
func adapterRoot(r string) string {
	seg := strings.Split(r, "/")
	if len(seg) < 4 {
		return r
	}
	return strings.Join(seg[:4], "/")
}

func allowedThirdParty(r, q string) bool {
	for _, a := range thirdPartyAllowlist {
		if under(r, a.pkg) && under(q, a.module) {
			return true
		}
	}
	return false
}

// coreNet reports, for one run, each forbidden network package that a core
// package reaches through this module's packages only.
func coreNet(mainPath string, run []*listedPackage) []entry {
	byPath := map[string]*listedPackage{}
	for _, p := range run {
		if p.Module != nil && p.Module.Main {
			byPath[p.ImportPath] = p
		}
	}
	var out []entry
	for path := range byPath {
		r := rel(mainPath, path)
		if !under(r, "internal/core") {
			continue
		}
		seen := map[string]bool{path: true}
		stack := []string{path}
		for len(stack) > 0 {
			p := byPath[stack[len(stack)-1]]
			stack = stack[:len(stack)-1]
			for _, q := range p.Imports {
				if byPath[q] != nil {
					if !seen[q] {
						seen[q] = true
						stack = append(stack, q)
					}
				} else if coreNetForbidden(q) {
					out = append(out, entry{"core-net", r, q})
				}
			}
		}
	}
	return out
}

// coreBuild reports each non-test .go file under internal/core that no run
// compiles (other platforms, other build tags, cgo).
func coreBuild(t *testing.T, root string, runs [][]*listedPackage) []entry {
	t.Helper()
	resolve := func(p string) string {
		abs, err := filepath.Abs(p)
		if err == nil {
			abs, err = filepath.EvalSymlinks(abs)
		}
		if err != nil {
			t.Fatalf("resolve %s: %v", p, err)
		}
		return filepath.Clean(abs)
	}
	built := map[string]bool{}
	for _, run := range runs {
		for _, p := range run {
			if p.Module == nil || !p.Module.Main {
				continue
			}
			for _, f := range slices.Concat(p.GoFiles, p.CgoFiles) {
				built[resolve(filepath.Join(p.Dir, f))] = true
			}
		}
	}
	var out []entry
	err := filepath.WalkDir(filepath.Join(root, "internal", "core"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || built[resolve(path)] {
			return nil
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, entry{"core-build", "", filepath.ToSlash(r)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/core under %s: %v", root, err)
	}
	return out
}

func compare(t *testing.T, got, want []entry) {
	t.Helper()
	g := map[entry]bool{}
	for _, e := range got {
		g[e] = true
	}
	w := map[entry]bool{}
	for _, e := range want {
		w[e] = true
	}
	var missing, extra []string
	for e := range w {
		if !g[e] {
			missing = append(missing, e.String())
		}
	}
	for e := range g {
		if !w[e] {
			extra = append(extra, e.String())
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	if len(missing)+len(extra) > 0 {
		t.Errorf("boundary violations differ\nmissing (expected, not reported):\n  %s\nextra (reported, not expected):\n  %s",
			strings.Join(missing, "\n  "), strings.Join(extra, "\n  "))
	}
}
