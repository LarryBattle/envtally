// Package report renders check results in the supported output styles.
package report

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LarryBattle/envtally/internal/group"
)

// Options controls rendering.
type Options struct {
	Color bool // ANSI colors; the caller decides (TTY, NO_COLOR, --no-color)
}

// Render writes a check result in the given style.
func Render(w io.Writer, style string, r group.Result, opt Options) error {
	switch style {
	case "table":
		return renderTable(w, r, opt)
	case "tree":
		return renderTree(w, r, opt)
	case "compact":
		return renderCompact(w, r, opt)
	case "markdown":
		return renderMarkdown(w, r)
	case "csv":
		return renderCSV(w, r)
	case "json":
		return renderJSON(w, r)
	}
	return fmt.Errorf("unknown output style %q", style)
}

// Dur formats a duration for people: 98ms, 1.2s, 1m5s.
func Dur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Round(time.Millisecond).Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

func symbol(state string) string {
	switch state {
	case group.EnvInSync, group.EnvUp:
		return "✓"
	case group.EnvUnknown:
		return "?"
	}
	return "✗"
}

// status is the env status with its symbol, e.g. "✗ DRIFT".
func status(e group.Env) string { return symbol(e.State) + " " + e.State }

// detail explains an env's state: failing instances, then keys with counts.
func detail(e group.Env) string {
	var parts []string
	for _, inst := range e.Instances {
		if inst.State != group.InstUp {
			parts = append(parts, fmt.Sprintf("[%s %s: %s]", inst.Host, inst.State, inst.Detail))
		}
	}
	for _, k := range e.Keys {
		parts = append(parts, fmt.Sprintf("[%s ×%d]", k.Key, k.Count))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("[%d up]", len(e.Instances))
	}
	return strings.Join(parts, " ")
}

// timing lists response times of UP instances with min/avg/max.
func timing(e group.Env) string {
	var times []string
	for _, inst := range e.Instances {
		if inst.State == group.InstUp {
			times = append(times, Dur(inst.Total))
		}
	}
	if len(times) == 0 {
		return ""
	}
	return fmt.Sprintf("[%s] min %s · avg %s · max %s", strings.Join(times, ", "), Dur(e.Min), Dur(e.Avg), Dur(e.Max))
}

func instanceTime(inst group.Instance) string {
	if inst.Slow {
		return Dur(inst.Total) + " (slow)"
	}
	return Dur(inst.Total)
}

func keyOrDash(inst group.Instance) string {
	if inst.Key == "" {
		return "-"
	}
	return inst.Key
}

// totals is the closing line, e.g. "3 app/envs: 1 in sync, 1 drift, 1 down".
func totals(r group.Result) string {
	counts := map[string]int{}
	for _, e := range r.Envs {
		counts[e.State]++
	}
	var parts []string
	for _, state := range []string{group.EnvInSync, group.EnvUp, group.EnvDrift, group.EnvUnknown, group.EnvDown} {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], strings.ToLower(state)))
		}
	}
	return fmt.Sprintf("%d app/envs: %s", len(r.Envs), strings.Join(parts, ", "))
}

// cell is one grid cell; state picks its color.
type cell struct {
	text  string
	state string
}

func plain(texts ...string) []cell {
	cells := make([]cell, len(texts))
	for i, t := range texts {
		cells[i] = cell{text: t}
	}
	return cells
}

func (o Options) paint(state, text string) string {
	if !o.Color || text == "" {
		return text
	}
	code := ""
	switch state {
	case group.EnvInSync, group.InstUp:
		code = "32" // green
	case group.EnvDrift, group.EnvUnknown, group.InstNoInfo, "slow":
		code = "33" // yellow
	case group.EnvDown:
		code = "31" // red
	case "header":
		code = "1" // bold
	default:
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// grid writes rows as aligned columns. Widths count runes, so symbols and
// color codes do not break alignment (text/tabwriter counts bytes).
func grid(w io.Writer, indent string, rows [][]cell, opt Options) error {
	var widths []int
	for _, row := range rows {
		for i, c := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c.text))
		}
	}
	for _, row := range rows {
		last := len(row) - 1
		for last > 0 && row[last].text == "" {
			last--
		}
		var b strings.Builder
		b.WriteString(indent)
		for i := 0; i <= last; i++ {
			b.WriteString(opt.paint(row[i].state, row[i].text))
			if i < last {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(row[i].text)+2))
			}
		}
		if _, err := fmt.Fprintln(w, b.String()); err != nil {
			return err
		}
	}
	return nil
}

func slowState(inst group.Instance) string {
	if inst.Slow {
		return "slow"
	}
	return ""
}
