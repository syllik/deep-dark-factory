// Package publisher holds the trusted Publisher's code (spec §5.2 Publisher,
// docs/concepts/roles.md#publisher). This part is pure text: Sanitize makes
// agent-written text safe for the change's sanitized section, Redactor removes
// secrets by loaded value and by pattern (spec §8.4), and RenderBody and
// SquashMessage write the change body and squash message from the project's
// change template (docs/concepts/lifecycle.md#publishing). No function here
// does I/O, reads a clock or the environment, or logs.
package publisher
