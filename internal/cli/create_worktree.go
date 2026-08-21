package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func runCreateWorktree(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("create").FindSubcommand("worktree")
	help := RenderHelp(cmd)

	flags := createCommonFlags{wait: createWaitDefault}
	base := ""
	agent := ""
	runCmd := ""
	skipPerms := false
	noLaunch := false
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isHelp(arg) {
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		}
		next, handled, code := applyCreateCommonFlag(arg, args, i, help, env.Stderr, &flags)
		if handled {
			if code != 0 {
				return code
			}
			i = next
			continue
		}
		switch {
		case arg == "--base" || strings.HasPrefix(arg, "--base="):
			val, next, ok := takeFlagArg(arg, args, i, "--base")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--base requires a ref\n\n%s", help)
				return 2
			}
			base = val
			i = next
		case arg == "--agent" || strings.HasPrefix(arg, "--agent="):
			val, next, ok := takeFlagArg(arg, args, i, "--agent")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--agent requires an agent type\n\n%s", help)
				return 2
			}
			agent = val
			i = next
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			val, next, ok := takeFlagArg(arg, args, i, "--run")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--run requires a command\n\n%s", help)
				return 2
			}
			runCmd = val
			i = next
		case arg == "--skip-permissions":
			skipPerms = true
		case arg == "--no-launch":
			noLaunch = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}

	if flags.splitSet {
		return refuseCreateSplit(env)
	}
	if len(positional) != 1 {
		cliErrf(env.Stderr, "create worktree requires exactly one name\n\n%s", help)
		return 2
	}
	if noLaunch && (agent != "" || runCmd != "") {
		cliErrf(env.Stderr, "--no-launch cannot be combined with --agent or --run\n\n%s", help)
		return 2
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	dest, err := resolveCreateDestination(ctx, env.StateDir, flags.shellFlag, flags.projectFlag)
	if err != nil {
		cliErrln(env.Stderr, err)
		return createDestExitCode(err)
	}
	proj, err := registeredProjectForCreate(env.StateDir, dest)
	if err != nil {
		cliErrln(env.Stderr, err)
		return createDestExitCode(err)
	}
	if proj.Path == "" {
		cliErrln(env.Stderr, "no Sidecar project is registered for this directory; pass --project or run from a registered project")
		return 2
	}

	cfg := loadCreateConfig()
	setup := cfg.WorktreeSetupForProject(proj.Path)
	dirPrefix := cfg.Plugins.Workspace.DirPrefix
	workDir := proj.Path
	if dest.Origin.WorkDir != "" {
		workDir = dest.Origin.WorkDir
	}

	plan, err := workspaceops.ResolveWorktreePlan(ctx, workDir, proj.Path, positional[0], base, dirPrefix, setup)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	if repoKey, keyErr := workspaceops.RepoKeyForPath(ctx, proj.Path); keyErr == nil {
		plan.RepoKey = repoKey
	} else {
		plan.RepoKey = workspaceops.StablePathKey(proj.Path)
	}
	plan.OperationID = fmt.Sprintf("cli-%d", time.Now().UnixNano())
	plan.AgentType = agent
	plan.SkipPerms = skipPerms

	record, err := workspaceops.ExecuteWorktree(ctx, plan.RepoKey, plan)
	if record == nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	outcomes := make([]workspaceops.SetupOutcome, 0)
	if journalErr := workspaceops.PersistPendingCreation(ctx, plan, record); journalErr != nil {
		outcomes = append(outcomes, workspaceops.SetupOutcome{Kind: "journal", Action: "persist recovery", Required: true, Err: journalErr})
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	outcomes = append(outcomes, workspaceops.PersistWorktreeIdentity(ctx, plan)...)
	outcomes = append(outcomes, workspaceops.RunConfiguredSetup(ctx, plan)...)

	requiredFailed := setupOutcomesFailed(outcomes, true)
	if len(requiredFailed) == 0 {
		if journalErr := workspaceops.RemovePendingCreation(plan); journalErr != nil {
			outcomes = append(outcomes, workspaceops.SetupOutcome{Kind: "journal", Action: "finalize pending creation", Required: true, Err: journalErr})
			requiredFailed = setupOutcomesFailed(outcomes, true)
		}
	}

	session := workspaceops.WorktreeSessionName(record.Path, record.Name)
	var launchErr error
	if !noLaunch && len(requiredFailed) == 0 {
		configured := map[string]string(nil)
		if cfg != nil {
			configured = cfg.Plugins.Workspace.AgentStart
		}
		startAgent := agent != "" || runCmd != ""
		command := ""
		if agent != "" {
			command = workspaceops.ResolveAgentCommand(record.Path, agent, configured, skipPerms)
		} else if runCmd != "" {
			command = runCmd
		}
		_, launchErr = workspaceops.LaunchWorktreeSession(ctx, workspaceops.AgentLaunchSpec{
			SessionName:  session,
			WorkDir:      record.Path,
			AgentCommand: command,
			Env:          workspaceops.BuildEnvOverrides(plan.MainWorktree),
			StartAgent:   startAgent,
		})
		if launchErr == nil && agent != "" && runCmd != "" {
			launchErr = workspaceops.StartAgentInShell(ctx, session, runCmd)
		}
	}

	focus := true
	payload := uirequest.CreatePayload{
		Kind:        uirequest.CreateKindWorktree,
		Session:     session,
		DisplayName: record.Name,
		Focus:       &focus,
		Path:        record.Path,
		Branch:      record.Branch,
	}
	dest.Origin.ProjectKey = proj.Key
	if dest.Origin.WorkDir == "" {
		dest.Origin.WorkDir = proj.Path
	}
	req, reqErr := writeCreateRequest(env, dest, payload, uirequest.Target{
		Kind:  uirequest.TargetKindWorktree,
		Value: record.Path,
	}, uirequest.Options{})
	if reqErr != nil {
		cliErrln(env.Stderr, reqErr)
		return 1
	}
	acks := pollCreateAcks(env.StateDir, req.ID, req.Action, flags.wait)

	result := createWorktreeResult{
		Shell: createShellInfo{
			DisplayName: record.Name,
			Session:     session,
			WorkDir:     record.Path,
		},
		Path:      record.Path,
		Branch:    record.Branch,
		Setup:     encodeSetupOutcomes(outcomes),
		Acked:     len(acks) > 0,
		Surface:   createAckSurface(acks),
		Placement: createPlacementWorkspace,
	}

	failed := requiredFailed
	if flags.jsonOutput {
		if encErr := json.NewEncoder(env.Stdout).Encode(result); encErr != nil {
			cliErrln(env.Stderr, encErr)
			return 1
		}
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Created worktree %q (%s) on %s.\n", record.Name, record.Path, record.Branch)
	}
	if len(failed) > 0 {
		cliErrln(env.Stderr, summarizeSetupOutcomes(failed))
		return 1
	}
	if launchErr != nil {
		cliErrln(env.Stderr, launchErr)
		return 1
	}
	return 0
}

func loadCreateConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return config.Default()
	}
	return cfg
}

