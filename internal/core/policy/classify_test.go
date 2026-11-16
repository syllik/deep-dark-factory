package policy_test

import (
	"errors"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func TestClassify(t *testing.T) {
	docs := prof()
	docs.Docs = []string{"docs/**", "**/*.md"}
	strict := prof()
	strict.LowMaxLines = 0
	sens := prof()
	sens.Sensitive = []string{"secrets/**"}
	ci := prof()
	ci.CIEntrypoints = []string{"package.json"}
	ops := prof()
	ops.Forbidden = []string{"ops/**"}
	bin := func(c policy.ChangedPath) policy.ChangedPath {
		return with(c, func(c *policy.ChangedPath) { c.Binary = true })
	}
	modeOnly := with(mod("run.sh", 0, 0), func(c *policy.ChangedPath) { c.NewMode, c.NewBlob = policy.ModeExecutable, c.OldBlob })

	tests := []struct {
		name   string
		p      policy.Profile
		paths  []policy.ChangedPath
		want   model.RiskClass
		lines  int
		binary bool
	}{
		{"docs row: only risk.docs paths", docs, []policy.ChangedPath{add("docs/a.txt", 900), mod("README.md", 5, 5)}, model.RiskClassDocs, 910, false},
		{"docs row: nested agent-instruction file is never docs", docs, []policy.ChangedPath{add("docs/a.txt", 9), mod("a/AGENTS.md", 1, 0)}, model.RiskClassLow, 10, false},
		{"low row: 200 counted lines", prof(), []policy.ChangedPath{add("a.go", 150), mod("b.go", 30, 20)}, model.RiskClassLow, 200, false},
		{"standard row: 201 counted lines", prof(), []policy.ChangedPath{add("a.go", 150), mod("b.go", 31, 20)}, model.RiskClassStandard, 201, false},
		{"changed-lines rule 3: lockfile not counted", prof(), []policy.ChangedPath{mod("web/package-lock.json", 4000, 1000), add("a.go", 10)}, model.RiskClassLow, 10, false},
		{"changed-lines rule 3: binary lockfile skipped", prof(), []policy.ChangedPath{bin(add("bun.lockb", 0)), add("a.go", 5)}, model.RiskClassLow, 5, false},
		{"changed-lines rule 5: binary exceeds lowMaxLines", prof(), []policy.ChangedPath{bin(add("img.png", 0))}, model.RiskClassStandard, 0, true},
		{"changed-lines rule 4: pure rename counts 0", strict, []policy.ChangedPath{ren("a.go", "b.go", 0, 0)}, model.RiskClassLow, 0, false},
		{"changed-lines rule 6: mode-only change counts 0", strict, []policy.ChangedPath{modeOnly}, model.RiskClassLow, 0, false},
		{"changed-lines rule 7: rename old path matches risk.sensitive", sens, []policy.ChangedPath{ren("secrets/k.go", "pub/k.go", 0, 0)}, model.RiskClassSensitive, 0, false},
		{"changed-paths rule 3: deleted Dockerfile is a hard stop", prof(), []policy.ChangedPath{del("Dockerfile", 3)}, model.RiskClassSensitive, 3, false},
		{"rule 4: ciEntrypoints force sensitive", ci, []policy.ChangedPath{mod("package.json", 1, 1)}, model.RiskClassSensitive, 2, false},
		{"forbidden default path forces sensitive", prof(), []policy.ChangedPath{mod(".github/workflows/ci.yml", 1, 0)}, model.RiskClassSensitive, 1, false},
		{"forbidden project path forces sensitive", ops, []policy.ChangedPath{add("ops/run.sh", 1)}, model.RiskClassSensitive, 1, false},
		{"changed-lines rule 7: rename yarn.lock to notes.txt is counted", prof(), []policy.ChangedPath{ren("yarn.lock", "notes.txt", 150, 60)}, model.RiskClassStandard, 210, false},
		{"docs row: risk.docs is case-sensitive", docs, []policy.ChangedPath{add("Docs/a.txt", 1)}, model.RiskClassLow, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Classify(tt.p, tt.paths)
			if err != nil {
				t.Fatal(err)
			}
			want := policy.Classification{Class: tt.want, ChangedLines: tt.lines, HasBinary: tt.binary}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			if got.Class == model.RiskClassRevert {
				t.Fatal("Classify returned revert")
			}
		})
	}
}

