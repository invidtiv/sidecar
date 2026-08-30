package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
)

// runLayout is the shared runner behind layout get/apply/move: one uirequest,
// one ack wait, exit codes exactly like open's (0 applied — which for a move
// includes an accepted no-op, 2 usage, 3 no instance, 4 declined with the
// reason verbatim). Layout requests never queue, so a queued ack is never
// expected and reads as a decline. The payload names its own mode and carries
// the batch's panes, the full-layout spec, or the move record.
type layoutDestFlags struct {
	shell, project, sessionsRow string
	sessions                    bool
}

func runLayout(env Env, payload uirequest.LayoutPayload, jsonOutput bool, destFlags layoutDestFlags, waitDuration time.Duration) int {
	mode := payload.Mode
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if destFlags.sessions && (destFlags.shell != "" || destFlags.project != "") {
		cliErrln(env.Stderr, "--sessions cannot be combined with --shell or --project")
		return 2
	}

	var dest openDestination
	var err error
	if destFlags.sessions {
		dest, err = resolveSessionsDestination(ctx, env.StateDir, destFlags.sessionsRow)
	} else {
		// Like open: layout writes a request onto the bus for a running
		// instance and needs no project state directory of its own, so it
		// resolves --project without creating one. Registering is for the
		// verbs that are about to write INTO a project's state.
		dest, err = resolveOpenDestination(ctx, env.StateDir, destFlags.shell, destFlags.project, resolveProjectOnly)
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return destExitCode(err)
	}

	encoded, err := json.Marshal(payload)
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
		Payload:   encoded,
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
		switch {
		case dest.Origin.Sessions:
			cliErrln(env.Stderr, "no running Sidecar instance is showing the Sessions surface")
		case dest.Origin.TmuxSession != "":
			cliErrf(env.Stderr, "no running Sidecar instance is showing this shell (%s)\n", dest.Origin.TmuxSession)
		default:
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
		case uirequest.StatusMoved, uirequest.StatusUnchanged:
			// Moved and already-there are both exit 0. A move that had nothing
			// to do is a satisfied request, not a refusal — the pane is where
			// the caller asked for it to be.
			hasOpened = true
		}
	}

	if hasDeclined && !hasOpened {
		if mode != uirequest.LayoutModeGet {
			printLayoutResult(env, mode, dest, acks, jsonOutput)
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
	printLayoutResult(env, mode, dest, acks, jsonOutput)
	return 0
}

// takeLayoutCommonFlag consumes --shell/--project/--sessions/--wait, which both
// layout subcommands share. next < 0 means the argument was none of these.
func takeLayoutCommonFlag(arg string, args []string, i int, help string, env Env, dest *layoutDestFlags, waitDuration *time.Duration) (next, code int) {
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
		dest.shell = value
	case arg == "--project" || strings.HasPrefix(arg, "--project="):
		value, ok := needValue("--project")
		if !ok {
			return -1, 2
		}
		dest.project = value
	case arg == "--sessions" || strings.HasPrefix(arg, "--sessions="):
		dest.sessions = true
		if strings.HasPrefix(arg, "--sessions=") {
			dest.sessionsRow = strings.TrimPrefix(arg, "--sessions=")
			return i, 0
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			dest.sessionsRow = args[i+1]
			return i + 1, 0
		}
		return i, 0
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
	var dest layoutDestFlags

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
		next, code := takeLayoutCommonFlag(arg, args, i, help, env, &dest, &waitDuration)
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
	if dest.sessions && (dest.shell != "" || dest.project != "") {
		cliErrf(env.Stderr, "--sessions cannot be combined with --shell or --project\n\n%s", help)
		return 2
	}
	return runLayout(env, uirequest.LayoutPayload{Mode: uirequest.LayoutModeGet}, jsonOutput, dest, waitDuration)
}

func runLayoutApply(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("layout").FindSubcommand("apply")
	help := RenderHelp(cmd)

	jsonOutput := false
	waitDuration := 1200 * time.Millisecond
	var dest layoutDestFlags
	var panes []uirequest.LayoutPane
	specColumns := json.RawMessage(nil)

	takeSpecValue := func(value string) int {
		if value == "-" {
			raw, err := io.ReadAll(os.Stdin)
			if err != nil || len(bytes.TrimSpace(raw)) == 0 {
				cliErrf(env.Stderr, "--spec - read no spec from stdin\n\n%s", help)
				return 2
			}
			value = string(raw)
		}
		spec, code, msg := layoutSpecFlag(value)
		if code != 0 {
			cliErrf(env.Stderr, "%s\n\n%s", msg, help)
			return code
		}
		// The payload's columns field carries the spec's column array; the
		// object form is the user-facing grammar.
		encoded, err := json.Marshal(spec.Columns)
		if err != nil {
			cliErrf(env.Stderr, "%v\n\n%s", err, help)
			return 1
		}
		specColumns = encoded
		return 0
	}

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
			if specColumns != nil {
				cliErrf(env.Stderr, "--spec and --pane are different modes: pass one or the other\n\n%s", help)
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
		if arg == "--spec" || strings.HasPrefix(arg, "--spec=") {
			if len(panes) > 0 || specColumns != nil {
				cliErrf(env.Stderr, "--spec and --pane are different modes: pass one or the other\n\n%s", help)
				return 2
			}
			value, nextArg, ok := takeFlagArg(arg, args, i, "--spec")
			if !ok || strings.TrimSpace(value) == "" {
				cliErrf(env.Stderr, "--spec requires a JSON layout (or - for stdin)\n\n%s", help)
				return 2
			}
			if code := takeSpecValue(value); code != 0 {
				return code
			}
			i = nextArg
			continue
		}
		next, code := takeLayoutCommonFlag(arg, args, i, help, env, &dest, &waitDuration)
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

	if dest.sessions && (dest.shell != "" || dest.project != "") {
		cliErrf(env.Stderr, "--sessions cannot be combined with --shell or --project\n\n%s", help)
		return 2
	}
	if len(panes) == 0 && specColumns == nil {
		cliErrf(env.Stderr, "layout apply needs --spec or at least one --pane descriptor\n\n%s", help)
		return 2
	}
	return runLayout(env, uirequest.LayoutPayload{Mode: uirequest.LayoutModeApply, Panes: panes, Columns: specColumns}, jsonOutput, dest, waitDuration)
}

// runLayoutMove is `sidecar layout move`. It validates the move's grammar
// through the same uirequest helpers the host validates with, so a usage error
// here and a decline there can never disagree about what a move even is. Which
// pane sits at a cell, what "right" means for this tree, and every cap and
// floor are the host's to answer against the live layout.
func runLayoutMove(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("layout").FindSubcommand("move")
	help := RenderHelp(cmd)

	jsonOutput := false
	waitDuration := 1200 * time.Millisecond
	var dest layoutDestFlags
	move := uirequest.LayoutMove{}

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
		if arg == "--focused" {
			move.Focused = true
			continue
		}
		if arg == "--to" || strings.HasPrefix(arg, "--to=") {
			value, nextArg, ok := takeFlagArg(arg, args, i, "--to")
			if !ok || strings.TrimSpace(value) == "" {
				cliErrf(env.Stderr, "--to requires a destination\n\n%s", help)
				return 2
			}
			move.To = value
			i = nextArg
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			if move.From != "" {
				cliErrf(env.Stderr, "layout move names one pane to move, and %q is a second\n\n%s", arg, help)
				return 2
			}
			move.From = arg
			continue
		}
		next, code := takeLayoutCommonFlag(arg, args, i, help, env, &dest, &waitDuration)
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

	if dest.sessions && (dest.shell != "" || dest.project != "") {
		cliErrf(env.Stderr, "--sessions cannot be combined with --shell or --project\n\n%s", help)
		return 2
	}
	if strings.TrimSpace(move.To) == "" {
		cliErrf(env.Stderr, "layout move needs --to naming where the pane goes\n\n%s", help)
		return 2
	}
	if err := uirequest.ValidateLayoutMove(move); err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, help)
		return 2
	}
	return runLayout(env, uirequest.LayoutPayload{Mode: uirequest.LayoutModeMove, Move: &move}, jsonOutput, dest, waitDuration)
}

