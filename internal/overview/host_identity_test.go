package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// A remote row that looks remote, in both projections.
//
// The Sessions list and the Activity board are two projections of one catalog,
// so provenance lands in both or it is a bug. What each surface spends on it
// differs — a list row has a project label to lean on, a card has 16 columns —
// but the glyph and the colour are the shared part, and these tests pin that.

func remoteCardWorkspace(hostID string) workspaceinventory.Workspace {
	return workspaceinventory.Workspace{
		ID: hostID + "\x1fapi:shell:s1", HostID: hostID,
		ProjectKey: hostID + "\x1f/home/me/api", ProjectName: "api",
		Kind: workspaceinventory.KindShell, Name: "Claude pane", Provider: "claude",
		Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "needs input", ChangedAt: time.Now()},
	}
}

func spanText(lines []kanban.Line) string {
	var builder strings.Builder
	for _, line := range lines {
		for _, span := range line.Spans {
			builder.WriteString(span.Text)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// TestRemoteRowAndCardCarryTheSameHostMark is the parity assertion: the same
// glyph and the same per-host colour reach the list and the board. A visual
// distinction that lands in one and not the other is the bug the shared
// catalog exists to prevent.
func TestRemoteRowAndCardCarryTheSameHostMark(t *testing.T) {
	m := catalogModel(t)
	seedHost(m, "mac-mini", *remoteSnapshot("blocked"))
	m.syncBoard()

	// The board.
	workspace := remoteCardWorkspace("mac-mini")
	lines := cardLines(workspace, false, time.Now())
	var cardColor any
	glyphOnCard := false
	for _, line := range lines {
		for _, span := range line.Spans {
			if strings.Contains(span.Text, workspacelist.HostGlyph) {
				glyphOnCard = true
				cardColor = span.Foreground
			}
		}
	}
	if !glyphOnCard {
		t.Fatalf("no host glyph on a remote card:\n%s", spanText(lines))
	}
	if cardColor != workspacelist.HostHue("mac-mini") {
		t.Errorf("card host glyph colour = %v, want the shared host hue %v", cardColor, workspacelist.HostHue("mac-mini"))
	}
	if !strings.Contains(spanText(lines), "mac-mini") {
		t.Errorf("a remote card never names its machine:\n%s", spanText(lines))
	}

	// The list.
	row := rowByName(t, m, "Claude pane")
	if row.Host != "mac-mini" {
		t.Fatalf("the list row dropped the host: %q", row.Host)
	}
	line := renderedRowLine(t, m, "Claude pane")
	if !strings.Contains(line, workspacelist.HostGlyph) {
		t.Errorf("no host glyph on the remote row: %q", line)
	}
	// Exactly once. The project label already carries the machine's name; a
	// chip repeating it would say the same host twice on one line.
	if got := strings.Count(line, "mac-mini"); got != 1 {
		t.Errorf("the remote row names its host %d times, want 1: %q", got, line)
	}
}

// TestLocalRowAndCardCarryNoHostMark: provenance is for rows that have some.
func TestLocalRowAndCardCarryNoHostMark(t *testing.T) {
	m := catalogModel(t)
	local := m.catalog["s1"]
	if local.ID == "" {
		t.Fatal("fixture lost its local worktree")
	}
	if text := spanText(cardLines(local, false, time.Now())); strings.Contains(text, workspacelist.HostGlyph) {
		t.Errorf("a local card was marked remote:\n%s", text)
	}
	if row := rowByName(t, m, "modal"); row.Host != "" {
		t.Errorf("a local row claims a host: %q", row.Host)
	}
	if line := renderedRowLine(t, m, "modal"); strings.Contains(line, workspacelist.HostGlyph) {
		t.Errorf("a local row was marked remote: %q", line)
	}
}

// renderedRowLine is the drawn line for one row, so an assertion reads what a
// user sees rather than the display model behind it.
func renderedRowLine(t *testing.T, m *Model, name string) string {
	t.Helper()
	for _, line := range strings.Split(ansi.Strip(m.renderWorkspaceList(0, 0, 70, 30)), "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no rendered row for %q", name)
	return ""
}

// TestHostHueIsDeterministicAndPerHost. Same host, same colour across
// restarts: the assignment hashes the host ID rather than reading a map or a
// registration order, either of which would repaint the board between runs.
func TestHostHueIsDeterministicAndPerHost(t *testing.T) {
	first := workspacelist.HostHue("mac-mini")
	if again := workspacelist.HostHue("mac-mini"); again != first {
		t.Errorf("the same host got two colours: %v then %v", first, again)
	}
	// Not all of a palette's entries are distinct in every theme, so this asks
	// only that the ramp is being cycled at all rather than collapsed to one.
	seen := map[any]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		seen[workspacelist.HostHue(id)] = true
	}
	if len(seen) < 2 {
		t.Errorf("every host hashed to one colour: %v", seen)
	}
}
