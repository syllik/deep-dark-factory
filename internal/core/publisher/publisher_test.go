package publisher_test

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

var pubKey = model.TaskKey(strings.Repeat("b", 64))

// loadedValue is the obviously fake secret loaded into the run's container.
const loadedValue = "test-secret-loaded-7f3a"

// ghToken is a pattern-shaped token built at run time so no source file
// matches a secret pattern.
func ghToken() string { return "gh" + "p_" + strings.Repeat("Z9", 18) }

// tokenName is a base file whose name holds a pattern-shaped token.
func tokenName() string { return "cfg/" + "gh" + "p_" + strings.Repeat("W5", 18) + ".txt" }

// pubEnv is a clean clone whose base commit holds baseFiles, with a fake
// sandbox host and a Publisher on them.
type pubEnv struct {
	r     *git.Runner
	clone string
	base  string
	files fstest.MapFS
	host  *fake.Host
	pub   *publisher.Publisher
}

func newPubEnv(t *testing.T) *pubEnv {
	t.Helper()
	r := newRunner(t)
	src, first := newSourceRepo(t, r)
	files := map[string]string{
		"src/a.go":      "package a\n",
		"deploy/x.yaml": "x: 1\n",
		"cfg/token.txt": "token = " + ghToken() + "\nline = 1\n",
		tokenName():     "named\n",
	}
	base := commitOn(t, r, src, first, files)
	files["README"] = "base\n"
	mfs := fstest.MapFS{}
	for name, content := range files {
		mfs[name] = &fstest.MapFile{Data: []byte(content)}
	}
	host := fake.NewHost(ports.Capacity{CPUMillis: 4000, MemoryBytes: 1 << 32, DiskBytes: 1 << 34})
	clone := cleanClone(t, r, src)
	return &pubEnv{r: r, clone: clone, base: base, files: mfs, host: host, pub: &publisher.Publisher{
		Git: r, Clone: clone, Scratch: t.TempDir(), Sandbox: host.Sandbox("i1"),
		Clock: fakes.NewClock(time.Unix(1_800_000_000, 0)), IDs: fakes.NewIDs("id")}}
}

// input returns a valid first-revision Input whose patch is edits on the
// base files.
func (e *pubEnv) input(t *testing.T, edits ...fakes.FileEdit) publisher.Input {
	t.Helper()
	p, err := fakes.BuildPatch(e.files, edits)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}
	return publisher.Input{Key: pubKey, RunID: "run-1", TaskRef: "#7", Title: "Add feature", Branch: "ddf/task-7",
		BaseBranch: "main", Base: e.base, ChangedFrom: e.base, Patch: p,
		Profile:  policy.Profile{Autonomy: policy.AutonomyPropose, LowMaxLines: 200},
		Template: changeTemplate(t), Verify: []string{"go vet ./...", "go test ./..."},
		Limits:   ports.CellLimits{CPUMillis: 1000, MemoryBytes: 1 << 30, DiskBytes: 1 << 30},
		Identity: git.Identity{Name: "DDF Test", Email: "ddf@example.invalid"},
		Loaded:   []ports.Secret{ports.NewSecret([]byte(loadedValue))}}
}

// commits counts the commit objects in the clone.
func (e *pubEnv) commits(t *testing.T) int {
	t.Helper()
	out := gitOut(t, e.r, e.clone, nil, "cat-file", "--batch-all-objects", "--batch-check=%(objecttype)")
	return strings.Count(out, "commit")
}

func (e *pubEnv) blobOf(t *testing.T, content string) string {
	t.Helper()
	return gitOut(t, e.r, e.clone, []byte(content), "hash-object", "--stdin")
}

func edit(path, content string) fakes.FileEdit {
	return fakes.FileEdit{Path: path, Content: []byte(content)}
}

// prepare runs Prepare and checks the error contract: an error comes with
// a nil Outcome.
func prepare(t *testing.T, e *pubEnv, in publisher.Input) publisher.Outcome {
	t.Helper()
	out, err := e.pub.Prepare(t.Context(), in)
	if err != nil {
		t.Fatalf("Prepare: %v (outcome %+v)", err, out)
	}
	return out
}

func asType[T publisher.Outcome](t *testing.T, out publisher.Outcome) T {
	t.Helper()
	v, ok := out.(T)
	if !ok {
		var zero T
		t.Fatalf("outcome %T %+v, want %T", out, out, zero)
	}
	return v
}

