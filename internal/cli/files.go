package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filefind"
)

func filesCommand() *Command {
	find := &Command{Name: "find", Summary: "Find project files with the shared fuzzy matcher", Usage: "sidecar files find [QUERY] --project PROJECT [--workspace ID] [--recent PATH] [--limit N] [--json]", Long: "Search the configured project's files using the same ranking and ignore rules as the TUI and UI API. Empty query lists recent files first, then shallow paths. Recent hints are relative paths in most-recent-first order. Workspace selectors are passed unchanged from the catalog's content_workspace_id. Match positions are zero-based Unicode code point indices.", Flags: []Flag{
		{Name: "--project", Arg: "PROJECT", Summary: "Exact configured project name or key (required)"},
		{Name: "--workspace", Arg: "ID", Summary: "Optional project workspace selector"},
		{Name: "--recent", Arg: "PATH", Summary: "Recent relative file hint, repeat most recent first"},
		{Name: "--limit", Arg: "N", Summary: "Maximum results, 1 to 100 (default 50)"},
		{Name: "--json", Bool: true, Summary: "Write the structured search result"},
		{Name: "--help", Short: "-h", Bool: true, Summary: "Show this help"},
	}, Args: ArgSpec{Min: 0, Max: 1}, ExitCodes: []ExitCode{{Code: 0, Summary: "searched"}, {Code: 1, Summary: "internal failure"}, {Code: 2, Summary: "usage error"}, {Code: 5, Summary: "project, workspace or path rejected"}}, Examples: []Example{{Command: "sidecar files find readme --project sidecar --json"}}, Run: runFilesFind}
	cmd := &Command{Name: "files", Summary: "Search project files", Usage: "sidecar files <command>", Sub: []*Command{find}}
	cmd.Run = func(env Env, args []string) int {
		if len(args) == 0 || isHelp(args[0]) {
			_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
			return 0
		}
		if sub := cmd.FindSubcommand(args[0]); sub != nil {
			return sub.Run(env, args[1:])
		}
		cliErrf(env.Stderr, "unknown files command %q\n", args[0])
		return 2
	}
	return cmd
}

func runFilesFind(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("files").FindSubcommand("find"))
	var project, workspace string
	var p contentservice.FileSearchParams
	var jsonOutput, hasQuery, flagsEnded bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !flagsEnded && arg == "--" {
			flagsEnded = true
			continue
		}
		if flagsEnded {
			if hasQuery {
				cliErrf(env.Stderr, "files find takes one query\n")
				return 2
			}
			p.Query = arg
			hasQuery = true
			continue
		}
		if arg == "--help" || arg == "-h" {
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		}
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		flag := strings.SplitN(arg, "=", 2)[0]
		switch flag {
		case "--project", "--workspace", "--recent", "--limit":
			val, next, ok := takeFlagArg(arg, args, i, flag)
			if !ok || val == "" {
				cliErrf(env.Stderr, "%s requires a value\n", flag)
				return 2
			}
			i = next
			switch flag {
			case "--project":
				project = val
			case "--workspace":
				workspace = val
			case "--recent":
				p.Recent = append(p.Recent, val)
			case "--limit":
				n, err := strconv.Atoi(val)
				if err != nil || n < 1 || n > 100 {
					cliErrf(env.Stderr, "--limit must be between 1 and 100\n")
					return 2
				}
				p.Limit = n
			}
		default:
			if strings.HasPrefix(arg, "-") || hasQuery {
				cliErrf(env.Stderr, "unknown option or extra query %q\n", arg)
				return 2
			}
			p.Query = arg
			hasQuery = true
		}
	}
	if project == "" {
		cliErrf(env.Stderr, "--project is required\n\n%s", help)
		return 2
	}
	service := contentservice.Default()
	ws, err := service.LookupProject(contentCtx(env), project, workspace)
	if err != nil {
		return contentExit(env, err)
	}
	result, err := service.SearchFiles(contentCtx(env), &filefind.Index{}, ws.Root, p)
	if err != nil {
		return contentExit(env, err)
	}
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrf(env.Stderr, "%v\n", err)
			return 1
		}
	} else {
		for _, match := range result.Results {
			_, _ = fmt.Fprintln(env.Stdout, match.Path)
		}
		if result.Warning != "" {
			_, _ = fmt.Fprintln(env.Stderr, result.Warning)
		}
	}
	return 0
}
