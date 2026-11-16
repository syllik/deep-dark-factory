package policy_test

import (
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

var zero = strings.Repeat("0", 40)

func blob(c string) string { return strings.Repeat(c, 40) }

func prof() policy.Profile {
	return policy.Profile{Autonomy: policy.AutonomyPropose, LowMaxLines: policy.DefaultLowMaxLines}
}

func add(p string, n int) policy.ChangedPath {
	return policy.ChangedPath{Status: policy.ChangeStatusAdded, Path: p, OldMode: policy.ModeAbsent, NewMode: policy.ModeRegular, OldBlob: zero, NewBlob: blob("a"), Added: n}
}

func mod(p string, a, d int) policy.ChangedPath {
	return policy.ChangedPath{Status: policy.ChangeStatusModified, Path: p, OldMode: policy.ModeRegular, NewMode: policy.ModeRegular, OldBlob: blob("a"), NewBlob: blob("b"), Added: a, Deleted: d}
}

func del(p string, n int) policy.ChangedPath {
	return policy.ChangedPath{Status: policy.ChangeStatusDeleted, Path: p, OldMode: policy.ModeRegular, NewMode: policy.ModeAbsent, OldBlob: blob("a"), NewBlob: zero, Deleted: n}
}

func ren(from, to string, a, d int) policy.ChangedPath {
	return policy.ChangedPath{Status: policy.ChangeStatusRenamed, Path: to, OldPath: from, OldMode: policy.ModeRegular, NewMode: policy.ModeRegular, OldBlob: blob("a"), NewBlob: blob("b"), Added: a, Deleted: d}
}

func with(c policy.ChangedPath, f func(*policy.ChangedPath)) policy.ChangedPath {
	f(&c)
	return c
}
