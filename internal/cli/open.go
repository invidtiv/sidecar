package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

func runOpen(env Env, args []string) int {
	openCmd := RootCommand().FindSubcommand("open")
	openHelp := RenderHelp(openCmd)

	jsonOutput := false
	wantDiff := false
	splitMode := "auto"
	splitSet := false
	atCell := ""
	waitDuration := 1200 * time.Millisecond
	lineNo := 0
	shellFlag := ""
	projectFlag := ""
	providerFlag := ""
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, openHelp)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--diff":
			wantDiff = true
		case arg == "--provider":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--provider requires a provider instance id\n\n%s", openHelp)
				return 2
			}
			i++
			providerFlag = args[i]
			if providerFlag == "" {
				cliErrf(env.Stderr, "--provider requires a provider instance id\n\n%s", openHelp)
				return 2
			}
		case strings.HasPrefix(arg, "--provider="):
			providerFlag = strings.TrimPrefix(arg, "--provider=")
			if providerFlag == "" {
				cliErrf(env.Stderr, "--provider requires a provider instance id\n\n%s", openHelp)
				return 2
			}
		case arg == "--shell":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", openHelp)
				return 2
			}
			i++
			shellFlag = args[i]
			if shellFlag == "" {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", openHelp)
				return 2
			}
		case strings.HasPrefix(arg, "--shell="):
			shellFlag = strings.TrimPrefix(arg, "--shell=")
			if shellFlag == "" {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", openHelp)
				return 2
			}
		case arg == "--project":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", openHelp)
				return 2
			}
			i++
			projectFlag = args[i]
			if projectFlag == "" {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", openHelp)
				return 2
			}
		case strings.HasPrefix(arg, "--project="):
			projectFlag = strings.TrimPrefix(arg, "--project=")
			if projectFlag == "" {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", openHelp)
				return 2
			}
		case arg == "--line":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--line requires a line number argument\n\n%s", openHelp)
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				cliErrf(env.Stderr, "invalid line number %q\n\n%s", args[i], openHelp)
				return 2
			}
			lineNo = n
		case strings.HasPrefix(arg, "--line="):
			val := strings.TrimPrefix(arg, "--line=")
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				cliErrf(env.Stderr, "invalid line number %q\n\n%s", val, openHelp)
				return 2
			}
			lineNo = n
		case arg == "--split":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--split requires an argument (auto|right|below)\n\n%s", openHelp)
				return 2
			}
			i++
			splitMode = strings.ToLower(args[i])
			splitSet = true
			if splitMode != "auto" && splitMode != "right" && splitMode != "below" {
				cliErrf(env.Stderr, "invalid split option %q (must be auto, right, or below)\n\n%s", args[i], openHelp)
				return 2
			}
		case strings.HasPrefix(arg, "--split="):
			splitMode = strings.ToLower(strings.TrimPrefix(arg, "--split="))
			splitSet = true
			if splitMode != "auto" && splitMode != "right" && splitMode != "below" {
				cliErrf(env.Stderr, "invalid split option %q (must be auto, right, or below)\n\n%s", splitMode, openHelp)
				return 2
			}
		case arg == "--at":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--at requires a grid cell argument (col or col.row)\n\n%s", openHelp)
				return 2
			}
			i++
			atCell = args[i]
		case strings.HasPrefix(arg, "--at="):
			atCell = strings.TrimPrefix(arg, "--at=")
		case arg == "--wait":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--wait requires a duration argument\n\n%s", openHelp)
				return 2
			}
			i++
			d, err := parseWaitDuration(args[i])
			if err != nil {
				cliErrf(env.Stderr, "invalid wait duration %q: %v\n\n%s", args[i], err, openHelp)
				return 2
			}
			waitDuration = d
		case strings.HasPrefix(arg, "--wait="):
			val := strings.TrimPrefix(arg, "--wait=")
			d, err := parseWaitDuration(val)
			if err != nil {
				cliErrf(env.Stderr, "invalid wait duration %q: %v\n\n%s", val, err, openHelp)
				return 2
			}
			waitDuration = d
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, openHelp)
				return 2
			}
			positional = append(positional, arg)
		}
	}

	if providerFlag != "" && wantDiff {
		cliErrf(env.Stderr, "--provider and --diff name different kinds of target\n\n%s", openHelp)
		return 2
	}
	if providerFlag != "" && lineNo > 0 {
		cliErrf(env.Stderr, "--line does not apply to a provider resource\n\n%s", openHelp)
		return 2
	}

	atCell = strings.TrimSpace(atCell)
	if atCell != "" {
		if splitSet {
			cliErrf(env.Stderr, "--at and --split are mutually exclusive: --at is a requirement (declines rather than land elsewhere), --split a preference\n\n%s", openHelp)
			return 2
		}
		if _, ok := panelayout.ParseCell(atCell); !ok {
			cliErrf(env.Stderr, "invalid cell %q for --at (use col or col.row, 1-based, like 2.1)\n\n%s", atCell, openHelp)
			return 2
		}
	}

	if wantDiff {
		if len(positional) > 1 {
			cliErrf(env.Stderr, "open accepts at most one target\n\n%s", openHelp)
			return 2
		}
	} else if len(positional) != 1 {
		cliErrf(env.Stderr, "open requires exactly one target (path, td-xxxxxx, or a git spec)\n\n%s", openHelp)
		return 2
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	dest, err := resolveOpenDestination(ctx, env.StateDir, shellFlag, projectFlag)
	if err != nil {
		cliErrln(env.Stderr, err)
		return destExitCode(err)
	}

	raw := ""
	if len(positional) == 1 {
		raw = positional[0]
	}
	if dest.Resolved != uirequest.ResolvedCurrentShell {
		if workDir := resolveTargetWorkDirForDest(env.StateDir, dest, raw); workDir != "" {
			dest.Origin.WorkDir = workDir
		}
	}
	target, err := uirequest.ResolveTarget(dest.Origin.WorkDir, raw, lineNo, uirequest.ResolveOptions{Diff: wantDiff, Provider: providerFlag})
	if err != nil {
		cliErrf(env.Stderr, "validation error: %v\n\n%s", err, openHelp)
		return 2
	}

	options := uirequest.Options{}
	if atCell != "" {
		// A cell replaces any axis preference: it is the whole placement.
		options.At = atCell
	} else if splitMode != "auto" || splitSet {
		options.Split = splitMode
	}
	req := uirequest.Request{
		Version:   1,
		ID:        uirequest.NewRequestID(),
		CreatedAt: time.Now().UTC(),
		TTLMs:     int(uirequest.DefaultTTL / time.Millisecond),
		Origin:    dest.Origin,
		Action:    uirequest.ActionOpen,
		Target:    target,
		Options:   options,
	}

	_, err = uirequest.WriteRequest(env.StateDir, req)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	if waitDuration <= 0 {
		// Fire-and-forget
		if jsonOutput {
			_ = json.NewEncoder(env.Stdout).Encode(openResult(req, dest, nil))
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "Sent open request for %s.\n", target.Value)
		}
		return 0
	}

	deadline := time.Now().Add(waitDuration)
	var acks []uirequest.Ack
	for time.Now().Before(deadline) {
		found, err := uirequest.ReadAcks(env.StateDir, req.ID, req.Action)
		if err == nil && len(found) > 0 {
			acks = found
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	_ = uirequest.Cleanup(env.StateDir, req.ID, req.Action)

	if len(acks) == 0 {
		if jsonOutput {
			_ = json.NewEncoder(env.Stdout).Encode(openResult(req, dest, nil))
		}
		if dest.Origin.TmuxSession != "" {
			cliErrf(env.Stderr, "no running Sidecar instance is showing this shell (%s)\n", dest.Origin.TmuxSession)
		} else {
			cliErrf(env.Stderr, "no running Sidecar instance is showing this project (%s)\n", dest.Origin.ProjectKey)
		}
		return 3
	}

	hasDeclined := false
	var declineReason string
	hasOpened := false
	allRetargeted := true
	for _, ack := range acks {
		if ack.Status == uirequest.StatusDeclined {
			hasDeclined = true
			if ack.Reason != "" {
				declineReason = ack.Reason
			}
		}
		if ack.Status == uirequest.StatusOpened || ack.Status == uirequest.StatusRetargeted {
			hasOpened = true
			if ack.Status != uirequest.StatusRetargeted {
				allRetargeted = false
			}
		}
	}

	if hasDeclined && !hasOpened {
		if jsonOutput {
			_ = json.NewEncoder(env.Stdout).Encode(openResult(req, dest, acks))
		}
		if declineReason == "" {
			declineReason = "the window is too small to split"
		}
		cliErrf(env.Stderr, "instance declined open request: %s\n", declineReason)
		return 4
	}

	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(openResult(req, dest, acks)); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}

	label := dest.DisplayName
	if label == "" {
		label = dest.Origin.ProjectKey
	}
	if hasOpened {
		if allRetargeted {
			_, _ = fmt.Fprintf(env.Stdout, "Opened %s in the split already beside %q.\n", target.Value, label)
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "Opened %s in a split beside %q.\n", target.Value, label)
		}
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Queued %s for %q; it opens when the user selects that shell.\n", target.Value, label)
	}
	return 0
}

func openResult(req uirequest.Request, dest openDestination, acks []uirequest.Ack) uirequest.Result {
	return uirequest.Result{
		Action:    req.Action,
		Target:    req.Target,
		Shell:     dest.Origin.TmuxSession,
		Name:      dest.DisplayName,
		Project:   dest.Origin.ProjectKey,
		Resolved:  dest.Resolved,
		Delivered: len(acks),
		Results:   acks,
	}
}

func parseWaitDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "0" || s == "0s" || s == "0ms" {
		return 0, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Millisecond, nil
	}
	return time.ParseDuration(s)
}
