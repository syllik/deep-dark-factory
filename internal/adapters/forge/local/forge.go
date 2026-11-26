package local

import "github.com/syllik/deep-dark-factory/internal/core/ports"

// *Forge implements the whole forge port: reads, task and change writes,
// checks, reviews, merge and revert.
var _ ports.Forge = (*Forge)(nil)
