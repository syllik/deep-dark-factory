package git_test

import (
	"errors"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		out  string
		want git.Version
		ok   bool
	}{
		{"git version 2.56.0\n", git.Version{Major: 2, Minor: 56}, true},
		{"git version 2.39.5 (Apple Git-154)", git.Version{Major: 2, Minor: 39, Patch: 5}, true},
		{"git version 2.40.1.windows.1", git.Version{Major: 2, Minor: 40, Patch: 1}, true},
		{"git version 2.45.0.rc0", git.Version{Major: 2, Minor: 45}, true},
		{"", git.Version{}, false},
		{"git 2.56.0", git.Version{}, false},
		{"git version 2", git.Version{}, false},
		{"git version 2.x.0", git.Version{}, false},
	}
	for _, c := range cases {
		t.Run(c.out, func(t *testing.T) {
			got, err := git.ParseVersion(c.out)
			if (err == nil) != c.ok || got != c.want {
				t.Fatalf("ParseVersion(%q) = %v, %v; want %v, ok %v", c.out, got, err, c.want, c.ok)
			}
		})
	}
}

func TestVersion_LessThanMin(t *testing.T) {
	cases := []struct {
		v    git.Version
		less bool
	}{
		{git.Version{Major: 2, Minor: 38, Patch: 9}, true},
		{git.Version{Major: 2, Minor: 38, Patch: 1}, true},
		{git.Version{Major: 2, Minor: 39}, false},
		{git.Version{Major: 2, Minor: 56}, false},
	}
	for _, c := range cases {
		if got := c.v.Less(git.MinVersion()); got != c.less {
			t.Errorf("%s.Less(%s) = %v, want %v", c.v, git.MinVersion(), got, c.less)
		}
	}
	err := error(&git.VersionError{Found: git.Version{Major: 2, Minor: 38, Patch: 1}, Min: git.MinVersion()})
	if !errors.Is(err, git.ErrGitTooOld) || err.Error() != "git 2.38.1 is older than the minimum 2.39.0" {
		t.Fatalf("VersionError = %q, Is ErrGitTooOld %v", err, errors.Is(err, git.ErrGitTooOld))
	}
}
