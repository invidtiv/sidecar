package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/marcus/sidecar/internal/config"
)

// RootCommand returns the root command hierarchy.
func RootCommand() *Command {
	root := &Command{
		Name:    "sidecar",
		Summary: "A TUI dashboard for AI coding agents. When run without a command, starts the interactive TUI.",
		Usage:   "sidecar <command> [options]",
	}

	helpCmd := &Command{
		Name:    "help",
		Summary: "Show help for commands or emit JSON command metadata",
		Usage:   "sidecar help [--json] [<command>]",
		Long:    "Show help for Sidecar commands, or emit the full machine-readable command tree.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write the command tree as JSON to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: -1, Description: "Optional command path to inspect"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 2, Summary: "unknown command"},
		},
		Examples: []Example{
			{Command: "sidecar help"},
			{Command: "sidecar help open"},
			{Command: "sidecar help --json"},
		},
		Run: runHelpCommand,
	}

	nameCmd := &Command{
		Name:    "name",
		Summary: "Print the current shell's display name",
		Usage:   "sidecar shell name [--json]",
		Long: "Print the Sidecar display name of the managed shell or worktree agent containing\n" +
			"this command. Reads registered Sidecar state (authoritative), not the agent SDK\n" +
			"or $SIDECAR_SHELL_NAME, so reopening another agent in place keeps its context.\n\n" +
			"Human output is the display name alone, one line, for easy scripting.\n" +
			"JSON includes the stable tmux session id and display name.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 1, Summary: "identity or state failure"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar shell name"},
			{Command: "sidecar shell name --json"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar shell name",
			Summary:    "Read the name this shell shows the user",
		},
		Run: runShellName,
	}

	renameCmd := &Command{
		Name:    "rename",
		Summary: "Rename the current shell's display name",
		Usage:   "sidecar shell rename [--json] <display-name>",
		Long: "Rename only the Sidecar-managed shell or worktree agent containing this command.\n" +
			"This changes Sidecar's display name; it does not rename the tmux session, Git\n" +
			"branch, or worktree directory.\n\n" +
			"The current display name is also published as $SIDECAR_SHELL_NAME. \"Shell 3\"\n" +
			"is the unset default; a previous task's name is equally stale — rename when\n" +
			"the name no longer describes the work in this shell.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 1, Summary: "identity or state failure"},
			{Code: 2, Summary: "usage or validation error"},
		},
		Examples: []Example{
			{Command: "sidecar shell rename \"shell rename implementation\""},
		},
		Agent: AgentDoc{
			Invocation: "sidecar shell rename \"<short context>\"",
			Summary:    "Keep the shell's name describing the work you are doing now",
		},
		Run: runShellRename,
	}

	shellCmd := &Command{
		Name:    "shell",
		Summary: "Manage the current Sidecar shell context",
		Usage:   "sidecar shell <command>",
		Long:    "Manage the current Sidecar-managed shell or worktree agent context.",
		Sub:     []*Command{nameCmd, renameCmd},
		Run:     runShellRoot,
	}

	openCmd := &Command{
		Name:    "open",
		Summary: "Show a file, a td issue, or a git diff in a split pane",
		Usage:   "sidecar open [options] [<target>]",
		Long: "Show a file, a td issue, or a git diff to the user as a split pane in a Sidecar workspace.\n" +
			"From a Sidecar shell this targets that shell. Otherwise it targets the unique running\n" +
			"instance, or a specific --shell / --project. --diff with no spec is the working tree.\n" +
			"--split only overrides the split axis; it never halves a live terminal after content is open.",
		Targets: []TargetDoc{
			{Target: "path", Summary: "A file inside the target workspace, optionally \"path:line\""},
			{Target: "td-xxxxxx", Summary: "A td issue id"},
			{Target: "--diff", Summary: "Working-tree diff (wt); add a spec for a commit or range"},
			{Target: "spec", Summary: "A git commit or range (abc1234, A..B); --diff accepts HEAD and branch names"},
		},
		Flags: []Flag{
			{Name: "--line", Arg: "N", Summary: "Line to reveal (alternative to \"path:line\")"},
			{Name: "--diff", Summary: "Open a Diff leaf (working tree if no spec)", Bool: true},
			{Name: "--shell", Arg: "NAME", Summary: "Target a registered shell by display name or tmux name"},
			{Name: "--project", Arg: "NAME", Summary: "Target a project's Workspaces surface (slug, basename, or path)"},
			{Name: "--split", Arg: "auto|right|below", Summary: "Where to place a new pane (default auto)"},
			{Name: "--wait", Arg: "DURATION", Summary: "Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)"},
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: 1, Description: "File, td-xxxxxx, or git spec; omitted with --diff for the working tree"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "opened or queued"},
			{Code: 1, Summary: "state failure"},
			{Code: 2, Summary: "usage or validation error"},
			{Code: 3, Summary: "no running instance, or several running with no target"},
			{Code: 4, Summary: "an instance declined (e.g. the window is too small to split)"},
		},
		Examples: []Example{
			{Command: "sidecar open internal/cli/cli.go", Description: "file, in a split beside the terminal"},
			{Command: "sidecar open internal/cli/cli.go:88", Description: "file at a line"},
			{Command: "sidecar open td-348d88", Description: "td issue"},
			{Command: "sidecar open --diff", Description: "working-tree Diff leaf"},
			{Command: "sidecar open --diff HEAD", Description: "that commit, not the working tree"},
			{Command: "sidecar open abc1234", Description: "commit, unless a file of that name exists"},
			{Command: "sidecar open --json --split below README.md", Description: "structured result for the agent"},
			{Command: "sidecar open --project sidecar README.md", Description: "from any terminal, that project's Workspaces surface"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar open <path>[:line] | td-xxxxxx | --diff [spec]",
			Summary:    "Put a file, a td issue, or a git diff in front of the user",
		},
		Run: runOpen,
	}

	agentsCmd := &Command{
		Name:    "agents",
		Summary: "List what an agent can do from Sidecar",
		Usage:   "sidecar --agents",
		Long: "List the Sidecar commands worth reaching for, one line each.\n" +
			"Also spelled \"sidecar --agents\".",
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
		},
		Examples: []Example{
			{Command: "sidecar --agents"},
		},
		Run: func(env Env, _ []string) int {
			_, _ = fmt.Fprint(env.Stdout, RenderAgents(RootCommand()))
			return 0
		},
	}

	root.Sub = []*Command{agentsCmd, helpCmd, openCmd, shellCmd}
	return root
}

