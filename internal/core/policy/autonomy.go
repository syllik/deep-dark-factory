package policy

import (
	"errors"
	"fmt"
	"slices"
)

// ErrInvalidAutonomy reports an empty or unknown autonomy level.
var ErrInvalidAutonomy = errors.New("invalid autonomy")

// Autonomy is an autonomy level (spec §6.1).
type Autonomy string

// Autonomy values.
const (
	AutonomyPropose          Autonomy = "propose"
	AutonomyMergeIntegration Autonomy = "merge-integration"
)

// parseValue returns the member of values spelled s, or an error wrapping
// sentinel that names what.
func parseValue[T ~string](sentinel error, what, s string, values ...T) (T, error) {
	if i := slices.Index(values, T(s)); i >= 0 {
		return values[i], nil
	}
	return "", fmt.Errorf("%w: unknown %s %q", sentinel, what, s)
}

// ParseAutonomy returns the Autonomy spelled s; an unknown or empty value is
// an error wrapping ErrInvalidAutonomy.
func ParseAutonomy(s string) (Autonomy, error) {
	return parseValue(ErrInvalidAutonomy, "autonomy", s, AutonomyPropose, AutonomyMergeIntegration)
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseAutonomy.
func (a *Autonomy) UnmarshalText(b []byte) error {
	v, err := ParseAutonomy(string(b))
	if err != nil {
		return err
	}
	*a = v
	return nil
}

func autonomyRank(a Autonomy) (int, error) {
	switch a {
	case AutonomyPropose:
		return 0, nil
	case AutonomyMergeIntegration:
		return 1, nil
	}
	return 0, fmt.Errorf("%w: unknown autonomy %q", ErrInvalidAutonomy, a)
}

// EffectiveAutonomy returns the lower of the operator maximum and the
// project request, with propose below merge-integration (config-layers.md
// rules).
func EffectiveAutonomy(operatorMax, projectRequest Autonomy) (Autonomy, error) {
	om, err := autonomyRank(operatorMax)
	if err != nil {
		return "", fmt.Errorf("operator maximum: %w", err)
	}
	pr, err := autonomyRank(projectRequest)
	if err != nil {
		return "", fmt.Errorf("project request: %w", err)
	}
	if pr < om {
		return projectRequest, nil
	}
	return operatorMax, nil
}
