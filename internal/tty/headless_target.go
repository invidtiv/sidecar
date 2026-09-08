package tty

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// HeadlessTargetIdentity is the tmux-owned half of a validated mobile target.
// Session and project ownership are resolved separately through Sidecar's
// managed-target registry before this probe runs.
type HeadlessTargetIdentity struct {
	ServerPID      int
	SessionID      string
	SessionCreated string
	Session        string
	Pane           string
	Width          int
	Height         int
	PaneCount      int
}

// InspectHeadlessTarget resolves exactly one pane in a managed session. M0
// refuses multi-pane windows rather than silently selecting a pane; the M1
// catalog/picker will make that choice explicit.
func InspectHeadlessTarget(ctx context.Context, session string) (HeadlessTargetIdentity, error) {
	if strings.TrimSpace(session) == "" {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: empty session")
	}
	format := "#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}\t#{pane_width}\t#{pane_height}\t#{window_panes}"
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-t", session, "-F", format).CombinedOutput()
	if err != nil {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: inspect session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 || lines[0] == "" {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: session %q has %d panes; select-one-pane layouts are not yet supported", session, len(lines))
	}
	return parseHeadlessTargetIdentity(lines[0], session, "")
}

// InspectHeadlessPane resolves one exact pane selected from a server-owned
// candidate set. display-message targets the pane itself, so a multi-pane
// window does not silently substitute its active pane.
func InspectHeadlessPane(ctx context.Context, pane string) (HeadlessTargetIdentity, error) {
	if !controlPanePattern.MatchString(strings.TrimSpace(pane)) {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: invalid pane %q", pane)
	}
	format := "#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}\t#{pane_width}\t#{pane_height}\t#{window_panes}"
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane, "-F", format).CombinedOutput()
	if err != nil {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: inspect pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseHeadlessTargetIdentity(strings.TrimSpace(string(out)), "", pane)
}

func parseHeadlessTargetIdentity(line, expectedSession, expectedPane string) (HeadlessTargetIdentity, error) {
	parts := strings.Split(line, "\t")
	if (len(parts) != 7 && len(parts) != 8) || parts[3] == "" || !controlPanePattern.MatchString(parts[4]) ||
		(expectedSession != "" && parts[3] != expectedSession) || (expectedPane != "" && parts[4] != expectedPane) {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: invalid tmux identity %q", line)
	}
	serverPID, errPID := strconv.Atoi(parts[0])
	width, errWidth := strconv.Atoi(parts[5])
	height, errHeight := strconv.Atoi(parts[6])
	if errPID != nil || errWidth != nil || errHeight != nil || serverPID <= 0 || width < 2 || height < 1 {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: invalid tmux identity %q", line)
	}
	if err := validHeadlessGeometry(width, height); err != nil {
		return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: unsupported capture geometry: %w", err)
	}
	paneCount := 1
	if len(parts) == 8 {
		paneCount, _ = strconv.Atoi(parts[7])
		if paneCount < 1 {
			return HeadlessTargetIdentity{}, fmt.Errorf("mobile target: invalid tmux layout identity %q", line)
		}
	}
	return HeadlessTargetIdentity{
		ServerPID: serverPID, SessionID: parts[1], SessionCreated: parts[2], Session: parts[3],
		Pane: parts[4], Width: width, Height: height, PaneCount: paneCount,
	}, nil
}
