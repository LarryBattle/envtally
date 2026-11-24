package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/LarryBattle/envtally/internal/group"
)

// renderMarkdown: tables for pasting into Jira, Slack or pull requests.
func renderMarkdown(w io.Writer, r group.Result) error {
	var b strings.Builder
	mdRow(&b, "App/Env", "Status", "Detail", "Response times")
	mdRow(&b, "---", "---", "---", "---")
	for _, e := range r.Envs {
		mdRow(&b, e.Name(), status(e), detail(e), timing(e))
	}
	b.WriteString("\n")
	mdRow(&b, "Project", "App", "Env", "Host", "Status", "Group key", "Time")
	mdRow(&b, "---", "---", "---", "---", "---", "---", "---")
	for _, e := range r.Envs {
		for _, inst := range e.Instances {
			mdRow(&b, inst.Project, inst.App, inst.Env, inst.Host, inst.State, keyOrDash(inst), instanceTime(inst))
		}
	}
	fmt.Fprintf(&b, "\n%s\n", totals(r))
	_, err := io.WriteString(w, b.String())
	return err
}

// mdRow writes one table row. Pipes are escaped: group keys often contain "|".
func mdRow(b *strings.Builder, cells ...string) {
	for _, c := range cells {
		b.WriteString("| " + strings.ReplaceAll(c, "|", `\|`) + " ")
	}
	b.WriteString("|\n")
}
