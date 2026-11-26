package lifecycle

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

// invalidResult wraps ErrInvalidInput and model.ErrInvalidResult: the
// reviewer result itself is unusable, so the caller treats it as an invalid
// role result (one retry, then needs-owner).
func invalidResult(role model.Role, format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s: %s", ErrInvalidInput, model.ErrInvalidResult, role, fmt.Sprintf(format, args...))
}

// priorBlocking validates the finding identity of r and returns, per finding
// ID of r.Prior, whether that prior finding was blocking. Identity is by
// Finding.ID and Finding.PriorID only (lifecycle.md#fixing): a duplicate
// finding id, a duplicate non-empty priorId, a priorId without a prior review
// and a priorId naming no prior finding are invalid results.
func priorBlocking(r ReviewResult) (map[string]bool, error) {
	ids := make(map[string]bool, len(r.Findings))
	priorIDs := make(map[string]bool, len(r.Findings))
	for i, f := range r.Findings {
		if ids[f.ID] {
			return nil, invalidResult(r.Role, "findings[%d]: duplicate finding id %q", i, f.ID)
		}
		ids[f.ID] = true
		if f.PriorID == "" {
			continue
		}
		if priorIDs[f.PriorID] {
			return nil, invalidResult(r.Role, "findings[%d]: duplicate priorId %q", i, f.PriorID)
		}
		priorIDs[f.PriorID] = true
		if !r.HasPrior {
			return nil, invalidResult(r.Role, "findings[%d]: priorId %q without a prior review", i, f.PriorID)
		}
	}
	if !r.HasPrior {
		return nil, nil
	}
	prior := make(map[string]bool, len(r.Prior))
	for i, f := range r.Prior {
		if _, dup := prior[f.ID]; dup {
			return nil, fmt.Errorf("%w: %s prior[%d]: duplicate finding id %q", ErrInvalidInput, r.Role, i, f.ID)
		}
		b, err := policy.IsBlocking(f)
		if err != nil {
			return nil, fmt.Errorf("%w: %s prior[%d]: %w", ErrInvalidInput, r.Role, i, err)
		}
		prior[f.ID] = b
	}
	for i, f := range r.Findings {
		if f.PriorID == "" {
			continue
		}
		if _, ok := prior[f.PriorID]; !ok {
			return nil, invalidResult(r.Role, "findings[%d]: priorId %q names no finding of the prior review", i, f.PriorID)
		}
	}
	return prior, nil
}

// RoutedFinding is the route of one finding of one reviewer result.
type RoutedFinding struct {
	Role  model.Role
	ID    string
	Route model.FindingRoute
}

// RouteFindings gives every finding of results its route for findings.routed
// (lifecycle.md#fixing): a blocking finding goes to fixing; a non-blocking P2
// to the follow-up task, or to minors on a task of kind followup; a
// non-blocking P3 to minors. The result is ordered by role, then by the order
// of the findings in their result. It is called on every valid round; the
// caller creates the one follow-up task of the change and appends minors only
// from the round that RouteReview sends to merge.
func RouteFindings(kind model.TaskKind, results []ReviewResult) ([]RoutedFinding, error) {
	if _, err := model.ParseTaskKind(string(kind)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var out []RoutedFinding
	seen := map[model.Role]bool{}
	for _, r := range results {
		if seen[r.Role] {
			return nil, fmt.Errorf("%w: duplicate result for %s", ErrInvalidInput, r.Role)
		}
		seen[r.Role] = true
		for i, f := range r.Findings {
			route, err := findingRoute(kind, f)
			if err != nil {
				return nil, fmt.Errorf("%w: %s findings[%d]: %w", ErrInvalidInput, r.Role, i, err)
			}
			out = append(out, RoutedFinding{Role: r.Role, ID: f.ID, Route: route})
		}
	}
	slices.SortStableFunc(out, func(a, b RoutedFinding) int { return cmp.Compare(a.Role, b.Role) })
	return out, nil
}

func findingRoute(kind model.TaskKind, f model.Finding) (model.FindingRoute, error) {
	b, err := policy.IsBlocking(f)
	if err != nil {
		return "", err
	}
	if b {
		return model.FindingRouteFixing, nil
	}
	switch f.Severity {
	case model.SeverityP2:
		if kind == model.TaskKindFollowup {
			return model.FindingRouteMinors, nil
		}
		return model.FindingRouteFollowup, nil
	case model.SeverityP3:
		return model.FindingRouteMinors, nil
	case model.SeverityP0, model.SeverityP1:
	}
	return "", fmt.Errorf("non-blocking finding with severity %s", f.Severity)
}
