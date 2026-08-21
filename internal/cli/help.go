package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// RenderHelp returns the formatted help string for a command.
func RenderHelp(cmd *Command) string {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "Usage: %s\n", cmd.Usage)

	if cmd.Long != "" {
		fmt.Fprintf(&buf, "\n%s\n", strings.TrimSpace(cmd.Long))
	} else if cmd.Summary != "" && len(cmd.Sub) > 0 {
		fmt.Fprintf(&buf, "\n%s\n", strings.TrimSpace(cmd.Summary))
	}

	if len(cmd.Targets) > 0 {
		buf.WriteString("\nTargets:\n")
		maxTarget := 0
		for _, t := range cmd.Targets {
			if len(t.Target) > maxTarget {
				maxTarget = len(t.Target)
			}
		}
		if maxTarget < 14 {
			maxTarget = 14
		}
		for _, t := range cmd.Targets {
			pad := strings.Repeat(" ", maxTarget-len(t.Target)+2)
			fmt.Fprintf(&buf, "  %s%s%s\n", t.Target, pad, t.Summary)
		}
	}

	if len(cmd.Sub) > 0 {
		buf.WriteString("\nCommands:\n")
		maxName := 0
		for _, sub := range cmd.Sub {
			if len(sub.Name) > maxName {
				maxName = len(sub.Name)
			}
		}
		for _, sub := range cmd.Sub {
			pad := strings.Repeat(" ", maxName-len(sub.Name)+4)
			fmt.Fprintf(&buf, "  %s%s%s\n", sub.Name, pad, sub.Summary)
		}
		switch cmd.Name {
		case "shell":
			buf.WriteString("\nRun \"sidecar shell <command> --help\" for command details.\n")
		case "", "sidecar":
			buf.WriteString("\nRun \"sidecar help <command>\" or \"sidecar <command> --help\" for command details.\n")
		default:
			fmt.Fprintf(&buf, "\nRun \"sidecar %s <command> --help\" for command details.\n", cmd.Name)
		}
		return buf.String()
	}

	if len(cmd.Examples) > 0 {
		if len(cmd.Examples) == 1 {
			buf.WriteString("\nExample:\n")
		} else {
			buf.WriteString("\nExamples:\n")
		}
		for _, ex := range cmd.Examples {
			if ex.Description != "" {
				fmt.Fprintf(&buf, "  # %s\n", ex.Description)
			}
			fmt.Fprintf(&buf, "  %s\n", ex.Command)
		}
	}

	if len(cmd.Flags) > 0 {
		buf.WriteString("\nOptions:\n")
		if cmd.Name == "name" || cmd.Name == "rename" {
			for _, f := range cmd.Flags {
				if f.Name == "--json" {
					buf.WriteString("  --json    " + f.Summary + "\n")
				} else if f.Name == "--help" || f.Short == "-h" {
					buf.WriteString("  -h, --help\n            " + f.Summary + "\n")
				} else {
					flagStr := f.Name
					if f.Short != "" {
						flagStr = f.Short + ", " + f.Name
					}
					pad := ""
					if len(flagStr) < 10 {
						pad = strings.Repeat(" ", 10-len(flagStr))
					}
					buf.WriteString("  " + flagStr + pad + f.Summary + "\n")
				}
			}
		} else {
			const targetCol = 18
			for _, f := range cmd.Flags {
				flagStr := f.Name
				if f.Short != "" {
					flagStr = f.Short + ", " + f.Name
				}
				if f.Arg != "" {
					flagStr += " " + f.Arg
				}

				if len(flagStr) >= (targetCol - 2) {
					// Put description on next line indented by targetCol
					fmt.Fprintf(&buf, "  %s\n", flagStr)
					fmt.Fprintf(&buf, "%s%s\n", strings.Repeat(" ", targetCol), f.Summary)
				} else {
					pad := strings.Repeat(" ", targetCol-2-len(flagStr))
					fmt.Fprintf(&buf, "  %s%s%s\n", flagStr, pad, f.Summary)
				}
			}
		}
	}

	if len(cmd.ExitCodes) > 0 {
		buf.WriteString("\nExit codes: ")
		var parts []string
		for _, ec := range cmd.ExitCodes {
			parts = append(parts, fmt.Sprintf("%d %s", ec.Code, ec.Summary))
		}
		exitText := strings.Join(parts, ", ") + "."
		if len(exitText) > 65 && len(parts) >= 4 && cmd.Name == "open" {
			fmt.Fprintf(&buf, "%s,\n            %s,\n            %s.\n",
				parts[0]+", "+parts[1], parts[2], strings.Join(parts[3:], ", "))
		} else {
			buf.WriteString(exitText + "\n")
		}
	}

	return buf.String()
}

