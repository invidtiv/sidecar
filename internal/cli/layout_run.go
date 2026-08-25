package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
)

// runLayout is the shared runner behind layout get/apply: one uirequest, one
// ack wait, exit codes exactly like open's (0 applied, 2 usage, 3 no
// instance, 4 declined with the reason verbatim). Layout requests never
// queue, so a queued ack is never expected and reads as a decline.
func runLayout(env Env, mode string, panes []uirequest.LayoutPane, jsonOutput bool, shellFlag, projectFlag string, waitDuration time.Duration) int {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	dest, err := resolveOpenDestination(ctx, env.StateDir, shellFlag, projectFlag)
	if err != nil {
		cliErrln(env.Stderr, err)
		return destExitCode(err)
	}

	payload, err := json.Marshal(uirequest.LayoutPayload{Mode: mode, Panes: panes})
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	req := uirequest.Request{
		Version:   1,
		ID:        uirequest.NewRequestID(),
		CreatedAt: time.Now().UTC(),
		TTLMs:     int(uirequest.DefaultTTL / time.Millisecond),
		Origin:    dest.Origin,
		Action:    uirequest.ActionLayout,
		Payload:   payload,
	}
	if _, err := uirequest.WriteRequest(env.StateDir, req); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	var acks []uirequest.Ack
	if waitDuration > 0 {
		deadline := time.Now().Add(waitDuration)
		for time.Now().Before(deadline) {
			found, err := uirequest.ReadAcks(env.StateDir, req.ID, req.Action)
			if err == nil && len(found) > 0 {
				acks = found
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	_ = uirequest.Cleanup(env.StateDir, req.ID, req.Action)

	if len(acks) == 0 {
		if dest.Origin.TmuxSession != "" {
			cliErrf(env.Stderr, "no running Sidecar instance is showing this shell (%s)\n", dest.Origin.TmuxSession)
		} else {
			cliErrf(env.Stderr, "no running Sidecar instance is showing this project (%s)\n", dest.Origin.ProjectKey)
		}
		return 3
	}

	hasDeclined := false
	reason := ""
	hasOpened := false
	for _, ack := range acks {
		switch ack.Status {
		case uirequest.StatusDeclined:
			hasDeclined = true
			if ack.Reason != "" && reason == "" {
				reason = ack.Reason
			}
		case uirequest.StatusOpened, uirequest.StatusRetargeted:
			hasOpened = true
		}
	}

	if hasDeclined && !hasOpened {
		if jsonOutput && mode == uirequest.LayoutModeApply {
			printLayoutResult(env, mode, dest, acks)
		}
		if reason == "" {
			reason = "the request was declined"
		}
		cliErrf(env.Stderr, "%s\n", reason)
		return 4
	}

	if mode == uirequest.LayoutModeGet {
		return emitLayoutGet(env, dest, acks, jsonOutput)
	}
	printLayoutResult(env, mode, dest, acks)
	return 0
}

// takeLayoutCommonFlag consumes --shell/--project/--wait, which both layout
// subcommands share. next < 0 means the argument was none of these.
func takeLayoutCommonFlag(arg string, args []string, i int, help string, env Env, shellFlag, projectFlag *string, waitDuration *time.Duration) (next, code int) {
	needValue := func(name string) (string, bool) {
		value, nextArg, ok := takeFlagArg(arg, args, i, name)
		if !ok || value == "" {
			cliErrf(env.Stderr, "%s requires an argument\n\n%s", name, help)
			return "", false
		}
		next = nextArg
		return value, true
	}
	switch {
	case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
		value, ok := needValue("--shell")
		if !ok {
			return -1, 2
		}
		*shellFlag = value
	case arg == "--project" || strings.HasPrefix(arg, "--project="):
		value, ok := needValue("--project")
		if !ok {
			return -1, 2
		}
		*projectFlag = value
	case arg == "--wait" || strings.HasPrefix(arg, "--wait="):
		value, ok := needValue("--wait")
		if !ok {
			return -1, 2
		}
		d, err := parseWaitDuration(value)
		if err != nil {
			cliErrf(env.Stderr, "invalid wait duration %q: %v\n\n%s", value, err, help)
			return -1, 2
		}
		*waitDuration = d
	default:
		return -1, 0
	}
	return next, 0
}

func runLayoutGet(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("layout").FindSubcommand("get")
	help := RenderHelp(cmd)

	jsonOutput := false
	waitDuration := 1200 * time.Millisecond
	shellFlag, projectFlag := "", ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isHelp(arg) {
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		}
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		next, code := takeLayoutCommonFlag(arg, args, i, help, env, &shellFlag, &projectFlag, &waitDuration)
		if code != 0 {
			return code
		}
		if next >= 0 {
			i = next
			continue
		}
		cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
		return 2
	}
	return runLayout(env, uirequest.LayoutModeGet, nil, jsonOutput, shellFlag, projectFlag, waitDuration)
}

func runLayoutApply(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("layout").FindSubcommand("apply")
	help := RenderHelp(cmd)

	jsonOutput := false
	waitDuration := 1200 * time.Millisecond
	shellFlag, projectFlag := "", ""
	var panes []uirequest.LayoutPane

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isHelp(arg) {
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		}
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		if arg == "--pane" || strings.HasPrefix(arg, "--pane=") {
			value, nextArg, ok := takeFlagArg(arg, args, i, "--pane")
			if !ok || value == "" {
				cliErrf(env.Stderr, "--pane requires a JSON descriptor\n\n%s", help)
				return 2
			}
			pane, code, msg := layoutPaneFlag(value)
			if code != 0 {
				cliErrf(env.Stderr, "%s\n\n%s", msg, help)
				return code
			}
			panes = append(panes, pane)
			i = nextArg
			continue
		}
		next, code := takeLayoutCommonFlag(arg, args, i, help, env, &shellFlag, &projectFlag, &waitDuration)
		if code != 0 {
			return code
		}
		if next >= 0 {
			i = next
			continue
		}
		cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
		return 2
	}

	if len(panes) == 0 {
		cliErrf(env.Stderr, "layout apply needs at least one --pane descriptor\n\n%s", help)
		return 2
	}
	return runLayout(env, uirequest.LayoutModeApply, panes, jsonOutput, shellFlag, projectFlag, waitDuration)
}

// emitLayoutGet answers get: --json is the payload itself, unchanged; human
// output is the sketch plus the table.
func emitLayoutGet(env Env, dest openDestination, acks []uirequest.Ack, jsonOutput bool) int {
	layout := firstLayoutPayload(acks)
	if jsonOutput {
		if layout == nil {
			cliErrln(env.Stderr, "the instance answered without a layout payload")
			return 1
		}
		_, _ = env.Stdout.Write(layout)
		_, _ = fmt.Fprintln(env.Stdout)
		return 0
	}
	report := decodeLayoutReport(layout)
	label := dest.DisplayName
	if label == "" {
		label = dest.Origin.ProjectKey
	}
	_, _ = fmt.Fprintf(env.Stdout, "Layout beside %q (%s):\n\n", label, report.Surface)
	_, _ = fmt.Fprint(env.Stdout, renderLayoutSketch(report))
	_, _ = fmt.Fprint(env.Stdout, renderLayoutTable(report))
	return 0
}

// emitApplyResult prints one structured result for apply, then the human
// per-pane lines.
func printLayoutResult(env Env, mode string, dest openDestination, acks []uirequest.Ack) {
	status := string(uirequest.StatusOpened)
	reason := ""
	items := []uirequest.AckItem{}
	for _, ack := range acks {
		status = string(ack.Status)
		if ack.Reason != "" && reason == "" {
			reason = ack.Reason
		}
		if len(ack.Items) > 0 {
			items = ack.Items
		}
	}
	result := struct {
		Action    string              `json:"action"`
		Mode      string              `json:"mode"`
		Shell     string              `json:"shell"`
		Name      string              `json:"name"`
		Project   string              `json:"project"`
		Resolved  string              `json:"resolved"`
		Delivered int                 `json:"delivered"`
		Status    string              `json:"status"`
		Reason    string              `json:"reason,omitempty"`
		Items     []uirequest.AckItem `json:"items,omitempty"`
		Results   []uirequest.Ack     `json:"results"`
	}{
		Action:    "layout",
		Mode:      mode,
		Shell:     dest.Origin.TmuxSession,
		Name:      dest.DisplayName,
		Project:   dest.Origin.ProjectKey,
		Resolved:  dest.Resolved,
		Delivered: len(acks),
		Status:    status,
		Reason:    reason,
		Items:     items,
		Results:   acks,
	}
	_ = json.NewEncoder(env.Stdout).Encode(result)

	if mode != uirequest.LayoutModeApply {
		return
	}
	for _, item := range items {
		switch item.Verdict {
		case uirequest.ItemVerdictOpened:
			_, _ = fmt.Fprintf(env.Stdout, "opened pane at %s (index %d)\n", cellOrDash(item.Cell), item.Index)
		case uirequest.ItemVerdictRetargeted:
			_, _ = fmt.Fprintf(env.Stdout, "retargeted the pane already showing that content at %s (index %d)\n", cellOrDash(item.Cell), item.Index)
		default:
			_, _ = fmt.Fprintf(env.Stdout, "declined index %d: %s\n", item.Index, item.Reason)
		}
	}
}

func firstLayoutPayload(acks []uirequest.Ack) json.RawMessage {
	for _, ack := range acks {
		if len(ack.Layout) > 0 {
			return ack.Layout
		}
	}
	return nil
}
