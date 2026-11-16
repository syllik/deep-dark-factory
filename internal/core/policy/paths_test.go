package policy_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func TestCheckPaths(t *testing.T) {
	ops := prof()
	ops.Forbidden = []string{"ops/**"}
	symlink := with(add("link", 0), func(c *policy.ChangedPath) { c.NewMode = policy.ModeSymlink })
	toSymlink := with(mod("file", 1, 1), func(c *policy.ChangedPath) {
		c.Status, c.NewMode = policy.ChangeStatusTypeChanged, policy.ModeSymlink
	})
	delSymlink := with(del("old-link", 1), func(c *policy.ChangedPath) { c.OldMode = policy.ModeSymlink })
	prodYAML := mod("deploy/prod.yaml", 1, 1)
	approvedProd := policy.HardStopEntry{Path: "deploy/prod.yaml", Mode: policy.ModeRegular, BlobID: blob("b")}

	tests := []struct {
		name       string
		p          policy.Profile
		paths      []policy.ChangedPath
		approved   []policy.HardStopEntry
		forbidden  []string
		symlinks   []string
		hardStops  []policy.HardStopEntry
		unapproved []policy.HardStopEntry
	}{
		{"§6.5 rule 3: project forbidden ops/**", ops, []policy.ChangedPath{add("ops/x", 1)}, nil, []string{"ops/x"}, nil, nil, nil},
		{"changed-paths rule 3: deleted .github file", prof(), []policy.ChangedPath{del(".github/x.yml", 1)}, nil, []string{".github/x.yml"}, nil, nil, nil},
		{"changed-paths rule 2: rename .env lists the old side", prof(), []policy.ChangedPath{ren(".env", "config/env", 0, 0)}, nil, []string{".env"}, nil, nil, nil},
		{"changed-paths rule 4: added and changed symlinks, deleted not", prof(), []policy.ChangedPath{toSymlink, delSymlink, symlink}, nil, nil, []string{"file", "link"}, nil, nil},
		{"§6.4 rule 3: approved entry passes", prof(), []policy.ChangedPath{prodYAML}, []policy.HardStopEntry{approvedProd}, nil, nil, []policy.HardStopEntry{approvedProd}, nil},
		{"§6.4 rule 3: other blob is a new hard stop", prof(), []policy.ChangedPath{prodYAML},
			[]policy.HardStopEntry{{Path: "deploy/prod.yaml", Mode: policy.ModeRegular, BlobID: blob("c")}}, nil, nil,
			[]policy.HardStopEntry{approvedProd}, []policy.HardStopEntry{approvedProd}},
		{"§6.4 rule 3: other mode is a new hard stop", prof(), []policy.ChangedPath{prodYAML},
			[]policy.HardStopEntry{{Path: "deploy/prod.yaml", Mode: policy.ModeExecutable, BlobID: blob("b")}}, nil, nil,
			[]policy.HardStopEntry{approvedProd}, []policy.HardStopEntry{approvedProd}},
		{"§6.4 rule 3: deletion is a new hard stop", prof(), []policy.ChangedPath{del("deploy/prod.yaml", 1)}, []policy.HardStopEntry{approvedProd}, nil, nil,
			[]policy.HardStopEntry{{Path: "deploy/prod.yaml", Deleted: true}}, []policy.HardStopEntry{{Path: "deploy/prod.yaml", Deleted: true}}},
		{"§6.4: approved deletion passes", prof(), []policy.ChangedPath{del("deploy/prod.yaml", 1)}, []policy.HardStopEntry{{Path: "deploy/prod.yaml", Deleted: true}}, nil, nil,
			[]policy.HardStopEntry{{Path: "deploy/prod.yaml", Deleted: true}}, nil},
		{"§6.4 rule 1: old side of a rename also added takes the added state", prof(),
			[]policy.ChangedPath{ren("deploy/a.yaml", "x/a.yaml", 0, 0), add("deploy/a.yaml", 1)}, nil, nil, nil,
			[]policy.HardStopEntry{{Path: "deploy/a.yaml", Mode: policy.ModeRegular, BlobID: blob("a")}},
			[]policy.HardStopEntry{{Path: "deploy/a.yaml", Mode: policy.ModeRegular, BlobID: blob("a")}}},
		{"sorted and unique", prof(),
			[]policy.ChangedPath{del(".github/b", 1), ren(".github/b", ".github/a", 0, 0), add("k8s/b.yaml", 1), del("Dockerfile", 1), ren("helm/x", "k8s/a.yaml", 0, 0)}, nil,
			[]string{".github/a", ".github/b"}, nil,
			[]policy.HardStopEntry{{Path: "Dockerfile", Deleted: true}, {Path: "helm/x", Deleted: true}, {Path: "k8s/a.yaml", Mode: policy.ModeRegular, BlobID: blob("b")}, {Path: "k8s/b.yaml", Mode: policy.ModeRegular, BlobID: blob("a")}},
			[]policy.HardStopEntry{{Path: "Dockerfile", Deleted: true}, {Path: "helm/x", Deleted: true}, {Path: "k8s/a.yaml", Mode: policy.ModeRegular, BlobID: blob("b")}, {Path: "k8s/b.yaml", Mode: policy.ModeRegular, BlobID: blob("a")}}},
		{"clean change passes", prof(), []policy.ChangedPath{add("a/AGENTS.md", 1), mod("src/x.go", 1, 1)}, nil, nil, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.CheckPaths(tt.p, tt.paths, tt.approved)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Forbidden, tt.forbidden) || !slices.Equal(got.Symlinks, tt.symlinks) ||
				!slices.Equal(got.HardStops, tt.hardStops) || !slices.Equal(got.Unapproved, tt.unapproved) {
				t.Fatalf("got %+v", got)
			}
			want := len(tt.forbidden) == 0 && len(tt.symlinks) == 0 && len(tt.unapproved) == 0
			if got.Passes() != want {
				t.Fatalf("Passes() = %v, want %v", got.Passes(), want)
			}
		})
	}
}