func runHelpCommand(env Env, args []string) int {
	jsonOutput := false
	var path []string
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		} else if arg == "-h" || arg == "--help" {
			// Show help for help itself
			_, _ = fmt.Fprint(env.Stdout, RenderHelp(RootCommand().FindSubcommand("help")))
			return 0
		} else if strings.HasPrefix(arg, "-") {
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, RenderHelp(RootCommand().FindSubcommand("help")))
			return 2
		} else {
			path = append(path, arg)
		}
	}

	root := RootCommand()
	if len(path) == 0 {
		if jsonOutput {
			if err := RenderJSON(env.Stdout, root); err != nil {
				cliErrln(env.Stderr, err)
				return 1
			}
			return 0
		}
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(root))
		return 0
	}

	curr := root
	for _, segment := range path {
		sub := curr.FindSubcommand(segment)
		if sub == nil {
			cliErrf(env.Stderr, "unknown command %q\n\n%s", strings.Join(path, " "), RenderHelp(curr))
			return 2
		}
		curr = sub
	}

	if jsonOutput {
		if err := RenderJSON(env.Stdout, curr); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprint(env.Stdout, RenderHelp(curr))
	return 0
}

func runShellRoot(env Env, args []string) int {
	shellCmd := RootCommand().FindSubcommand("shell")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(shellCmd))
		return 0
	}
	sub := shellCmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown shell command %q\n\n%s", args[0], RenderHelp(shellCmd))
	return 2
}

func defaultEnv(stdout, stderr io.Writer) Env {
	return Env{
		Stdout:   stdout,
		Stderr:   stderr,
		StateDir: config.StateDir(),
		Ctx:      context.Background(),
	}
}