// layoutSpecFlag validates one --spec value CLI-side through the shared
// grammar and returns the parsed spec: shape within the caps, known kinds,
// exactly one primary, and the fields each kind takes. Semantic resolution
// (paths, providers, live-leaf accounting) stays host-side where the tree and
// matchers live.
func layoutSpecFlag(value string) (uirequest.LayoutSpec, int, string) {
	spec, err := uirequest.DecodeLayoutSpec(json.RawMessage(value))
	if err != nil {
		return spec, 2, fmt.Sprintf("--spec is not a valid layout: %v", err)
	}
	if err := uirequest.ValidateLayoutSpec(spec); err != nil {
		return spec, 2, err.Error()
	}
	return spec, 0, ""
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

// printLayoutResult prints apply's and move's outcome in one projection or the
// other, never both. A bare `layout apply` is a human command and must not
// spray JSON at a terminal that never asked for it; `--json` is the documented
// promise of "one structured result object to stdout", which a jq pipe can only
// read if the human per-pane lines are not appended after it. Both projections
// carry the same per-item verdicts, cells, and reasons, so nothing is lost by
// choosing one — `get` already answers this way.
func printLayoutResult(env Env, mode string, dest openDestination, acks []uirequest.Ack, jsonOutput bool) {
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
	if jsonOutput {
		_ = json.NewEncoder(env.Stdout).Encode(result)
		return
	}

	if mode == uirequest.LayoutModeMove {
		for _, item := range items {
			switch item.Verdict {
			case uirequest.ItemVerdictMoved:
				_, _ = fmt.Fprintf(env.Stdout, "moved the pane to %s on %s\n", cellOrDash(item.Cell), surfaceOrDash(item.Surface))
			case uirequest.ItemVerdictUnchanged:
				_, _ = fmt.Fprintf(env.Stdout, "unchanged: the pane is still at %s on %s (%s)\n",
					cellOrDash(item.Cell), surfaceOrDash(item.Surface), item.Reason)
			default:
				_, _ = fmt.Fprintf(env.Stdout, "declined: %s\n", item.Reason)
			}
		}
		return
	}
	if mode != uirequest.LayoutModeApply {
		return
	}
	for _, item := range items {
		switch item.Verdict {
		case uirequest.ItemVerdictOpened:
			_, _ = fmt.Fprintf(env.Stdout, "opened pane at %s (index %d)\n", cellOrDash(item.Cell), item.Index)
		case uirequest.ItemVerdictRetargeted:
			_, _ = fmt.Fprintf(env.Stdout, "retargeted the pane already showing that content at %s (index %d)\n", cellOrDash(item.Cell), item.Index)
		case uirequest.ItemVerdictCarried:
			_, _ = fmt.Fprintf(env.Stdout, "carried the live pane already at %s (index %d)\n", cellOrDash(item.Cell), item.Index)
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
