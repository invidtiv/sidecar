package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
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
	tab         bool
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

// usageReporter answers a usage refusal in the shape the caller asked for and
// returns the exit code to leave with.
//
// A `create` verb used to print its reason and then the whole help text, and
// did so under --json too — so a script doing `| tail` saw help and never the
// reason (td-a658ed). With --json the refusal is the same envelope the agent
// verbs use, `{"error":{"code":"usage",...}}`, on stderr; without it, the
// reason and the help, as before. The flag is looked for before parsing
// starts, because the refusal may be about an argument that precedes it.
type usageReporter func(format string, a ...any) int

func newUsageReporter(env Env, jsonOutput bool, help string) usageReporter {
	return func(format string, a ...any) int {
		msg := fmt.Sprintf(format, a...)
		if jsonOutput {
			return emitAgentError(env, true, &agentcontrol.Error{Code: agentcontrol.ErrUsage, Message: msg})
		}
		cliErrf(env.Stderr, "%s\n\n%s", msg, help)
		return 2
	}
}

// wantsJSON is the pre-parse answer to "will this run report in JSON": it
// exists so a usage refusal raised while parsing can already be shaped.
func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" {
			return true
		}
	}
	return false
}

func applyCreateCommonFlag(arg string, args []string, i int, usage usageReporter, flags *createCommonFlags) (next int, handled bool, code int) {
	switch {
	case arg == "--json":
		flags.jsonOutput = true
		return i, true, 0
	case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
		val, next, ok := takeFlagArg(arg, args, i, "--shell")
		if !ok || val == "" {
			return i, true, usage("--shell requires a shell name")
		}
		flags.shellFlag = val
		return next, true, 0
	case arg == "--project" || strings.HasPrefix(arg, "--project="):
		val, next, ok := takeFlagArg(arg, args, i, "--project")
		if !ok || val == "" {
			return i, true, usage("--project requires a project name")
		}
		flags.projectFlag = val
		return next, true, 0
	case arg == "--wait" || strings.HasPrefix(arg, "--wait="):
		val, next, ok := takeFlagArg(arg, args, i, "--wait")
		if !ok {
			return i, true, usage("--wait requires a duration argument")
		}
		d, err := parseWaitDuration(val)
		if err != nil {
			return i, true, usage("invalid wait duration %q: %v", val, err)
		}
		flags.wait = d
		return next, true, 0
	case arg == "--tab":
		flags.tab = true
		return i, true, 0
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
			return i, true, usage("invalid split option %q (must be auto, right, or below)", mode)
		}
		flags.splitSet = true
		flags.splitMode = mode
		return next, true, 0
	default:
		return i, false, 0
	}
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