type createSetupOutcome struct {
	Kind     string `json:"kind"`
	Action   string `json:"action"`
	Required bool   `json:"required"`
	Error    string `json:"error,omitempty"`
}

type createWorktreeResult struct {
	Shell     createShellInfo      `json:"shell"`
	Path      string               `json:"path"`
	Branch    string               `json:"branch"`
	Setup     []createSetupOutcome `json:"setup"`
	Acked     bool                 `json:"acked"`
	Surface   string               `json:"surface,omitempty"`
	Placement string               `json:"placement"`
}

func encodeSetupOutcomes(outcomes []workspaceops.SetupOutcome) []createSetupOutcome {
	out := make([]createSetupOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		item := createSetupOutcome{Kind: o.Kind, Action: o.Action, Required: o.Required}
		if o.Err != nil {
			item.Error = o.Err.Error()
		}
		out = append(out, item)
	}
	return out
}

func setupOutcomesFailed(outcomes []workspaceops.SetupOutcome, requiredOnly bool) []workspaceops.SetupOutcome {
	var failed []workspaceops.SetupOutcome
	for _, outcome := range outcomes {
		if outcome.Err != nil && (!requiredOnly || outcome.Required) {
			failed = append(failed, outcome)
		}
	}
	return failed
}

func summarizeSetupOutcomes(outcomes []workspaceops.SetupOutcome) string {
	parts := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			parts = append(parts, outcome.Action+": "+outcome.Err.Error())
		}
	}
	return strings.Join(parts, "; ")
}
