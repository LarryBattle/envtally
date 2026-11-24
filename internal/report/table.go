package report

import (
	"fmt"
	"io"

	"github.com/LarryBattle/envtally/internal/group"
)

// renderTable: one row per instance, then a summary row per app/env.
func renderTable(w io.Writer, r group.Result, opt Options) error {
	rows := [][]cell{header("PROJECT", "APP", "ENV", "HOST", "STATUS", "GROUP KEY", "TIME")}
	for _, e := range r.Envs {
		for _, inst := range e.Instances {
			rows = append(rows, []cell{
				{text: inst.Project}, {text: inst.App}, {text: inst.Env}, {text: inst.Host},
				{text: inst.State, state: inst.State},
				{text: keyOrDash(inst)},
				{text: instanceTime(inst), state: slowState(inst)},
			})
		}
	}
	if err := grid(w, "", rows, opt); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", opt.paint("header", "SUMMARY")); err != nil {
		return err
	}
	if err := grid(w, "", summaryRows(r), opt); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s\n", totals(r))
	return err
}

func header(titles ...string) []cell {
	cells := make([]cell, len(titles))
	for i, t := range titles {
		cells[i] = cell{text: t, state: "header"}
	}
	return cells
}

func summaryRows(r group.Result) [][]cell {
	var rows [][]cell
	for _, e := range r.Envs {
		rows = append(rows, []cell{
			{text: e.Name()},
			{text: status(e), state: e.State},
			{text: detail(e)},
			{text: timing(e)},
		})
	}
	return rows
}