// noSideEffects checks that no cell was created and no commit written.
func noSideEffects(t *testing.T, e *pubEnv, commitsBefore int) {
	t.Helper()
	if st := e.host.Stats(); st.Created != 0 {
		t.Errorf("sandbox stats %+v, want no cell", st)
	}
	if n := e.commits(t); n != commitsBefore {
		t.Errorf("%d commits, want %d: a commit was written", n, commitsBefore)
	}
}

func TestPrepare_HappyPathFirstRevision(t *testing.T) {
	e := newPubEnv(t)
	in := e.input(t, edit("src/a.go", "package a\n\nfunc A() {}\n"))
	ready := asType[publisher.Ready](t, prepare(t, e, in))
	obj := gitOut(t, e.r, e.clone, nil, "cat-file", "-p", ready.Commit)
	if !strings.HasPrefix(obj, "tree "+ready.Tree+"\nparent "+e.base+"\nauthor ") {
		t.Errorf("commit object:\n%s\nwant tree %s and exactly one parent %s", obj, ready.Tree, e.base)
	}
	runs := e.host.Runs()
	if len(runs) != len(in.Verify) {
		t.Fatalf("%d runs, want %d", len(runs), len(in.Verify))
	}
	for i, run := range runs {
		if want := []string{"sh", "-c", in.Verify[i]}; !reflect.DeepEqual(run.Argv, want) {
			t.Errorf("run %d argv %q, want %q", i, run.Argv, want)
		}
	}
	if st := e.host.Stats(); st != (fake.Stats{Created: 1, Killed: 1}) {
		t.Errorf("stats %+v, want one cell created and killed", st)
	}
	if want := (publisher.PushRequest{Clone: e.clone, Branch: in.Branch, NewSHA: ready.Commit}); ready.Push != want {
		t.Errorf("push %+v, want %+v", ready.Push, want)
	}
	if ready.Change.HeadSHA != ready.Commit || ready.Change.Branch != in.Branch || ready.Change.BaseBranch != "main" {
		t.Errorf("change %+v does not match commit %s", ready.Change, ready.Commit)
	}
	if !strings.HasPrefix(ready.Change.Body, "Task: Closes #7") {
		t.Errorf("body starts %q, want 'Task: Closes #7'", ready.Change.Body[:min(40, len(ready.Change.Body))])
	}
	if !strings.Contains(ready.Change.Body, "`go test ./...`: passed (local gate)") {
		t.Errorf("body lacks passed verify line:\n%s", ready.Change.Body)
	}
	if got := gitOut(t, e.r, e.clone, nil, "log", "-1", "--format=%B", ready.Commit); got != strings.TrimSpace(ready.SquashMessage) {
		t.Errorf("commit message %q, want squash message %q", got, ready.SquashMessage)
	}
	rec := &recorder{}
	if _, err := publisher.Publish(t.Context(), ports.WriteModeDryRun, rec, rec, ready); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if want := []string{"push:" + string(ports.WriteModeDryRun), "change:" + string(ports.WriteModeDryRun)}; !reflect.DeepEqual(rec.calls, want) {
		t.Errorf("publish calls %q, want %q", rec.calls, want)
	}
}

func TestPrepare_CorrectionRevision(t *testing.T) {
	t.Run("branch head equals base: push from the head", func(t *testing.T) {
		e := newPubEnv(t)
		in := e.input(t, edit("src/a.go", "package a\n\nvar B = 1\n"))
		in.BranchHead = e.base
		ready := asType[publisher.Ready](t, prepare(t, e, in))
		if ready.Push.OldSHA != e.base {
			t.Errorf("push old sha %q, want %q", ready.Push.OldSHA, e.base)
		}
	})
	t.Run("branch head differs from base: error, no cell, no commit", func(t *testing.T) {
		e := newPubEnv(t)
		before := e.commits(t)
		in := e.input(t, edit("src/a.go", "package a\n\nvar B = 1\n"))
		in.BranchHead = strings.Repeat("c", 40)
		if out, err := e.pub.Prepare(t.Context(), in); err == nil || out != nil {
			t.Fatalf("Prepare = %+v, %v; want nil outcome and an error", out, err)
		}
		noSideEffects(t, e, before)
	})
}

