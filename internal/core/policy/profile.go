package policy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrInvalidProfile reports a project profile value policy refuses.
var ErrInvalidProfile = errors.New("invalid profile")

// DefaultLowMaxLines is the default risk.lowMaxLines (spec §6.2); the config
// loader fills it, policy uses Profile.LowMaxLines as given.
const DefaultLowMaxLines = 200

// Profile holds the project profile fields policy and the merge gate read
// (spec §6.7). Values are used as given; policy applies no defaults.
type Profile struct {
	Autonomy       Autonomy // the project's requested autonomy
	LowMaxLines    int      // risk.lowMaxLines
	Docs           []string // risk.docs globs
	Sensitive      []string // risk.sensitive globs
	CIEntrypoints  []string // ciEntrypoints globs
	Forbidden      []string // forbidden globs added to the defaults
	RequiredChecks []string // checks.required; nil when the key is absent
}

// Validate returns an error wrapping ErrInvalidProfile, naming the field and
// index, for the first invalid value.
func (p Profile) Validate() error {
	if _, err := ParseAutonomy(string(p.Autonomy)); err != nil {
		return fmt.Errorf("%w: autonomy: %w", ErrInvalidProfile, err)
	}
	if p.LowMaxLines < 0 {
		return fmt.Errorf("%w: risk.lowMaxLines: negative value %d", ErrInvalidProfile, p.LowMaxLines)
	}
	lists := []struct {
		name  string
		globs []string
	}{{"risk.docs", p.Docs}, {"risk.sensitive", p.Sensitive}, {"ciEntrypoints", p.CIEntrypoints}, {"forbidden", p.Forbidden}}
	for _, l := range lists {
		for i, g := range l.globs {
			if err := validateGlob(g); err != nil {
				return fmt.Errorf("%w: %s[%d]: %w", ErrInvalidProfile, l.name, i, err)
			}
		}
	}
	if p.RequiredChecks != nil && len(p.RequiredChecks) == 0 {
		return fmt.Errorf("%w: checks.required: empty list", ErrInvalidProfile)
	}
	for i, c := range p.RequiredChecks {
		if c == "" {
			return fmt.Errorf("%w: checks.required[%d]: empty name", ErrInvalidProfile, i)
		}
	}
	return nil
}

func validateGlob(g string) error {
	switch {
	case g == "":
		return errors.New("empty glob")
	case strings.HasPrefix(g, "/"):
		return fmt.Errorf("glob %q starts with '/'", g)
	case !doublestar.ValidatePattern(g):
		return fmt.Errorf("glob %q is not a valid pattern", g)
	}
	return nil
}
