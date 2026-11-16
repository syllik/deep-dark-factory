package policy

import (
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// ErrInvalidFinding reports a finding with an invalid severity, category or
// security level.
var ErrInvalidFinding = errors.New("invalid finding")

func validateFinding(f model.Finding) error {
	if _, err := model.ParseSeverity(string(f.Severity)); err != nil {
		return fmt.Errorf("%w: severity: %w", ErrInvalidFinding, err)
	}
	if _, err := model.ParseFindingCategory(string(f.Category)); err != nil {
		return fmt.Errorf("%w: category: %w", ErrInvalidFinding, err)
	}
	if f.SecurityLevel != "" {
		if _, err := model.ParseSecurityLevel(string(f.SecurityLevel)); err != nil {
			return fmt.Errorf("%w: securityLevel: %w", ErrInvalidFinding, err)
		}
	} else if f.Category == model.FindingCategorySecurity {
		return fmt.Errorf("%w: securityLevel: required for category security", ErrInvalidFinding)
	}
	return nil
}

// IsBlocking reports whether f blocks the merge: P0 or P1, security level
// High or Medium in any category, or category hard-stop (spec §6.3).
func IsBlocking(f model.Finding) (bool, error) {
	if err := validateFinding(f); err != nil {
		return false, err
	}
	switch f.Severity {
	case model.SeverityP0, model.SeverityP1:
		return true, nil
	case model.SeverityP2, model.SeverityP3:
	}
	switch f.SecurityLevel {
	case model.SecurityLevelHigh, model.SecurityLevelMedium:
		return true, nil
	case model.SecurityLevelLow:
	}
	return f.Category == model.FindingCategoryHardStop, nil
}

// BlockingCount returns the number of blocking findings in fs, or the error
// of the first invalid finding and no count.
func BlockingCount(fs []model.Finding) (int, error) {
	n := 0
	for i, f := range fs {
		b, err := IsBlocking(f)
		if err != nil {
			return 0, fmt.Errorf("findings[%d]: %w", i, err)
		}
		if b {
			n++
		}
	}
	return n, nil
}
