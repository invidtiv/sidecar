package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	shellStatusLive             = "live"
	shellStatusForgotten        = "forgotten"
	shellStatusAlreadyForgotten = "already_forgotten"
	shellStatusRestored         = "restored"
	shellStatusAlreadyLive      = "already_live"
)

type shellRecordFlags struct {
	jsonOutput  bool
	shellFlag   string
	projectFlag string
	positional  []string
}

type shellListResult struct {
	Shells []shellListItem `json:"shells"`
}

type shellListItem struct {
	Shell     string     `json:"shell"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	AgentType string     `json:"agentType,omitempty"`
	SkipPerms bool       `json:"skipPerms,omitempty"`
	WorkDir   string     `json:"workDir,omitempty"`
	Status    string     `json:"status"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

type shellRecordResult struct {
	Shell  string `json:"shell"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
}

func runShellList(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("shell").FindSubcommand("list")
	help := RenderHelp(cmd)
	flags, code := parseShellRecordArgs(args, help, env, 0)
	if code >= 0 {
		return code
	}

	_, path, code := resolveShellRecordsProject(env, flags, help, resolveProjectOnly)
	if code != 0 {
		return code
	}

	live, err := shellstate.ListAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	tombs, err := shellstate.ListTombstonesAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	if flags.jsonOutput {
		items := make([]shellListItem, 0, len(live)+len(tombs))
		for _, def := range live {
			items = append(items, listItemFromDefinition(def, shellStatusLive, nil))
		}
		for _, stone := range tombs {
			deleted := stone.DeletedAt
			items = append(items, listItemFromDefinition(stone.Definition, shellStatusForgotten, &deleted))
		}
		return writeShellJSON(env, shellListResult{Shells: items})
	}

	if len(live) == 0 && len(tombs) == 0 {
		if _, err := fmt.Fprintln(env.Stdout, "No shells."); err != nil {
			return 1
		}
		return 0
	}
	for _, def := range live {
		if _, err := fmt.Fprintf(env.Stdout, "%s  %s\n", def.TmuxName, def.DisplayName); err != nil {
			return 1
		}
	}
	if len(tombs) == 0 {
		return 0
	}
	// Forgotten records are listed here too, not only under --json: the tmux
	// name is the only argument `sidecar shell restore` takes, and a human who
	// cannot see it has no way to reach the command.
	if len(live) > 0 {
		if _, err := fmt.Fprintln(env.Stdout); err != nil {
			return 1
		}
	}
	if _, err := fmt.Fprintln(env.Stdout, "Forgotten (restore with: sidecar shell restore <tmux-name>)"); err != nil {
		return 1
	}
	for _, stone := range tombs {
		if _, err := fmt.Fprintf(env.Stdout, "%s  %s\n", stone.TmuxName, stone.DisplayName); err != nil {
			return 1
		}
	}
	return 0
}

func runShellForget(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("shell").FindSubcommand("forget")
	help := RenderHelp(cmd)
	flags, code := parseShellRecordArgs(args, help, env, 1)
	if code >= 0 {
		return code
	}
	tmuxName := flags.positional[0]

	proj, path, code := resolveShellRecordsProject(env, flags, help, registerProject)
	if code != 0 {
		return code
	}

	live, err := shellstate.ListAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if def, ok := findDefinition(live, tmuxName); ok {
		if err := workspaceops.ForgetManagedShell(proj.Path, def.TmuxName, def.Namespace, time.Time{}); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if flags.jsonOutput {
			return writeShellJSON(env, shellRecordResult{Shell: def.TmuxName, Name: def.DisplayName, Status: shellStatusForgotten})
		}
		if _, err := fmt.Fprintf(env.Stdout, "Forgot shell record %q.\n", def.TmuxName); err != nil {
			return 1
		}
		return 0
	}

	tombs, err := shellstate.ListTombstonesAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if stone, ok := findTombstone(tombs, tmuxName); ok {
		if flags.jsonOutput {
			return writeShellJSON(env, shellRecordResult{Shell: stone.TmuxName, Name: stone.DisplayName, Status: shellStatusAlreadyForgotten})
		}
		if _, err := fmt.Fprintf(env.Stdout, "Shell record %q is already forgotten.\n", stone.TmuxName); err != nil {
			return 1
		}
		return 0
	}

	cliErrf(env.Stderr, "no shell record named %q in this project\n", tmuxName)
	return 1
}

func runShellRestore(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("shell").FindSubcommand("restore")
	help := RenderHelp(cmd)
	flags, code := parseShellRecordArgs(args, help, env, 1)
	if code >= 0 {
		return code
	}
	tmuxName := flags.positional[0]

	proj, path, code := resolveShellRecordsProject(env, flags, help, registerProject)
	if code != 0 {
		return code
	}

	live, err := shellstate.ListAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if def, ok := findDefinition(live, tmuxName); ok {
		if flags.jsonOutput {
			return writeShellJSON(env, shellRecordResult{Shell: def.TmuxName, Name: def.DisplayName, Status: shellStatusAlreadyLive})
		}
		if _, err := fmt.Fprintf(env.Stdout, "Shell record %q is already live.\n", def.TmuxName); err != nil {
			return 1
		}
		return 0
	}

	tombs, err := shellstate.ListTombstonesAtPath(path)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	stone, ok := findTombstone(tombs, tmuxName)
	if !ok {
		cliErrf(env.Stderr, "no forgotten shell record named %q in this project\n", tmuxName)
		return 1
	}

	got, err := workspaceops.RestoreManagedShell(proj.Path, stone.TmuxName, stone.Namespace)
	if err != nil {
		if shellstate.IsAlready(err) {
			if flags.jsonOutput {
				return writeShellJSON(env, shellRecordResult{Shell: stone.TmuxName, Name: stone.DisplayName, Status: shellStatusAlreadyLive})
			}
			if _, err := fmt.Fprintf(env.Stdout, "Shell record %q is already live.\n", stone.TmuxName); err != nil {
				return 1
			}
			return 0
		}
		if shellstate.IsNotFound(err) {
			cliErrf(env.Stderr, "no forgotten shell record named %q in this project\n", tmuxName)
			return 1
		}
		cliErrln(env.Stderr, err)
		return 1
	}
	if flags.jsonOutput {
		return writeShellJSON(env, shellRecordResult{Shell: got.TmuxName, Name: got.DisplayName, Status: shellStatusRestored})
	}
	if _, err := fmt.Fprintf(env.Stdout, "Restored shell record %q as %q.\n", got.TmuxName, got.DisplayName); err != nil {
		return 1
	}
	return 0
}

func parseShellRecordArgs(args []string, help string, env Env, wantPositional int) (shellRecordFlags, int) {
	var flags shellRecordFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isHelp(arg):
			if _, err := fmt.Fprint(env.Stdout, help); err != nil {
				return flags, 1
			}
			return flags, 0
		case arg == "--json":
			flags.jsonOutput = true
		case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
			val, next, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", help)
				return flags, 2
			}
			flags.shellFlag = val
			i = next
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			val, next, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", help)
				return flags, 2
			}
			flags.projectFlag = val
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return flags, 2
			}
			flags.positional = append(flags.positional, arg)
		}
	}
	if len(flags.positional) != wantPositional {
		switch wantPositional {
		case 0:
			cliErrf(env.Stderr, "shell list takes no positional arguments\n\n%s", help)
		default:
			cliErrf(env.Stderr, "requires exactly one tmux session name\n\n%s", help)
		}
		return flags, 2
	}
	if wantPositional == 1 && strings.TrimSpace(flags.positional[0]) == "" {
		cliErrf(env.Stderr, "requires exactly one tmux session name\n\n%s", help)
		return flags, 2
	}
	return flags, -1
}

// resolveShellRecordsProject finds the project a shell-records verb addresses
// and the manifest it should read or write.
//
// register is the caller's answer to "am I about to write": a read resolves a
// configured-but-never-opened project without creating its state directory, and
// gets an empty manifest path — which is the truth, since Sidecar owns no
// records for a project nobody has opened here. A writer asks for the directory
// and gets one.
func resolveShellRecordsProject(env Env, flags shellRecordFlags, help string, register projectRegistration) (registeredProject, string, int) {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	dest, err := resolveCreateDestination(ctx, env.StateDir, flags.shellFlag, flags.projectFlag, register)
	if err != nil {
		cliErrln(env.Stderr, err)
		return registeredProject{}, "", createDestExitCode(err)
	}
	proj, err := registeredProjectForCreate(env.StateDir, dest)
	if err != nil {
		cliErrln(env.Stderr, err)
		return registeredProject{}, "", createDestExitCode(err)
	}
	if proj.Path == "" {
		cliErrf(env.Stderr, "%s\n\n%s", unregisteredCreateProject, help)
		return registeredProject{}, "", 2
	}
	if proj.Dir == "" {
		if register == registerProject {
			// A writer with nowhere to write is a failure, not an empty answer.
			cliErrf(env.Stderr, "%s\n\n%s", unregisteredCreateProject, help)
			return registeredProject{}, "", 2
		}
		// shellstate.ListAtPath treats a manifest that is not there as no
		// records, which is exactly what this project has.
		return proj, "", 0
	}
	return proj, filepath.Join(proj.Dir, "shells.json"), 0
}

func listItemFromDefinition(def shellstate.Definition, status string, deletedAt *time.Time) shellListItem {
	item := shellListItem{
		Shell:     def.TmuxName,
		Name:      def.DisplayName,
		Namespace: def.Namespace,
		AgentType: def.AgentType,
		SkipPerms: def.SkipPerms,
		WorkDir:   def.WorkDir,
		Status:    status,
	}
	if deletedAt != nil && !deletedAt.IsZero() {
		item.DeletedAt = deletedAt
	}
	return item
}

func findDefinition(defs []shellstate.Definition, tmuxName string) (shellstate.Definition, bool) {
	for _, def := range defs {
		if def.TmuxName == tmuxName {
			return def, true
		}
	}
	return shellstate.Definition{}, false
}

func findTombstone(tombs []shellstate.Tombstone, tmuxName string) (shellstate.Tombstone, bool) {
	for _, stone := range tombs {
		if stone.TmuxName == tmuxName {
			return stone, true
		}
	}
	return shellstate.Tombstone{}, false
}

func writeShellJSON(env Env, v any) int {
	if err := json.NewEncoder(env.Stdout).Encode(v); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