func TestPrepare_Deterministic(t *testing.T) {
	e := newPubEnv(t)
	in := e.input(t, edit("src/a.go", "package a\n\nvar C = 3\n"))
	first := asType[publisher.Ready](t, prepare(t, e, in))
	second := asType[publisher.Ready](t, prepare(t, e, in))
	if first.Commit != second.Commit {
		t.Errorf("commits %s and %s differ for the same input and clock", first.Commit, second.Commit)
	}
}

func TestPrepare_ApplyRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit fakes.FileEdit
		want publisher.Failed
	}{
		{"node_modules addition", edit("web/node_modules/x/index.js", "x\n"),
			publisher.Failed{Reason: publisher.FailNodeModules, Paths: []string{"web/node_modules/x/index.js"}}},
		{"path under .git", edit(".git/hooks/pre-commit", "x\n"),
			publisher.Failed{Reason: publisher.FailGitDir, Paths: []string{".git/hooks/pre-commit"}}},
		{"refused path holds the loaded value: redacted", edit("web/node_modules/"+loadedValue+"/i.js", "x\n"),
			publisher.Failed{Reason: publisher.FailNodeModules, Paths: []string{"web/node_modules/[redacted]/i.js"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			before := e.commits(t)
			got := asType[publisher.Failed](t, prepare(t, e, e.input(t, tc.edit)))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("failed %+v, want %+v", got, tc.want)
			}
			noSideEffects(t, e, before)
		})
	}
}

func TestPrepare_Secrets(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     string
		secret   string
		patterns []string
		loaded   bool
	}{
		{"file holds the loaded value", "src/conf.go", loadedValue, nil, true},
		{"pattern-shaped token added", "src/tok.txt", "gh" + "p_" + strings.Repeat("Y8", 18), []string{"github-token"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			before := e.commits(t)
			in := e.input(t, edit(tc.path, "package a\n\nconst k = \""+tc.secret+"\"\n"))
			in.Title = "title " + loadedValue
			got := asType[publisher.NeedsOwner](t, prepare(t, e, in))
			if got.Stop != publisher.StopSecrets || !got.HitStop || !got.FailedRun || got.Category != "secrets and credentials" {
				t.Errorf("needs owner %+v, want secrets stop, hit stop, failed run", got)
			}
			if !reflect.DeepEqual(got.SecretPatterns, tc.patterns) || !reflect.DeepEqual(got.Paths, []string{tc.path}) {
				t.Errorf("patterns %q paths %q, want %q and [%s]", got.SecretPatterns, got.Paths, tc.patterns, tc.path)
			}
			if all := fmt.Sprintf("%#v", got); strings.Contains(all, loadedValue) || strings.Contains(all, tc.secret) {
				t.Errorf("outcome leaks the secret: %s", all)
			}
			if !strings.Contains(got.Question, tc.path) {
				t.Errorf("question %q does not name the path", got.Question)
			}
			for _, p := range tc.patterns {
				if !strings.Contains(got.Question, p) {
					t.Errorf("question %q does not name pattern %s", got.Question, p)
				}
			}
			if tc.loaded != strings.Contains(got.Question, "a loaded secret value") {
				t.Errorf("question %q: loaded value mention want %v", got.Question, tc.loaded)
			}
			noSideEffects(t, e, before)
		})
	}
	t.Run("token already in the base file and unchanged: not refused", func(t *testing.T) {
		e := newPubEnv(t)
		in := e.input(t, edit("cfg/token.txt", "token = "+ghToken()+"\nline = 2\n"))
		asType[publisher.Ready](t, prepare(t, e, in))
	})
}

func TestPrepare_SecretsInPathNames(t *testing.T) {
	tok := "gh" + "p_" + strings.Repeat("X7", 18)
	for _, tc := range []struct {
		name     string
		path     string
		secret   string
		patterns []string
		want     string
	}{
		{"added file name holds the loaded value", "src/" + loadedValue + ".txt", loadedValue, nil, "src/[redacted].txt"},
		{"added file name holds a pattern-shaped token", "notes/" + tok + ".txt", tok, []string{"github-token"}, "notes/[redacted].txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			before := e.commits(t)
			got := asType[publisher.NeedsOwner](t, prepare(t, e, e.input(t, edit(tc.path, "harmless\n"))))
			if got.Stop != publisher.StopSecrets || !got.HitStop || !got.FailedRun {
				t.Errorf("needs owner %+v, want secrets stop, hit stop, failed run", got)
			}
			if !reflect.DeepEqual(got.SecretPatterns, tc.patterns) || !reflect.DeepEqual(got.Paths, []string{tc.want}) {
				t.Errorf("patterns %q paths %q, want %q and [%s]", got.SecretPatterns, got.Paths, tc.patterns, tc.want)
			}
			if all := fmt.Sprintf("%#v", got); strings.Contains(all, tc.secret) {
				t.Errorf("outcome leaks the secret: %s", all)
			}
			noSideEffects(t, e, before)
		})
	}
	t.Run("modified file whose base name holds a token: not refused", func(t *testing.T) {
		e := newPubEnv(t)
		asType[publisher.Ready](t, prepare(t, e, e.input(t, edit(tokenName(), "changed\n"))))
	})
}

