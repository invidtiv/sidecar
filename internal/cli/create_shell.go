package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func runCreateShell(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("create").FindSubcommand("shell")
	help := RenderHelp(cmd)

	flags := createCommonFlags{wait: createWaitDefault}
	nameFlag := ""
	agentFlag := ""
	runCmd := ""
	typeCmd := ""
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
		case arg == "--name" || strings.HasPrefix(arg, "--name="):
			val, next, ok := takeFlagArg(arg, args, i, "--name")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--name requires a display name\n\n%s", help)
				return 2
			}
			nameFlag = val
			i = next
		case arg == "--agent" || strings.HasPrefix(arg, "--agent="):
			val, next, ok := takeFlagArg(arg, args, i, "--agent")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--agent requires an agent type\n\n%s", help)
				return 2
			}
			agentFlag = val
			i = next
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			val, next, ok := takeFlagArg(arg, args, i, "--run")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--run requires a command\n\n%s", help)
				return 2
			}
			runCmd = val
			i = next
		case arg == "--type" || strings.HasPrefix(arg, "--type="):
			val, next, ok := takeFlagArg(arg, args, i, "--type")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--type requires a command\n\n%s", help)
				return 2
			}
			typeCmd = val
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 0 {
		cliErrf(env.Stderr, "create shell takes no positional arguments\n\n%s", help)
		return 2
	}
	if runCmd != "" && typeCmd != "" {
		cliErrf(env.Stderr, "--run and --type are mutually exclusive\n\n%s", help)
		return 2
	}
	// The agent family is checked before anything is created, and against the
	// families this Sidecar can actually launch. Resolution does not refuse —
	// it falls back to Claude's command for a name it does not know — so
	// `--agent claud` would otherwise create a shell recorded as "claud" with
	// Claude running in it, and every surface keyed on the type would disagree
	// with the pane. See workspaceops.KnownAgentType.
	if agentFlag = strings.TrimSpace(agentFlag); agentFlag != "" {
		configured := loadCreateConfig().Plugins.Workspace.AgentStart
		if !workspaceops.KnownAgentType(agentFlag, configured) {
			cliErrf(env.Stderr, "unknown agent type %q; known types are %s\n",
				agentFlag, strings.Join(workspaceops.KnownAgentTypes(configured), ", "))
			return exitInputRejected
		}
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	dest, err := resolveCreateDestination(ctx, env.StateDir, flags.shellFlag, flags.projectFlag, registerProject)
	if err != nil {
		cliErrln(env.Stderr, err)
		return createDestExitCode(err)
	}
	if flags.splitSet && flags.tab {
		cliErrf(env.Stderr, "--split and --tab name different placements\n\n%s", help)
		return 2
	}
	// --agent writes a field of a workspace shell's durable record, and a
	// beside-the-session split adds no such record ("do not add a workspace
	// row", below). Refused rather than accepted and dropped: a caller that
	// asked for the durable agent type and silently did not get one is exactly
	// the defect this flag exists to fix.
	if agentFlag != "" && flags.splitSet {
		cliErrf(env.Stderr, "--agent records a workspace shell's agent type, and a beside-the-session split adds no workspace row\n\n%s", help)
		return 2
	}
	// A shell asked for from inside a Sidecar shell opens beside that session
	// by default; switching the whole workspace to the new shell is what --tab
	// asks for explicitly (nt-7c82c9). Outside any Sidecar shell there is
	// nothing to split beside, so workspace placement is the only option.
	if dest.Origin.TmuxSession == "" && !flags.tab {
		if identity, idErr := currentShellIdentity(ctx); idErr == nil {
			dest.Origin.TmuxSession = identity.session
			if dest.Origin.WorkDir == "" {
				dest.Origin.WorkDir = identity.path
			}
		}
	}
	// --agent implies workspace placement for the same reason it is refused
	// with --split: it names a field only a workspace row has. It is not
	// spelled --tab because the caller did not ask where the shell goes; it
	// asked for something only one placement can carry.
	if flags.splitSet || (!flags.tab && agentFlag == "" && dest.Origin.TmuxSession != "") {
		if dest.Origin.TmuxSession == "" {
			cliErrf(env.Stderr, "%s\n\n%s", createSplitNeedsShell, help)
			return 2
		}
		if !flags.splitSet {
			flags.splitMode = "auto"
		}
		code := runCreateShellSplit(env, dest, flags, nameFlag, runCmd, typeCmd)
		// Beside-the-session was only the default, not something the caller
		// asked for: a DECLINE (feature off, no room, no live instance) falls
		// back to a workspace shell so the command still lands.
		//
		// Only a decline. A verdict about the command itself is final wherever
		// placement would have put it, and retrying it can only produce the
		// same refusal twice — which is exactly what exit 5 did when it was
		// missing from this list: `--name <too long>` printed its refusal, then
		// "created a workspace shell instead", then the refusal again, having
		// created nothing. Listed by what falls through rather than by what
		// does not, so the next new code is final by default.
		declined := code == 3 || code == 4
		if !declined || flags.splitSet {
			return code
		}
		cliErrf(env.Stderr, "no beside-the-session placement available; created a workspace shell instead\n")
	}

	return runCreateShellWorkspace(env, dest, flags, nameFlag, agentFlag, runCmd, typeCmd)
}

