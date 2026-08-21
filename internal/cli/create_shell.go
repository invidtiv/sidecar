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
	if flags.splitSet {
		return refuseCreateSplit(env)
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

	display, session := workspaceops.ShellNames(proj.Path, existingShellDefinitions(proj))
	if custom := strings.TrimSpace(nameFlag); custom != "" {
		display, err = shellstate.NormalizeName(custom)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 2
		}
	}

	spec := workspaceops.ManagedShellSpec{
		ShellSpec: workspaceops.ShellSpec{
			WorkDir:     proj.Path,
			SessionName: session,
			DisplayName: display,
		},
		ProjectRoot: proj.Path,
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
	})
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
