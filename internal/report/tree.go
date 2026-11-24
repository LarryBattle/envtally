package report

import (
	"fmt"
	"io"

	"github.com/LarryBattle/envtally/internal/group"
)

// renderTree: project -> app -> env -> instances, by indentation.
func renderTree(w io.Writer, r group.Result, opt Options) error {
	project, app := "", ""
	for i, e := range r.Envs {
		if e.Project != project {
			project, app = e.Project, ""
			if _, err := fmt.Fprintln(w, opt.paint("header", project)); err != nil {
				return err
			}
		}
		if e.App != app {
			app = e.App
			if _, err := fmt.Fprintln(w, "  "+app); err != nil {
				return err
			}
		}
		line := fmt.Sprintf("    %s  %s  %s", e.Env, opt.paint(e.State, status(e)), detail(e))
		if t := timing(e); t != "" {
			line += "  " + t
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		var rows [][]cell
		for _, inst := range r.Envs[i].Instances {
			rows = append(rows, []cell{
				{text: inst.Host},
				{text: inst.State, state: inst.State},
				{text: keyOrDash(inst)},
				{text: instanceTime(inst), state: slowState(inst)},
			})
		}
		if err := grid(w, "      ", rows, opt); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\n%s\n", totals(r))
	return err
}