func runCreateShellWorkspace(env Env, dest openDestination, flags createCommonFlags, nameFlag, agentFlag, runCmd, typeCmd string) int {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
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

	display, session := workspaceops.ShellNames(proj.Path, existingShellDefinitions(proj))
	if custom := strings.TrimSpace(nameFlag); custom != "" {
		var err error
		display, err = shellstate.NormalizeName(custom)
		if err != nil {
			cliErrln(env.Stderr, err)
			return exitInputRejected
		}
	}

	spec := workspaceops.ManagedShellSpec{
		ShellSpec: workspaceops.ShellSpec{
			WorkDir:     proj.Path,
			SessionName: session,
			DisplayName: display,
		},
		ProjectRoot: proj.Path,
		// The durable statement of which agent family this shell is for, in the
		// same field the TUI's Create Shell writes. Starting the process is a
		// separate concern (--run, or `sidecar shell send --run` afterwards):
		// this is the backstop that keeps the shell on the Activity board while
		// the agent is still booting and whenever live screen identification
		// momentarily fails — which is the whole reason a remote agent shell
		// used to go missing where its local twin did not.
		AgentType: agentFlag,
	}
	if _, err := workspaceops.CreateManagedShell(spec); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	var seedErr error
	if runCmd != "" {
		seedErr = workspaceops.StartAgentInShell(ctx, session, runCmd)
	} else if typeCmd != "" {
		seedErr = workspaceops.TypeInShell(ctx, session, typeCmd)
	}

	focus := true
	payload := uirequest.CreatePayload{
		Kind:        uirequest.CreateKindShell,
		Session:     session,
		DisplayName: display,
		Focus:       &focus,
	}
	dest.Origin.ProjectKey = proj.Key
	if dest.Origin.WorkDir == "" {
		dest.Origin.WorkDir = proj.Path
	}
	req, reqErr := writeCreateRequest(env, dest, payload, uirequest.Target{
		Kind:  uirequest.TargetKindShell,
		Value: session,
	}, uirequest.Options{})
	if reqErr != nil {
		cliErrln(env.Stderr, reqErr)
		if seedErr != nil {
			cliErrln(env.Stderr, seedErr)
		}
		return 1
	}

	acks := pollCreateAcks(env.StateDir, req.ID, req.Action, flags.wait)
	result := createShellResult{
		Shell: createShellInfo{
			DisplayName: display,
			Session:     session,
			WorkDir:     proj.Path,
		},
		Acked:     len(acks) > 0,
		Surface:   createAckSurface(acks),
		Placement: createPlacementWorkspace,
	}

	if flags.jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			if seedErr != nil {
				return 1
			}
			return 1
		}
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Created shell %q (%s).\n", display, session)
	}
	if seedErr != nil {
		cliErrln(env.Stderr, seedErr)
		return 1
	}
	return 0
}

func runCreateShellSplit(env Env, dest openDestination, flags createCommonFlags, nameFlag, runCmd, typeCmd string) int {
	display := strings.TrimSpace(nameFlag)
	if display != "" {
		normalized, err := shellstate.NormalizeName(display)
		if err != nil {
			cliErrln(env.Stderr, err)
			return exitInputRejected
		}
		display = normalized
	}

	workDir := dest.Origin.WorkDir
	if proj, err := registeredProjectForCreate(env.StateDir, dest); err == nil && proj.Path != "" {
		dest.Origin.ProjectKey = proj.Key
		if workDir == "" {
			workDir = proj.Path
		}
		if dest.Origin.WorkDir == "" {
			dest.Origin.WorkDir = proj.Path
		}
	}

	focus := true
	payload := uirequest.CreatePayload{
		Kind:        uirequest.CreateKindShell,
		DisplayName: display,
		Focus:       &focus,
		Run:         runCmd,
		Type:        typeCmd,
	}
	req, reqErr := writeCreateRequest(env, dest, payload, uirequest.Target{
		Kind:  uirequest.TargetKindShell,
		Value: dest.Origin.TmuxSession,
	}, uirequest.Options{Split: flags.splitMode})
	if reqErr != nil {
		cliErrln(env.Stderr, reqErr)
		return 1
	}

	result := createShellResult{
		Shell: createShellInfo{
			DisplayName: display,
			WorkDir:     workDir,
		},
		Placement: flags.splitMode,
	}

	emit := func() {
		if flags.jsonOutput {
			if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
				cliErrln(env.Stderr, err)
			}
			return
		}
		if result.Shell.Session != "" {
			_, _ = fmt.Fprintf(env.Stdout, "Created split %q (%s).\n", display, result.Shell.Session)
			return
		}
		_, _ = fmt.Fprintf(env.Stdout, "Sent split request for %q.\n", display)
	}

	if flags.wait <= 0 {
		emit()
		return 0
	}

	acks := pollCreateAcks(env.StateDir, req.ID, req.Action, flags.wait)
	result.Acked = len(acks) > 0
	result.Surface = createAckSurface(acks)
	if session := createAckSession(acks); session != "" {
		result.Shell.Session = session
	}

	if createAcksOpened(acks) {
		emit()
		return 0
	}
	emit()
	if createAcksAllDeclined(acks) {
		reason := createAcksDeclinedReason(acks)
		if reason == "" {
			reason = "the window is too small to split"
		}
		cliErrln(env.Stderr, reason)
		return 4
	}
	if dest.Origin.TmuxSession != "" {
		cliErrf(env.Stderr, "no running Sidecar instance is showing this shell (%s)\n", dest.Origin.TmuxSession)
	} else {
		cliErrf(env.Stderr, "no running Sidecar instance is showing this project (%s)\n", dest.Origin.ProjectKey)
	}
	return 3
}

type createShellInfo struct {
	DisplayName string `json:"displayName"`
	Session     string `json:"session"`
	WorkDir     string `json:"workDir"`
}

type createShellResult struct {
	Shell     createShellInfo `json:"shell"`
	Acked     bool            `json:"acked"`
	Surface   string          `json:"surface,omitempty"`
	Placement string          `json:"placement"`
}
