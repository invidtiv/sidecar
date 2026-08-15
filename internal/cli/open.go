package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

func runOpen(env Env, args []string) int {
	openCmd := RootCommand().FindSubcommand("open")
	openHelp := RenderHelp(openCmd)

	jsonOutput := false
	splitMode := "auto"
	waitDuration := 1200 * time.Millisecond
	lineNo := 0
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, openHelp)
			return 0
		case arg == "--json":
			jsonOutput = true
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
			if splitMode != "auto" && splitMode != "right" && splitMode != "below" {
				cliErrf(env.Stderr, "invalid split option %q (must be auto, right, or below)\n\n%s", args[i], openHelp)
				return 2
			}
		case strings.HasPrefix(arg, "--split="):
			splitMode = strings.ToLower(strings.TrimPrefix(arg, "--split="))
			if splitMode != "auto" && splitMode != "right" && splitMode != "below" {
				cliErrf(env.Stderr, "invalid split option %q (must be auto, right, or below)\n\n%s", splitMode, openHelp)
				return 2
			}
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

	if len(positional) != 1 {
		cliErrf(env.Stderr, "open requires exactly one target (path or td-xxxxxx)\n\n%s", openHelp)
		return 2
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	identity, err := currentShellIdentity(ctx)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 3
	}

	originInfo, err := shellstate.LookupOrigin(env.StateDir, shellstate.Identity{
		TmuxName:  identity.session,
		Namespace: identity.socket,
	})
	if err != nil {
		cliErrln(env.Stderr, err)
		return 3
	}

	target, err := uirequest.ResolveTarget(originInfo.WorkDir, positional[0], lineNo)
	if err != nil {
		cliErrf(env.Stderr, "validation error: %v\n\n%s", err, openHelp)
		return 2
	}

	req := uirequest.Request{
		Version:   1,
		ID:        uirequest.NewRequestID(),
		CreatedAt: time.Now().UTC(),
		TTLMs:     int(uirequest.DefaultTTL / time.Millisecond),
		Origin: uirequest.Origin{
			TmuxSession: identity.session,
			Namespace:   identity.socket,
			ProjectKey:  originInfo.ProjectKey,
			WorkDir:     originInfo.WorkDir,
			PID:         os.Getpid(),
		},
		Action: uirequest.ActionOpen,
		Target: target,
		Options: uirequest.Options{
			Split: splitMode,
			Focus: true,
		},
	}

	_, err = uirequest.WriteRequest(env.StateDir, req)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	if waitDuration <= 0 {
		// Fire-and-forget
		if jsonOutput {
			res := uirequest.Result{
				Action:    req.Action,
				Target:    req.Target,
				Shell:     originInfo.TmuxName,
				Name:      originInfo.DisplayName,
				Delivered: 0,
				Results:   nil,
			}
			_ = json.NewEncoder(env.Stdout).Encode(res)
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
			res := uirequest.Result{
				Action:    req.Action,
				Target:    req.Target,
				Shell:     originInfo.TmuxName,
				Name:      originInfo.DisplayName,
				Delivered: 0,
				Results:   nil,
			}
			_ = json.NewEncoder(env.Stdout).Encode(res)
		}
		cliErrf(env.Stderr, "no running Sidecar instance is showing this shell (%s)\n", originInfo.TmuxName)
		return 3
	}

	hasDeclined := false
	var declineReason string
	hasOpened := false
	for _, ack := range acks {
		if ack.Status == uirequest.StatusDeclined {
			hasDeclined = true
			if ack.Reason != "" {
				declineReason = ack.Reason
			}
		}
		if ack.Status == uirequest.StatusOpened || ack.Status == uirequest.StatusRetargeted {
			hasOpened = true
		}
	}

	if hasDeclined && !hasOpened {
		if jsonOutput {
			res := uirequest.Result{
				Action:    req.Action,
				Target:    req.Target,
				Shell:     originInfo.TmuxName,
				Name:      originInfo.DisplayName,
				Delivered: len(acks),
				Results:   acks,
			}
			_ = json.NewEncoder(env.Stdout).Encode(res)
		}
		if declineReason == "" {
			declineReason = "the window is too small to split"
		}
		cliErrf(env.Stderr, "instance declined open request: %s\n", declineReason)
		return 4
	}

	if jsonOutput {
		res := uirequest.Result{
			Action:    req.Action,
			Target:    req.Target,
			Shell:     originInfo.TmuxName,
			Name:      originInfo.DisplayName,
			Delivered: len(acks),
			Results:   acks,
		}
		if err := json.NewEncoder(env.Stdout).Encode(res); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}

	if hasOpened {
		_, _ = fmt.Fprintf(env.Stdout, "Opened %s in a split beside %q.\n", target.Value, originInfo.DisplayName)
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Queued %s for %q; it opens when the user selects that shell.\n", target.Value, originInfo.DisplayName)
	}
	return 0
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
