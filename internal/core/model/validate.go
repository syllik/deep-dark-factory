package model

import (
	"errors"
	"fmt"
)

// fieldCheck validates one field of a record.
type fieldCheck func(record string) error

// required checks that v parses through parse; the zero value fails.
func required[T ~string](field string, v T, parse func(string) (T, error)) fieldCheck {
	return func(record string) error {
		if _, err := parse(string(v)); err != nil {
			return fmt.Errorf("validate %s: field %s: %w", record, field, err)
		}
		return nil
	}
}

// optional checks that v is the zero value or parses through parse.
func optional[T ~string](field string, v T, parse func(string) (T, error)) fieldCheck {
	if v == "" {
		return func(string) error { return nil }
	}
	return required(field, v, parse)
}

// validate runs every check against record and joins the failures.
func validate(record string, checks ...fieldCheck) error {
	errs := make([]error, 0, len(checks))
	for _, c := range checks {
		errs = append(errs, c(record))
	}
	return errors.Join(errs...)
}

// Validate reports a missing, unknown or duplicate enum value, an invalid
// key or an invalid agent default.
func (t Task) Validate() error {
	checks := []fieldCheck{
		required("key", t.Key, ParseTaskKey),
		required("kind", t.Kind, ParseTaskKind),
		optional("parentKey", t.ParentKey, ParseTaskKey),
		required("state", t.State, ParseTaskState),
		optional("risk", t.Risk, ParseRiskClass),
		required("priority", t.Priority, ParsePriority),
		optional("plannerRisk", t.PlannerRisk, ParseRiskClass),
	}
	seen := make(map[TaskFlag]bool, len(t.Flags))
	for _, f := range t.Flags {
		checks = append(checks, required("flags", f, ParseTaskFlag))
		if seen[f] {
			checks = append(checks, func(record string) error {
				return fmt.Errorf("validate %s: field flags: duplicate %q", record, f)
			})
		}
		seen[f] = true
	}
	for i, d := range t.AgentDefaults {
		checks = append(checks, func(record string) error {
			if err := d.Validate(); err != nil {
				return fmt.Errorf("validate %s: field agentDefaults[%d]: %w", record, i, err)
			}
			return nil
		})
	}
	return validate("task", checks...)
}

// Validate reports a missing or unknown enum value or an invalid key.
func (r Run) Validate() error {
	return validate("run",
		required("key", r.Key, ParseTaskKey),
		required("role", r.Role, ParseRole),
		required("purpose", r.Purpose, ParseRunPurpose),
		required("status", r.Status, ParseRunStatus),
		optional("failureClass", r.FailureClass, ParseFailureClass),
	)
}

// Validate reports a missing or unknown enum value or an invalid key.
func (c Change) Validate() error {
	return validate("change",
		required("key", c.Key, ParseTaskKey),
		required("kind", c.Kind, ParseChangeKind),
	)
}

// Validate reports a missing or unknown enum value or an invalid key.
func (r Review) Validate() error {
	return validate("review",
		required("key", r.Key, ParseTaskKey),
		required("role", r.Role, ParseRole),
		required("verdict", r.Verdict, ParseReviewVerdict),
		optional("raiseRisk", r.RaiseRisk, ParseRiskClass),
	)
}

// Validate reports a missing or unknown enum value or an invalid key.
func (f Finding) Validate() error {
	return validate("finding",
		required("key", f.Key, ParseTaskKey),
		required("role", f.Role, ParseRole),
		required("severity", f.Severity, ParseSeverity),
		required("category", f.Category, ParseFindingCategory),
		optional("securityLevel", f.SecurityLevel, ParseSecurityLevel),
		optional("routed", f.Routed, ParseFindingRoute),
	)
}

// Validate reports an invalid key.
func (q Question) Validate() error {
	return validate("question", required("key", q.Key, ParseTaskKey))
}

// Validate reports an invalid key.
func (d Decision) Validate() error {
	return validate("decision", required("key", d.Key, ParseTaskKey))
}

// Validate reports a missing or unknown enum value or an invalid key.
func (i Intent) Validate() error {
	return validate("intent",
		required("key", i.Key, ParseTaskKey),
		required("operation", i.Operation, ParseIntentOperation),
		required("status", i.Status, ParseIntentStatus),
	)
}

// Validate reports an unknown enum value or an invalid key; an empty key
// marks a global event.
func (e Event) Validate() error {
	return validate("event",
		optional("key", e.Key, ParseTaskKey),
		optional("role", e.Role, ParseRole),
	)
}