// RenderJSON writes the JSON representation of a command tree to w.
func RenderJSON(w io.Writer, cmd *Command) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cmd)
}

// RenderMarkdownDoc generates reference markdown documentation for the CLI command tree.
func RenderMarkdownDoc(root *Command) string {
	var buf bytes.Buffer
	buf.WriteString("# Sidecar CLI Reference\n\n")
	buf.WriteString("Sidecar provides non-interactive commands for scripting and agent workflows.\n\n")

	for _, sub := range root.Sub {
		renderMarkdownCommand(&buf, sub, "sidecar", 2)
	}
	return buf.String()
}

func renderMarkdownCommand(buf *bytes.Buffer, cmd *Command, parentPath string, level int) {
	cmdPath := parentPath + " " + cmd.Name
	header := strings.Repeat("#", level)
	fmt.Fprintf(buf, "%s `%s`\n\n", header, cmdPath)
	if cmd.Summary != "" {
		fmt.Fprintf(buf, "%s\n\n", cmd.Summary)
	}
	if cmd.Long != "" {
		fmt.Fprintf(buf, "%s\n\n", cmd.Long)
	}
	fmt.Fprintf(buf, "```\nUsage: %s\n```\n\n", cmd.Usage)

	if len(cmd.Targets) > 0 {
		buf.WriteString("**Targets:**\n\n")
		for _, t := range cmd.Targets {
			fmt.Fprintf(buf, "- `%s`: %s\n", t.Target, t.Summary)
		}
		buf.WriteString("\n")
	}

	if len(cmd.Flags) > 0 {
		buf.WriteString("**Options:**\n\n")
		for _, f := range cmd.Flags {
			flagName := f.Name
			if f.Short != "" {
				flagName = f.Short + ", " + f.Name
			}
			if f.Arg != "" {
				flagName += " " + f.Arg
			}
			fmt.Fprintf(buf, "- `%s`: %s\n", flagName, f.Summary)
		}
		buf.WriteString("\n")
	}

	if len(cmd.ExitCodes) > 0 {
		buf.WriteString("**Exit codes:**\n\n")
		for _, ec := range cmd.ExitCodes {
			fmt.Fprintf(buf, "- `%d`: %s\n", ec.Code, ec.Summary)
		}
		buf.WriteString("\n")
	}

	if len(cmd.Examples) > 0 {
		buf.WriteString("**Examples:**\n\n```bash\n")
		for _, ex := range cmd.Examples {
			if ex.Description != "" {
				fmt.Fprintf(buf, "# %s\n", ex.Description)
			}
			fmt.Fprintf(buf, "%s\n", ex.Command)
		}
		buf.WriteString("```\n\n")
	}

	for _, sub := range cmd.Sub {
		renderMarkdownCommand(buf, sub, cmdPath, level+1)
	}
}

// RenderAgents lists what an agent can do from Sidecar.
// It is generated from the same registry the help and the reference doc come
// from, so a command that grows an agent-facing use appears here by declaring
// it, not by someone remembering to update a list.
func RenderAgents(root *Command) string {
	var buf strings.Builder
	buf.WriteString("Sidecar commands for agents. sidecar open works from any context; shell name and rename act on the shell you are running in.\n\n")

	var lines [][2]string
	var collect func(cmd *Command)
	collect = func(cmd *Command) {
		if cmd.Agent.Invocation != "" {
			lines = append(lines, [2]string{cmd.Agent.Invocation, cmd.Agent.Summary})
		}
		for _, sub := range cmd.Sub {
			collect(sub)
		}
	}
	collect(root)

	width := 0
	for _, line := range lines {
		if len(line[0]) > width {
			width = len(line[0])
		}
	}
	for _, line := range lines {
		fmt.Fprintf(&buf, "  %s%s  %s\n", line[0], strings.Repeat(" ", width-len(line[0])), line[1])
	}

	buf.WriteString("\nAdd --json to any of them for a structured result.\n")
	buf.WriteString("If td is installed, td note list / td note show find notes and sidecar open sidecar://note/<id> puts one in a pane.\n")
	buf.WriteString("Run \"sidecar help <command>\" for options and exit codes.\n")
	return buf.String()
}