// symlinkPatch adds the symlink link pointing at README.
const symlinkPatch = "diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1 @@\n+README\n\\ No newline at end of file\n"

func TestPrepare_PathPolicy(t *testing.T) {
	for _, path := range []string{".github/workflows/x.yml", "pkg/.claude/settings.json", "a/b/.opencode/plugin.js"} {
		t.Run(path, func(t *testing.T) {
			e := newPubEnv(t)
			before := e.commits(t)
			got := asType[publisher.NeedsOwner](t, prepare(t, e, e.input(t, edit(path, "x\n"))))
			if got.Stop != publisher.StopPathPolicy || !got.HitStop || got.FailedRun || got.Category != "forbidden path" {
				t.Errorf("needs owner %+v, want path-policy stop, hit stop, no failed run", got)
			}
			if !reflect.DeepEqual(got.Forbidden, []string{path}) || !strings.Contains(got.Question, path) {
				t.Errorf("forbidden %q question %q, want %s listed", got.Forbidden, got.Question, path)
			}
			noSideEffects(t, e, before)
		})
	}
	t.Run("mention in a forbidden path: sanitized in the question only", func(t *testing.T) {
		e := newPubEnv(t)
		path := "@org/.claude/x.json"
		got := asType[publisher.NeedsOwner](t, prepare(t, e, e.input(t, edit(path, "x\n"))))
		if strings.Contains(got.Question, "@org") || !strings.Contains(got.Question, "org/.claude/x.json") {
			t.Errorf("question %q: want the path without the mention", got.Question)
		}
		if !reflect.DeepEqual(got.Forbidden, []string{path}) {
			t.Errorf("forbidden %q, want [%s]", got.Forbidden, path)
		}
	})
	t.Run("added symlink", func(t *testing.T) {
		e := newPubEnv(t)
		before := e.commits(t)
		in := e.input(t)
		in.Patch = []byte(symlinkPatch)
		got := asType[publisher.NeedsOwner](t, prepare(t, e, in))
		if got.Stop != publisher.StopPathPolicy || !reflect.DeepEqual(got.Symlinks, []string{"link"}) || !got.HitStop {
			t.Errorf("needs owner %+v, want path-policy stop with symlink link", got)
		}
		noSideEffects(t, e, before)
	})
}

func TestPrepare_HardStops(t *testing.T) {
	e := newPubEnv(t)
	prod := "replicas: 2\n"
	in := e.input(t, edit("deploy/prod.yaml", prod), fakes.FileEdit{Path: "deploy/x.yaml", Delete: true})
	entries := []policy.HardStopEntry{
		{Path: "deploy/prod.yaml", Mode: policy.ModeRegular, BlobID: e.blobOf(t, prod)},
		{Path: "deploy/x.yaml", Deleted: true},
	}
	t.Run("unapproved entries: hard stop with mode, blob and deleted", func(t *testing.T) {
		before := e.commits(t)
		got := asType[publisher.NeedsOwner](t, prepare(t, e, in))
		if got.Stop != publisher.StopHardStop || !got.HitStop || got.FailedRun || got.Category != "deploy and infrastructure" {
			t.Errorf("needs owner %+v, want hard-stop stop", got)
		}
		if !reflect.DeepEqual(got.HardStops, entries) {
			t.Errorf("hard stops %+v, want %+v", got.HardStops, entries)
		}
		for _, want := range []string{"deploy/prod.yaml", "100644", entries[0].BlobID, "deploy/x.yaml\" (deleted)"} {
			if !strings.Contains(got.Question, want) {
				t.Errorf("question %q lacks %q", got.Question, want)
			}
		}
		noSideEffects(t, e, before)
	})
	t.Run("approved exactly: Ready, sensitive, hard stops set", func(t *testing.T) {
		ok := in
		ok.Approved = entries
		ready := asType[publisher.Ready](t, prepare(t, e, ok))
		if ready.Class != model.RiskClassSensitive || !reflect.DeepEqual(ready.HardStops, entries) {
			t.Errorf("ready class %s hard stops %+v, want sensitive and %+v", ready.Class, ready.HardStops, entries)
		}
	})
	t.Run("approved entry with another blob: hard stop again", func(t *testing.T) {
		other := in
		other.Approved = []policy.HardStopEntry{{Path: "deploy/prod.yaml", Mode: policy.ModeRegular, BlobID: e.blobOf(t, "other\n")}, entries[1]}
		got := asType[publisher.NeedsOwner](t, prepare(t, e, other))
		if got.Stop != publisher.StopHardStop || !reflect.DeepEqual(got.HardStops, entries[:1]) {
			t.Errorf("needs owner %+v, want hard stop for %+v", got, entries[0])
		}
	})
}

