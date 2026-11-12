// Package ports holds the port interfaces of spec §4.2 (forge, agent,
// sandbox, notify, secrets), the clock, ID source and instance lock, and
// their argument and result types. Adapters implement them; the core and
// the daemon depend only on these interfaces.
package ports
