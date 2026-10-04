package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/marcus/sidecar/internal/uiapi"
)

func workspaceCommand() *Command {
	list := &Command{
		Name: "list", Summary: "Read a project's ordered workspace and recoverable shells",
		Usage: "sidecar workspace list --project NAME [--host ID] [--sort MODE] [--search TEXT] [--json]",
		Long:  "Use the configured owning project key or path. The JSON resource contains project metadata, the shared ordered catalog and all durable shell records, including recoverable forgotten shells. Filters apply to the catalog; shell records remain complete.",
		Flags: []Flag{
			{Name: "--project", Arg: "NAME", Summary: "Configured owning project (required)"},
			{Name: "--host", Arg: "ID", Summary: "Owning remote host"},
			{Name: "--sort", Arg: "MODE", Summary: "activity, project, recent or name"},
			{Name: "--search", Arg: "TEXT", Summary: "Shared workspace search"},
			{Name: "--provider", Arg: "ID", Summary: "Filter provider (repeatable)"},
			{Name: "--state", Arg: "STATE", Summary: "Filter state (repeatable)"},
			{Name: "--show-idle-sessions", Arg: "BOOL", Summary: "Include idle worktrees"},
			{Name: "--json", Bool: true, Summary: "Write the shared workspace resource"},
			{Name: "--help", Short: "-h", Bool: true, Summary: "Show this help"},
		},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "workspace or owning host unavailable"}, {Code: 2, Summary: "usage or query error"}},
		Examples:  []Example{{Command: "sidecar workspace list --project sidecar --json"}, {Command: "sidecar workspace list --project sidecar --sort name --search review"}},
		Run:       runWorkspaceList,
		Agent:     AgentDoc{Invocation: "sidecar workspace list --project NAME --json", Summary: "Read worktrees, shells, agent state and forgotten records"},
	}
	return &Command{Name: "workspace", Summary: "Query project workspaces", Usage: "sidecar workspace list", Sub: []*Command{list}, Run: func(env Env, args []string) int {
		if len(args) > 0 && args[0] == "list" {
			return runWorkspaceList(env, args[1:])
		}
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(list))
		if len(args) != 0 && !isHelp(args[0]) {
			return 2
		}
		return 0
	}}
}
func runWorkspaceList(env Env, args []string) int {
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(workspaceCommand().Sub[0]))
		return 0
	}
	flags, err := parseAPIFlags(args, []string{"--json"}, []string{"--project", "--host", "--sort", "--search", "--show-idle-sessions"}, "--provider", "--state")
	if err != nil || flags.values["--project"] == "" {
		cliErrln(env.Stderr, "workspace list requires --project and valid query flags")
		return 2
	}
	values := url.Values{}
	for key, list := range flags.repeated {
		values[key[2:]] = list
	}
	for key, v := range flags.values {
		if key != "--project" && key != "--host" {
			values.Set(key[2:], v)
		}
	}
	q, err := uiapi.ParseCatalogQuery(values)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	backend := &mobileBackend{env: env}
	result, err := backend.Workspace(ctx, flags.values["--project"], flags.values["--host"], q)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if flags.bools["--json"] {
		return writeJSON(env, result)
	}
	for _, section := range result.Catalog.Sections {
		if section.Title != "" {
			_, _ = fmt.Fprintln(env.Stdout, section.Title)
		}
		for _, row := range section.Rows {
			_, _ = fmt.Fprintf(env.Stdout, "%s  %s  %s\n", row.DisplayName, row.WorkspaceKind, row.Status)
		}
	}
	return 0
}