func TestPrepare_Precedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edits  []fakes.FileEdit
		script []fake.RunStep
		want   publisher.Stop
	}{
		{"secret in a forbidden path: secrets", []fakes.FileEdit{edit(".github/workflows/x.yml", "k: "+loadedValue+"\n")}, nil, publisher.StopSecrets},
		{"forbidden path and hard stop: path policy", []fakes.FileEdit{edit(".github/x.yml", "x\n"), edit("deploy/prod.yaml", "x\n")}, nil, publisher.StopPathPolicy},
		{"hard stop and failing gate: hard stop", []fakes.FileEdit{edit("deploy/prod.yaml", "x\n")},
			[]fake.RunStep{{Outcome: ports.CellRunOutcome{ExitCode: 1}}}, publisher.StopHardStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			e.host.ScriptRun(pubKey, tc.script...)
			before := e.commits(t)
			got := asType[publisher.NeedsOwner](t, prepare(t, e, e.input(t, tc.edits...)))
			if got.Stop != tc.want {
				t.Errorf("stop %s, want %s", got.Stop, tc.want)
			}
			noSideEffects(t, e, before)
		})
	}
}

func TestPrepare_Gate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script []fake.RunStep
		want   publisher.GateFailed
	}{
		{"second command exits 1", []fake.RunStep{{}, {Outcome: ports.CellRunOutcome{ExitCode: 1}}},
			publisher.GateFailed{Command: "go test ./...", Index: 1, Outcome: ports.CellRunOutcome{ExitCode: 1}}},
		{"first command times out", []fake.RunStep{{Outcome: ports.CellRunOutcome{TimedOut: true}}},
			publisher.GateFailed{Command: "go vet ./...", Index: 0, Outcome: ports.CellRunOutcome{TimedOut: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			e.host.ScriptRun(pubKey, tc.script...)
			got := asType[publisher.GateFailed](t, prepare(t, e, e.input(t, edit("src/a.go", "package a\n\nvar D = 4\n"))))
			if got != tc.want {
				t.Errorf("gate failed %+v, want %+v", got, tc.want)
			}
		})
	}
	t.Run("sandbox error: error and nil outcome", func(t *testing.T) {
		e := newPubEnv(t)
		lost := errors.New("sandbox lost")
		e.host.ScriptRun(pubKey, fake.RunStep{Err: lost})
		out, err := e.pub.Prepare(t.Context(), e.input(t, edit("src/a.go", "package a\n\nvar D = 4\n")))
		if !errors.Is(err, lost) || out != nil {
			t.Fatalf("Prepare = %+v, %v; want nil outcome and %v", out, err, lost)
		}
	})
}