func TestClassify_Errors(t *testing.T) {
	badGlob, absGlob, emptyChecks, badAuto, negLines := prof(), prof(), prof(), prof(), prof()
	badGlob.Forbidden = []string{"["}
	absGlob.Sensitive = []string{"/abs"}
	emptyChecks.RequiredChecks = []string{}
	badAuto.Autonomy = ""
	negLines.LowMaxLines = -1
	tests := []struct {
		name  string
		p     policy.Profile
		paths []policy.ChangedPath
		want  error
	}{
		{"empty change", prof(), nil, policy.ErrNoChangedPaths},
		{"project glob '['", badGlob, []policy.ChangedPath{add("a", 1)}, policy.ErrInvalidProfile},
		{"project glob '/abs'", absGlob, []policy.ChangedPath{add("a", 1)}, policy.ErrInvalidProfile},
		{"empty checks.required", emptyChecks, []policy.ChangedPath{add("a", 1)}, policy.ErrInvalidProfile},
		{"empty autonomy", badAuto, []policy.ChangedPath{add("a", 1)}, policy.ErrInvalidProfile},
		{"negative lowMaxLines", negLines, []policy.ChangedPath{add("a", 1)}, policy.ErrInvalidProfile},
		{"'..' segment", prof(), []policy.ChangedPath{add("a/../b", 1)}, policy.ErrInvalidChangedPath},
		{"'.git' segment", prof(), []policy.ChangedPath{add("a/.GIT/config", 1)}, policy.ErrInvalidChangedPath},
		{"leading '/'", prof(), []policy.ChangedPath{add("/a", 1)}, policy.ErrInvalidChangedPath},
		{"renamed without OldPath", prof(), []policy.ChangedPath{ren("", "b", 0, 0)}, policy.ErrInvalidChangedPath},
		{"OldPath without rename", prof(), []policy.ChangedPath{with(add("b", 1), func(c *policy.ChangedPath) { c.OldPath = "a" })}, policy.ErrInvalidChangedPath},
		{"mismatched mode and blob", prof(), []policy.ChangedPath{with(add("a", 1), func(c *policy.ChangedPath) { c.NewBlob = zero })}, policy.ErrInvalidChangedPath},
		{"added with an old mode", prof(), []policy.ChangedPath{with(add("a", 1), func(c *policy.ChangedPath) { c.OldMode = policy.ModeRegular })}, policy.ErrInvalidChangedPath},
		{"uppercase blob", prof(), []policy.ChangedPath{with(add("a", 1), func(c *policy.ChangedPath) { c.NewBlob = blob("A") })}, policy.ErrInvalidChangedPath},
		{"unknown status", prof(), []policy.ChangedPath{with(add("a", 1), func(c *policy.ChangedPath) { c.Status = "copied" })}, policy.ErrInvalidChangedPath},
		{"negative count", prof(), []policy.ChangedPath{mod("a", -1, 0)}, policy.ErrInvalidChangedPath},
		{"two non-deleted entries for one path", prof(), []policy.ChangedPath{add("a", 1), ren("b", "a", 0, 0)}, policy.ErrInvalidChangedPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Classify(tt.p, tt.paths)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if got != (policy.Classification{}) {
				t.Fatalf("got %+v with an error", got)
			}
		})
	}
}

func TestRaise(t *testing.T) {
	d, l, s, x, r := model.RiskClassDocs, model.RiskClassLow, model.RiskClassStandard, model.RiskClassSensitive, model.RiskClassRevert
	tests := []struct {
		name    string
		base    model.RiskClass
		raises  []model.RiskClass
		want    model.RiskClass
		wantErr bool
	}{
		{"rule 2: max over the raises", d, []model.RiskClass{l, x, s}, x, false},
		{"rule 2: planner raises low to standard", l, []model.RiskClass{s}, s, false},
		{"rule 2: a lower raise never lowers", x, []model.RiskClass{d, l, s}, x, false},
		{"rule 2: no raise keeps base", s, nil, s, false},
		{"revert base plus standard raise", r, []model.RiskClass{s}, s, false},
		{"revert base plus docs raise stays revert", r, []model.RiskClass{d}, r, false},
		{"revert base with no raise", r, nil, r, false},
		{"raise of revert", l, []model.RiskClass{r}, "", true},
		{"empty raise", l, []model.RiskClass{""}, "", true},
		{"unknown raise", l, []model.RiskClass{"high"}, "", true},
		{"unknown base", "high", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Raise(tt.base, tt.raises...)
			if tt.wantErr != errors.Is(err, policy.ErrInvalidRiskClass) || (!tt.wantErr && err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewers(t *testing.T) {
	r1check := []policy.Reviewer{{Role: model.RoleR1, HardStopChecklist: true}}
	tests := []struct {
		class model.RiskClass
		want  []policy.Reviewer
	}{
		{model.RiskClassDocs, r1check},
		{model.RiskClassLow, r1check},
		{model.RiskClassRevert, r1check},
		{model.RiskClassStandard, []policy.Reviewer{{Role: model.RoleR1}, {Role: model.RoleR2, HardStopChecklist: true}}},
		{model.RiskClassSensitive, []policy.Reviewer{{Role: model.RoleR1}, {Role: model.RoleR2, Deep: true, HardStopChecklist: true}}},
	}
	for _, tt := range tests {
		t.Run("§6.2 reviewers "+string(tt.class), func(t *testing.T) {
			got, err := policy.Reviewers(tt.class)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
	for _, c := range []model.RiskClass{"", "high"} {
		if _, err := policy.Reviewers(c); !errors.Is(err, policy.ErrInvalidRiskClass) {
			t.Errorf("Reviewers(%q) err = %v", c, err)
		}
	}
}
