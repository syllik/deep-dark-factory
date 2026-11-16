package config

import (
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

// Forge is the forge kind of a connected project.
type Forge string

// Forge values.
const (
	ForgeGitHub Forge = "github"
	ForgeLocal  Forge = "local"
)

// ParseForge returns the Forge spelled s; an unknown or empty value is an
// error wrapping ErrInvalidConfig.
func ParseForge(s string) (Forge, error) {
	return parseValue("forge", s, ForgeGitHub, ForgeLocal)
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseForge.
func (f *Forge) UnmarshalText(b []byte) error { return unmarshalVia(f, ParseForge, b) }

// Effort is the reasoning effort of a routing entry; empty means absent.
type Effort string

// Effort values.
const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

// ParseEffort returns the Effort spelled s; an unknown or empty value is an
// error wrapping ErrInvalidConfig.
func ParseEffort(s string) (Effort, error) {
	return parseValue("effort", s, EffortLow, EffortMedium, EffortHigh)
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseEffort.
func (e *Effort) UnmarshalText(b []byte) error { return unmarshalVia(e, ParseEffort, b) }

// MergeMethod is the forge merge method of a change.
type MergeMethod string

// MergeMethod values.
const (
	MergeMethodSquash MergeMethod = "squash"
)

// ParseMergeMethod returns the MergeMethod spelled s; an unknown or empty
// value is an error wrapping ErrInvalidConfig.
func ParseMergeMethod(s string) (MergeMethod, error) {
	return parseValue("merge method", s, MergeMethodSquash)
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseMergeMethod.
func (m *MergeMethod) UnmarshalText(b []byte) error { return unmarshalVia(m, ParseMergeMethod, b) }

func parseValue[T ~string](what, s string, values ...T) (T, error) {
	if i := slices.Index(values, T(s)); i >= 0 {
		return values[i], nil
	}
	return "", fmt.Errorf("%w: unknown %s %q", ErrInvalidConfig, what, s)
}

func unmarshalVia[T ~string](dst *T, parse func(string) (T, error), b []byte) error {
	v, err := parse(string(b))
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// Project is .ddf/project.yaml (schema ddf.project/v1, spec §6.7).
type Project struct {
	Version           string          `json:"version"`
	IntegrationBranch string          `json:"integrationBranch"`
	Autonomy          policy.Autonomy `json:"autonomy"`
	Verify            []string        `json:"verify"`
	Checks            struct {
		Required []string `json:"required"` // nil when absent
	} `json:"checks"`
	Image      string   `json:"image"`
	Registries []string `json:"registries"`
	Risk       struct {
		LowMaxLines int      `json:"lowMaxLines"`
		Docs        []string `json:"docs"`
		Sensitive   []string `json:"sensitive"`
	} `json:"risk"`
	CIEntrypoints []string            `json:"ciEntrypoints"`
	Forbidden     []string            `json:"forbidden"`
	HardStops     []string            `json:"hardStops"`
	Zones         map[string][]string `json:"zones"`
	Planning      struct {
		MaxAutoChildren int `json:"maxAutoChildren"`
	} `json:"planning"`
	Change struct {
		Branch   string      `json:"branch"`
		Merge    MergeMethod `json:"merge"`
		Template string      `json:"template"`
	} `json:"change"`
	Reviewers struct {
		R2DifferentProvider bool `json:"r2DifferentProvider"`
	} `json:"reviewers"`
}

func defaultProject() Project {
	var p Project
	p.Autonomy = policy.AutonomyPropose
	p.Risk.LowMaxLines = policy.DefaultLowMaxLines
	p.Planning.MaxAutoChildren = 3
	p.Change.Merge = MergeMethodSquash
	return p
}

// PolicyProfile maps the project to a policy.Profile with copied slices and
// returns Profile.Validate's error.
func (p Project) PolicyProfile() (policy.Profile, error) {
	prof := policy.Profile{
		Autonomy:       p.Autonomy,
		LowMaxLines:    p.Risk.LowMaxLines,
		Docs:           slices.Clone(p.Risk.Docs),
		Sensitive:      slices.Clone(p.Risk.Sensitive),
		CIEntrypoints:  slices.Clone(p.CIEntrypoints),
		Forbidden:      slices.Clone(p.Forbidden),
		RequiredChecks: slices.Clone(p.Checks.Required),
	}
	if err := prof.Validate(); err != nil {
		return policy.Profile{}, err
	}
	return prof, nil
}

// Projects is <operator-config>/projects.yaml (schema ddf.projects/v1).
type Projects struct {
	Projects      []ConnectedProject `json:"projects"`
	Notifications struct {
		Telegram *struct {
			UserID string `json:"userId"`
			ChatID string `json:"chatId"`
		} `json:"telegram"`
	} `json:"notifications"`
	Limits struct {
		MinFreeDiskGb  int `json:"minFreeDiskGb"`
		MaxRunsPerTask int `json:"maxRunsPerTask"`
	} `json:"limits"`
}

// ConnectedProject is one entry of projects.yaml.
type ConnectedProject struct {
	ID            string          `json:"id"`
	Forge         Forge           `json:"forge"`
	Repository    string          `json:"repository"`
	Remote        string          `json:"remote"`
	MaxAutonomy   policy.Autonomy `json:"maxAutonomy"`
	AllowedActors []string        `json:"allowedActors"`
}

func defaultProjects() Projects {
	var p Projects
	p.Limits.MinFreeDiskGb = 15
	p.Limits.MaxRunsPerTask = 20
	return p
}

// check refuses a duplicate id, a duplicate github repository and a
// duplicate local remote.
func (p Projects) check() error {
	ids := map[string]int{}
	locs := map[string]int{}
	for i, cp := range p.Projects {
		if j, ok := ids[cp.ID]; ok {
			return fmt.Errorf("projects[%d]: duplicate id of projects[%d]", i, j)
		}
		ids[cp.ID] = i
		var loc string
		switch cp.Forge {
		case ForgeGitHub:
			loc = "github:" + cp.Repository
		case ForgeLocal:
			loc = "local:" + cp.Remote
		}
		if j, ok := locs[loc]; ok {
			return fmt.Errorf("projects[%d]: duplicate repository or remote of projects[%d]", i, j)
		}
		locs[loc] = i
	}
	return nil
}

// Routing is <operator-config>/routing.yaml (schema ddf.routing/v1).
type Routing struct {
	Tiers struct {
		Deep     []Entry `json:"deep"`
		Standard []Entry `json:"standard"`
		Routine  []Entry `json:"routine"`
		Fast     []Entry `json:"fast"`
	} `json:"tiers"`
	Roles struct {
		Planner  RolePreference `json:"planner"`
		Executor RolePreference `json:"executor"`
		R1       RolePreference `json:"r1"`
		R2       RolePreference `json:"r2"`
	} `json:"roles"`
	Limits struct {
		MaxConcurrentContainers int `json:"maxConcurrentContainers"` // 0 when absent
		MaxSubagentsPerRun      int `json:"maxSubagentsPerRun"`
	} `json:"limits"`
}

// RolePreference lists the entries a role tries before its tier entries.
type RolePreference struct {
	Prefer []Entry `json:"prefer"`
}

// Entry is one routing entry: an agent adapter and a model of a provider.
type Entry struct {
	Adapter  string `json:"adapter"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   Effort `json:"effort"`
}

func defaultRouting() Routing {
	var r Routing
	r.Limits.MaxSubagentsPerRun = 3
	return r
}