func TestPrepare_Class(t *testing.T) {
	for _, tc := range []struct {
		name    string
		planner model.RiskClass
		want    model.RiskClass
	}{
		{"small code change: low", "", model.RiskClassLow},
		{"planner risk sensitive raises: sensitive", model.RiskClassSensitive, model.RiskClassSensitive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			in := e.input(t, edit("src/a.go", "package a\n\nvar E = 5\n"))
			in.PlannerRisk = tc.planner
			if got := asType[publisher.Ready](t, prepare(t, e, in)).Class; got != tc.want {
				t.Errorf("class %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPrepare_Redaction(t *testing.T) {
	e := newPubEnv(t)
	in := e.input(t, edit("src/a.go", "package a\n\nvar F = 6\n"))
	in.Title = "Add " + loadedValue
	in.AgentNotes = "used " + loadedValue + "\nFixes #2"
	ready := asType[publisher.Ready](t, prepare(t, e, in))
	t.Run("loaded value only as [redacted]", func(t *testing.T) {
		for name, s := range map[string]string{"title": ready.Change.Title, "body": ready.Change.Body, "squash message": ready.SquashMessage} {
			if strings.Contains(s, loadedValue) || !strings.Contains(s, "[redacted]") {
				t.Errorf("%s %q: want the loaded value only as [redacted]", name, s)
			}
		}
	})
	t.Run("agent closing keyword stripped: exactly one in the body", func(t *testing.T) {
		kw := regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b`)
		if n := len(kw.FindAllString(ready.Change.Body, -1)); n != 1 {
			t.Errorf("%d closing keywords in body, want 1:\n%s", n, ready.Change.Body)
		}
	})
}

func TestPrepare_InvalidInput(t *testing.T) {
	e := newPubEnv(t)
	for _, tc := range []struct {
		name string
		in   func(in *publisher.Input)
		pub  func(p *publisher.Publisher)
	}{
		{name: "invalid key", in: func(in *publisher.Input) { in.Key = "ABC" }},
		{name: "empty run id", in: func(in *publisher.Input) { in.RunID = "" }},
		{name: "empty branch", in: func(in *publisher.Input) { in.Branch = "" }},
		{name: "branch with ..", in: func(in *publisher.Input) { in.Branch = "a..b" }},
		{name: "branch with leading -", in: func(in *publisher.Input) { in.Branch = "-f" }},
		{name: "branch with leading /", in: func(in *publisher.Input) { in.BaseBranch = "/main" }},
		{name: "branch with trailing /", in: func(in *publisher.Input) { in.Branch = "ddf/" }},
		{name: "branch ending .lock", in: func(in *publisher.Input) { in.Branch = "ddf.lock" }},
		{name: "branch with a space", in: func(in *publisher.Input) { in.Branch = "a b" }},
		{name: "branch with //", in: func(in *publisher.Input) { in.Branch = "a//b" }},
		{name: "component starting with .", in: func(in *publisher.Input) { in.Branch = "ddf/.x" }},
		{name: "branch starting with .", in: func(in *publisher.Input) { in.BaseBranch = ".main" }},
		{name: "component ending .lock", in: func(in *publisher.Input) { in.Branch = "a.lock/b" }},
		{name: "branch equals base branch", in: func(in *publisher.Input) { in.Branch = in.BaseBranch }},
		{name: "short base", in: func(in *publisher.Input) { in.Base = "abc" }},
		{name: "changed-from not a sha", in: func(in *publisher.Input) { in.ChangedFrom = "HEAD" }},
		{name: "planner risk revert", in: func(in *publisher.Input) { in.PlannerRisk = model.RiskClassRevert }},
		{name: "planner risk unknown", in: func(in *publisher.Input) { in.PlannerRisk = "high" }},
		{name: "invalid profile", in: func(in *publisher.Input) { in.Profile.Autonomy = "" }},
		{name: "relative clone", pub: func(p *publisher.Publisher) { p.Clone = "clone" }},
		{name: "relative scratch", pub: func(p *publisher.Publisher) { p.Scratch = "scratch" }},
		{name: "nil sandbox", pub: func(p *publisher.Publisher) { p.Sandbox = nil }},
		{name: "nil clock", pub: func(p *publisher.Publisher) { p.Clock = nil }},
		{name: "nil ids", pub: func(p *publisher.Publisher) { p.IDs = nil }},
		{name: "nil git", pub: func(p *publisher.Publisher) { p.Git = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := e.input(t, edit("src/a.go", "package a\n\nvar G = 7\n"))
			pub := *e.pub
			if tc.in != nil {
				tc.in(&in)
			}
			if tc.pub != nil {
				tc.pub(&pub)
			}
			if out, err := pub.Prepare(t.Context(), in); err == nil || out != nil {
				t.Fatalf("Prepare = %+v, %v; want nil outcome and an error", out, err)
			}
		})
	}
	if st := e.host.Stats(); st.Created != 0 {
		t.Errorf("stats %+v, want no cell", st)
	}
}
