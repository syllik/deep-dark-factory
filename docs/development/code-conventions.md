# Code conventions

Purpose: the rules every DDF Go change follows, for executors writing code and reviewers judging it.

Status: normative

Scope: DDF's own Go code. The stack and the CI steps are in the [M2 plan](../plans/2026-11-05-m2-offline-core.md) (Tech Stack, Hosted CI); the module boundaries are in [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries). This file does not restate them. Where this file and the spec disagree, the spec wins.

## Toolchain

- The Go version is the `toolchain` line of `go.mod`; nothing else pins it. Build and test with `CGO_ENABLED=0`; code that needs cgo is refused.
- Code is `gofmt`-formatted and passes the plan's **verify** sequence (`go vet`, `exhaustive`, `staticcheck`, tests, the four-target build) before it is published.
- Imports in three groups, separated by a blank line: standard library, other modules, this module.

## Layout and packages

- Layout and boundary rules: [plan OD-15 and OD-16](../plans/2026-11-05-m2-offline-core.md#open-decisions-for-the-owner) and the Architecture section. One package per core module (`internal/core/<module>`), one per adapter (`internal/adapters/<port>/<name>`), the engine in `internal/daemon`, `main` packages in `cmd/`.
- `test/boundaries/` enforces the import rules; a new rule comes with a failing fixture under its `testdata/`.
- A package that only its parent may use goes under the parent's `internal/` directory.
- No `util`, `common`, `helpers` or `misc` packages. A shared helper lives in the package whose concept it serves.
- No package-level mutable state and no `init()` with side effects. Dependencies (clock, ID source, ports, paths) are passed in.

## Naming

- Package names: short, lower case, one word, no underscores; the directory name equals the package name. Callers read `policy.EvaluateMerge`, so names do not repeat the package (`policy.Result`, not `policy.PolicyResult`).
- Go initialisms stay upper case: `ID`, `SHA`, `URL`, `JSON`, `HTTP` (`TaskID`, `HeadSHA`).
- Exported identifiers have a doc comment that starts with the name. Unexported helpers need none unless the reason is not obvious.
- Spec terms keep their spec names: a `Finding` is a finding, a `Change` is a change; no synonyms.
- Receivers: one or two letters, the same in every method of the type.
- Test names: `TestEvaluateMerge_RefusesMovedHead` style: function, then the case.

## Enum-like types and verdicts

Go has no sum types ([ADR 2](../../.ai/decisions.md)). Every closed set of values (states, verdicts, risk classes, refusal reasons, bounds, pause reasons) is:

- a named type over `string` with one `const` block of all its values, each named `<Type><Value>` (`RefusalHeadMoved`, `RiskSensitive`); the string value is the spec's spelling, so it round-trips through JSON and the store unchanged;
- parsed from text by one `Parse<Type>` function that returns an error for an unknown value; never a cast from untrusted input;
- switched on exhaustively: the `exhaustive` analyzer is a required step, a `default` branch does not count as covering the values, and a missing value fails `verify`;
- covered by a test per value where the value changes behaviour (each refusal, bound and pause reason, [plan Global Constraints](../plans/2026-11-05-m2-offline-core.md#global-constraints)).

A result that carries different data per variant is a sealed interface: an interface with an unexported marker method, implemented only by the package's own struct types, and handled by a type switch with a final `default` that returns an error.

## Errors

- A function that can fail returns `error` as its last result. Callers check it at once; `_ =` on an error needs a comment saying why it is safe.
- Wrap with context: `fmt.Errorf("apply patch %s: %w", id, err)`. Messages are lower case, no final punctuation, and name what failed, not "failed to".
- Test for kinds with `errors.Is` and `errors.As`, never by comparing message text. Sentinel errors are `var Err<Name> = errors.New(...)`; an error that carries data is a struct type implementing `error`.
- **Fail closed.** On a gate, policy, publish or recovery path an error is never turned into a default value or a permissive outcome: a read error refuses the merge, an unknown value refuses the input, a failed check counts as failed ([spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate)).
- Expected outcomes are values, not errors: a merge refusal is a `Refusal` value returned with a nil error; an error means the decision could not be made.
- No `panic` in `internal/core`, except for a broken invariant that a test proves unreachable. Adapters and the daemon recover nothing silently; a panic in a run handler is logged and the run is failed.
- Error text and log lines never contain secret values, tokens or the review-signing key; redaction ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)) applies before anything leaves the daemon.

## Purity, time and I/O

- `lifecycle`, `policy` and `scheduler` take inputs and a time value and return decisions; they do no I/O and call no clock.
- Nothing in `internal/core` calls `time.Now`, `time.Sleep`, `rand` or reads the environment. Time and IDs come from the clock and ID ports; tests use the fakes in `test/fakes/`.
- Functions that do I/O take `context.Context` as their first parameter and honour cancellation. Pure functions take no context.
- Concurrency belongs to the daemon. Core packages are synchronous and safe for one caller; the daemon owns every goroutine and stops each one on shutdown.
- Logging uses `log/slog`, in the daemon and adapters only. Core returns data; it does not log.

## Subprocesses

- Only `internal/core/workspace` (and, later, adapters that drive a CLI) import `os/exec`.
- `exec.CommandContext` with the program and an argument slice; never `sh -c`, never a command string built from input.
- Set `cmd.Env` explicitly (the plan's git env); never inherit the daemon's environment into a child.
- Capture stdout and stderr; an error includes the exit code and a bounded, redacted stderr excerpt.
- The host never executes repository content ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)): no `go run`, test runner, hook or script from a workspace.

## Tests and fixtures

- Standard `testing` only. Tests sit next to the code as `<file>_test.go`; use the external `<pkg>_test` package for API tests and the internal package only for unexported helpers.
- Table-driven tests with `t.Run(name, …)` for every rule with more than one case. Each table row names the spec rule or case it covers.
- Deterministic: fixed clock and ID source, no sleeps, no network, no dependence on test order. `t.Parallel()` only for tests with no shared state.
- Temp files under `t.TempDir()`; nothing written outside it. Git fixtures are built by a helper that runs `git` with the plan's env and `-c user.name=… -c user.email=…`.
- Static fixtures live in the package's `testdata/` directory (Go tools ignore it). Golden files are compared byte for byte and regenerated only by an explicit `-update` flag, never by default.
- Fixtures contain no real secrets, hosts or personal data; secret-shaped test values are obviously fake (`test-secret-…`).
- Every bug fix adds the test that would have caught it.

## Dependencies

- Standard library first. A new module needs a reason line in `.ai/decisions.md` ("Dependencies") and enters only through the task that owns `go.mod` changes (plan Task 1). Any later task that needs one stops and asks: `go.mod` and `go.sum` are `ciEntrypoints`.
- Versions are exact; `go.sum` is committed; no `replace` directives.
- Tools run through `tool` lines in `go.mod` and `go tool`, never through a global install.

## Comments

- Comments say why, not what. A comment cites a spec or concept anchor only where the code enforces that rule: `// spec §6.6 rule 4`.
- No restated tables, state lists or bounds in comments; link the source instead.
- English only; a to-do marker in code names its Issue number.
