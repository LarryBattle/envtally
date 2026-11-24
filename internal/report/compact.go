package report

import (
	"fmt"
	"io"

	"github.com/LarryBattle/envtally/internal/group"
)

// renderCompact: one line per app/env, e.g.
// billing/payments/dev ✗ DRIFT [1.4.2|a1b2 ×1] [1.4.1|9f8e ×1]
func renderCompact(w io.Writer, r group.Result, opt Options) error {
	for _, e := range r.Envs {
		if _, err := fmt.Fprintf(w, "%s %s %s\n", e.Name(), opt.paint(e.State, status(e)), detail(e)); err != nil {
			return err
		}
	}
	return nil
}
