package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

const createWaitDefault = 1200 * time.Millisecond

const createSplitWorktreeUnsupported = "--split is not supported for create worktree"

const createSplitNeedsShell = "--split requires a current Sidecar shell; run this from a managed shell or pass --shell"

const createPlacementWorkspace = "workspace"

func runCreateRoot(env Env, args []string) int {
	createCmd := RootCommand().FindSubcommand("create")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(createCmd))
		return 0
	}
	sub := createCmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown create command %q\n\n%s", args[0], RenderHelp(createCmd))
	return 2
}

type createCommonFlags struct {
	jsonOutput  bool
	wait        time.Duration
	shellFlag   string
	projectFlag string
	splitSet    bool
	splitMode   string
}

func takeFlagArg(arg string, args []string, i int, name string) (value string, next int, ok bool) {
	if strings.HasPrefix(arg, name+"=") {
		return strings.TrimPrefix(arg, name+"="), i, true
	}
	if arg != name {
		return "", i, false
	}
	if i+1 >= len(args) {
		return "", i, false
	}
	return args[i+1], i + 1, true
}

func applyCreateCommonFlag(arg string, args []string, i int, help string, stderr io.Writer, flags *createCommonFlags) (next int, handled bool, code int) {
	switch {
	case arg == "--json":
		flags.jsonOutput = true
		return i, true, 0
	case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
		val, next, ok := takeFlagArg(arg, args, i, "--shell")
		if !ok || val == "" {
			cliErrf(stderr, "--shell requires a shell name\n\n%s", help)
			return i, true, 2
		}
		flags.shellFlag = val
		return next, true, 0
	case arg == "--project" || strings.HasPrefix(arg, "--project="):
		val, next, ok := takeFlagArg(arg, args, i, "--project")
		if !ok || val == "" {
			cliErrf(stderr, "--project requires a project name\n\n%s", help)
			return i, true, 2
		}
		flags.projectFlag = val
		return next, true, 0
	case arg == "--wait" || strings.HasPrefix(arg, "--wait="):
		val, next, ok := takeFlagArg(arg, args, i, "--wait")
		if !ok {
			cliErrf(stderr, "--wait requires a duration argument\n\n%s", help)
			return i, true, 2
		}
		d, err := parseWaitDuration(val)
		if err != nil {
			cliErrf(stderr, "invalid wait duration %q: %v\n\n%s", val, err, help)
			return i, true, 2
		}
		flags.wait = d
		return next, true, 0
	case arg == "--split" || strings.HasPrefix(arg, "--split="):
		mode := "auto"
		next = i
		if strings.HasPrefix(arg, "--split=") {
			mode = strings.ToLower(strings.TrimPrefix(arg, "--split="))
			if mode == "" {
				mode = "auto"
			}
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			next = i + 1
			mode = strings.ToLower(args[next])
		}
		if mode != "auto" && mode != "right" && mode != "below" {
			cliErrf(stderr, "invalid split option %q (must be auto, right, or below)\n\n%s", mode, help)
			return i, true, 2
		}
		flags.splitSet = true
		flags.splitMode = mode
		return next, true, 0
	default:
		return i, false, 0
	}
}

func refuseCreateSplit(env Env) int {
	cliErrln(env.Stderr, createSplitWorktreeUnsupported)
	return 2
}

func writeCreateRequest(env Env, dest openDestination, payload uirequest.CreatePayload, target uirequest.Target, opts uirequest.Options) (uirequest.Request, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return uirequest.Request{}, err
	}
	req := uirequest.Request{
		Version:   1,
		ID:        uirequest.NewRequestID(),
		CreatedAt: time.Now().UTC(),
		TTLMs:     int(uirequest.DefaultTTL / time.Millisecond),
		Origin:    dest.Origin,
		Action:    uirequest.ActionCreate,
		Target:    target,
		Options:   opts,
		Payload:   data,
	}
	_, err = uirequest.WriteRequest(env.StateDir, req)
	return req, err
}

func pollCreateAcks(stateDir, id string, action uirequest.Action, wait time.Duration) []uirequest.Ack {
	if wait <= 0 {
		return nil
	}
	deadline := time.Now().Add(wait)
	var acks []uirequest.Ack
	for time.Now().Before(deadline) {
		found, err := uirequest.ReadAcks(stateDir, id, action)
		if err == nil && len(found) > 0 {
			acks = found
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	_ = uirequest.Cleanup(stateDir, id, action)
	return acks
}

func createAckSurface(acks []uirequest.Ack) string {
	for _, ack := range acks {
		if ack.Surface != "" {
			return ack.Surface
		}
	}
	return ""
}

func createAckSession(acks []uirequest.Ack) string {
	for _, ack := range acks {
		if ack.Status != uirequest.StatusOpened && ack.Status != uirequest.StatusRetargeted {
			continue
		}
		if session, ok := strings.CutPrefix(ack.Surface, "shell:"); ok && session != "" {
			return session
		}
	}
	return ""
}

func createAcksOpened(acks []uirequest.Ack) bool {
	for _, ack := range acks {
		if ack.Status == uirequest.StatusOpened || ack.Status == uirequest.StatusRetargeted {
			return true
		}
	}
	return false
}

func createAcksDeclinedReason(acks []uirequest.Ack) string {
	for _, ack := range acks {
		if ack.Status == uirequest.StatusDeclined && ack.Reason != "" {
			return ack.Reason
		}
	}
	for _, ack := range acks {
		if ack.Status == uirequest.StatusDeclined {
			return ""
		}
	}
	return ""
}

func createAcksAllDeclined(acks []uirequest.Ack) bool {
	if len(acks) == 0 {
		return false
	}
	for _, ack := range acks {
		if ack.Status != uirequest.StatusDeclined {
			return false
		}
	}
	return true
}

func existingShellDefinitions(proj registeredProject) []shellstate.Definition {
	if proj.Dir == "" {
		return proj.Shells
	}
	listed, err := shellstate.ListAtPath(filepath.Join(proj.Dir, "shells.json"))
	if err != nil {
		return proj.Shells
	}
	return listed
}
