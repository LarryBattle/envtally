package report

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"

	"github.com/LarryBattle/envtally/internal/group"
)

// renderCSV: one row per instance.
func renderCSV(w io.Writer, r group.Result) error {
	out := csv.NewWriter(w)
	rows := [][]string{{
		"project", "app", "env", "url", "host", "instance_status", "group_key",
		"env_status", "health_ms", "info_ms", "total_ms", "slow", "detail",
	}}
	for _, e := range r.Envs {
		for _, inst := range e.Instances {
			rows = append(rows, []string{
				inst.Project, inst.App, inst.Env, inst.URL, inst.Host, inst.State, inst.Key,
				e.State, ms(inst.Health), ms(inst.Info), ms(inst.Total),
				strconv.FormatBool(inst.Slow), inst.Detail,
			})
		}
	}
	if err := out.WriteAll(rows); err != nil {
		return err
	}
	return out.Error()
}

func ms(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}