func TestCheckPaths_Errors(t *testing.T) {
	bad := prof()
	bad.Forbidden = []string{""}
	ok := []policy.ChangedPath{add("a", 1)}
	tests := []struct {
		name     string
		p        policy.Profile
		paths    []policy.ChangedPath
		approved []policy.HardStopEntry
		want     error
	}{
		{"empty change", prof(), nil, nil, policy.ErrNoChangedPaths},
		{"invalid profile", bad, ok, nil, policy.ErrInvalidProfile},
		{"invalid changed path", prof(), []policy.ChangedPath{add(".git/config", 1)}, nil, policy.ErrInvalidChangedPath},
		{"approved deleted with a mode", prof(), ok, []policy.HardStopEntry{{Path: "k8s/a", Deleted: true, Mode: policy.ModeRegular}}, policy.ErrInvalidHardStopEntry},
		{"approved present without a blob", prof(), ok, []policy.HardStopEntry{{Path: "k8s/a", Mode: policy.ModeRegular}}, policy.ErrInvalidHardStopEntry},
		{"approved present with zero blob", prof(), ok, []policy.HardStopEntry{{Path: "k8s/a", Mode: policy.ModeRegular, BlobID: zero}}, policy.ErrInvalidHardStopEntry},
		{"approved absent mode", prof(), ok, []policy.HardStopEntry{{Path: "k8s/a", Mode: policy.ModeAbsent, BlobID: blob("a")}}, policy.ErrInvalidHardStopEntry},
		{"approved neither deleted nor stated", prof(), ok, []policy.HardStopEntry{{Path: "k8s/a"}}, policy.ErrInvalidHardStopEntry},
		{"approved bad path", prof(), ok, []policy.HardStopEntry{{Path: "k8s/../x", Deleted: true}}, policy.ErrInvalidHardStopEntry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := policy.CheckPaths(tt.p, tt.paths, tt.approved); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
