package report

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// URLRow is one instance with its expanded links (swagger, jenkins, ...).
type URLRow struct {
	Project string            `json:"project"`
	App     string            `json:"app"`
	Env     string            `json:"env"`
	URL     string            `json:"url"`
	Links   map[string]string `json:"links"`
}

// RenderURLs writes URL rows. json, csv and markdown have their own shape;
// every other style prints an aligned text table.
func RenderURLs(w io.Writer, style string, rows []URLRow, opt Options) error {
	if style == "json" {
		if rows == nil {
			rows = []URLRow{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	}
	names := linkNames(rows)
	table := [][]string{append([]string{"project", "app", "env", "url"}, names...)}
	for _, r := range rows {
		line := []string{r.Project, r.App, r.Env, r.URL}
		for _, name := range names {
			line = append(line, r.Links[name])
		}
		table = append(table, line)
	}
	switch style {
	case "csv":
		out := csv.NewWriter(w)
		if err := out.WriteAll(table); err != nil {
			return err
		}
		return out.Error()
	case "markdown":
		var b strings.Builder
		mdRow(&b, table[0]...)
		mdRow(&b, strings.Split(strings.Repeat("---,", len(table[0])-1)+"---", ",")...)
		for _, line := range table[1:] {
			mdRow(&b, line...)
		}
		_, err := io.WriteString(w, b.String())
		return err
	}
	cells := [][]cell{}
	for i, line := range table {
		if i == 0 {
			for j := range line {
				line[j] = strings.ToUpper(line[j])
			}
			cells = append(cells, header(line...))
			continue
		}
		cells = append(cells, plain(line...))
	}
	return grid(w, "", cells, opt)
}

func linkNames(rows []URLRow) []string {
	seen := map[string]bool{}
	var names []string
	for _, r := range rows {
		for name := range r.Links {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}
