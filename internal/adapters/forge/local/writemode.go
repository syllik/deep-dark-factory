package local

import (
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// guard is the first step of every forge write (ports.Forge contract). It
// reports live=true for WriteModeLive. For WriteModeDryRun it logs method
// and the given ids or keys, never free text, and reports live=false with a
// nil error; the caller then returns its zero result. Any other mode is an
// error and the caller writes nothing.
func (f *Forge) guard(mode ports.WriteMode, method string, ids ...any) (live bool, err error) {
	switch mode {
	case ports.WriteModeLive:
		return true, nil
	case ports.WriteModeDryRun:
		f.cfg.Logger.Info("forge-local dry-run write", append([]any{"method", method}, ids...)...)
		return false, nil
	}
	return false, fmt.Errorf("%s: invalid write mode %q", method, string(mode))
}
