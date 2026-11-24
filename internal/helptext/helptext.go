// Package helptext gives every command the same --help layout: usage, short
// examples, commands, flags, and a closing block of useful commands.
package helptext

import (
	"strings"

	"github.com/spf13/cobra"
)

// tipsKey is the annotation holding a command's closing block.
const tipsKey = "tips"

// template is cobra's default usage template plus the closing block.
const template = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Options:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global options:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Run "{{.CommandPath}} [command] --help" for more about a command.{{end}}{{with index .Annotations "` + tipsKey + `"}}

{{.}}{{end}}
`

// Apply sets the help layout on a root command; its subcommands inherit it.
func Apply(root *cobra.Command) {
	root.SetUsageTemplate(template)
}

// Block formats lines for Example or Tips: trimmed and indented by two
// spaces, so they can be written as an indented raw string.
func Block(text string) string {
	lines := strings.Split(strings.Trim(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// Tips sets the block printed at the bottom of a command's help: a title
// line, then the useful commands.
func Tips(cmd *cobra.Command, title, commands string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[tipsKey] = title + "\n" + Block(commands)
}
