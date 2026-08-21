package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

// The create modal's placement row is the CLI's --split vocabulary, and the
// leaf lands where it says: Right beside the primary terminal, Below under it,
// Auto by panelayout's rules (which, from a lone terminal, is beside it).
func TestCreateTerminalSplitPlacement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		placement string
		want      SplitAxis
	}{
		{name: "auto", placement: "auto", want: SplitCols},
		{name: "right", placement: "right", want: SplitCols},
		{name: "below", placement: "below", want: SplitRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
			stubTd(t)
			p := docPaneTestPlugin(t, t.TempDir(), true)
			p.sidebarVisible = false
			p.View(p.width, p.height)

			p.createTerminalSplit("dev server", tc.placement)
			leaf := p.shellLeaf()
			if leaf == nil {
				t.Fatalf("no shell leaf after a %s create", tc.placement)
			}
			split := FindPane(p.paneRoot, p.shellSplitID())
			if split == nil || split.Split == nil || split.Split.Axis != tc.want {
				t.Fatalf("axis = %+v, want %v", split, tc.want)
			}
			if got := p.shellLeafTitle(); got != "dev server" {
				t.Fatalf("leaf title = %q, want the name the modal gave it", got)
			}
			if p.shellSplitPlacement != "" {
				t.Fatalf("placement %q outlived the open it was for", p.shellSplitPlacement)
			}
		})
	}
}

// The cap is two live terminals on screen. The third request refuses and says
// so; it never opens a leaf, and it never silently does nothing.
func TestTerminalSplitRefusesPastTheLiveCapWithAToast(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitCols)
	if got := panelayout.LiveLeafCount(p.paneRoot); got != panelayout.LiveLeafCap {
		t.Fatalf("live leaves = %d, want the cap %d", got, panelayout.LiveLeafCap)
	}
	before := p.shellLeaf()
	p.toastMessage = ""

	p.createTerminalSplit("second", "right")
	if leaf := p.shellLeaf(); leaf != before {
		t.Fatalf("a refused create changed the tree: %+v", leaf)
	}
	if !strings.Contains(p.toastMessage, "Two live terminals") {
		t.Fatalf("toast = %q, want the cap refusal", p.toastMessage)
	}

	// The refusal is panelayout's, not this surface's: openShellLeaf turns the
	// flag back off rather than claiming a leaf nothing drew.
	p.toastMessage = ""
	p.termPanelVisible = true
	p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, before.ID)
	extra := &PaneNode{Kind: PaneShell}
	p.paneRoot, _ = SplitLeaf(p.paneRoot, terminalLeafID(p.paneRoot), SplitCols, extra)
	p.termPanelVisible = false
	if !panelayout.LiveCapReached(p.paneRoot) {
		t.Fatal("premise: the tree is at the cap")
	}
	p.termPanelVisible = true
	if p.openShellLeaf() {
		t.Fatal("openShellLeaf opened a third live terminal")
	}
	if p.termPanelVisible {
		t.Fatal("a refused open left the panel flagged visible")
	}
	if p.toastMessage != shellCapMessage {
		t.Fatalf("toast = %q, want %q", p.toastMessage, shellCapMessage)
	}
}

// A shell leaf is a peer, not an accessory: closing the pane beside it collapses
// that split and leaves the terminal — and its session — alone.
func TestClosingANeighbourLeavesTheShellLeaf(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitRows)
	p.termPanelSession = termPanelSessionPrefix + "peer"
	shell := p.shellLeaf()
	if shell == nil {
		t.Fatal("premise: a shell leaf is on screen")
	}
	terminal := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
	if terminal == nil {
		t.Fatal("premise: a primary terminal leaf is on screen")
	}

	p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, terminal.ID)
	if got := p.shellLeaf(); got == nil || got.ID != shell.ID {
		t.Fatalf("shell leaf after a neighbour closed = %+v, want %d", got, shell.ID)
	}
	if p.termPanelSession != termPanelSessionPrefix+"peer" {
		t.Fatalf("session = %q, want the leaf to keep owning it", p.termPanelSession)
	}
}

// The leaf's identity is persisted as a durable selector — the session name it
// owns — and restore reattaches that, not a pane id.
func TestShellLeafPersistsItsSessionSelector(t *testing.T) {
	p := &Plugin{termPanelSession: termPanelSessionPrefix + "sidecar-main"}
	encoded := p.encodePaneNode(&PaneNode{ID: 1, Kind: PaneShell})
	if encoded == nil || encoded.Kind != contentKindShell {
		t.Fatalf("encoded = %+v, want a shell leaf", encoded)
	}
	if encoded.Session != p.termPanelSession {
		t.Fatalf("persisted session = %q, want %q", encoded.Session, p.termPanelSession)
	}

	tests := []struct {
		name      string
		persisted string
		derived   string
		want      string
	}{
		{
			name:      "a persisted session is reattached",
			persisted: termPanelSessionPrefix + "old",
			derived:   termPanelSessionPrefix + "new",
			want:      termPanelSessionPrefix + "old",
		},
		{
			name:    "nothing persisted derives from the selection",
			derived: termPanelSessionPrefix + "new",
			want:    termPanelSessionPrefix + "new",
		},
		{
			name:      "a tmux pane id is never a selector",
			persisted: "%17",
			derived:   termPanelSessionPrefix + "new",
			want:      termPanelSessionPrefix + "new",
		},
		{
			name:      "nor is another tool's session",
			persisted: "sidecar-agent-foo",
			derived:   termPanelSessionPrefix + "new",
			want:      termPanelSessionPrefix + "new",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellSessionSelector(tc.persisted, tc.derived); got != tc.want {
				t.Fatalf("shellSessionSelector = %q, want %q", got, tc.want)
			}
		})
	}

	// A persisted layout round-trips the selector into the restore path.
	host := &Plugin{}
	shellCount := 0
	saved := &state.PaneLayoutJSON{Kind: contentKindShell, Session: termPanelSessionPrefix + "restored"}
	if node := host.decodePaneNode(saved, "", new(int), &shellCount, new([]tea.Cmd)); node == nil || node.Kind != PaneShell {
		t.Fatalf("decoded = %+v, want a shell leaf", node)
	}
	if host.restoredShellSession != saved.Session {
		t.Fatalf("restored selector = %q, want %q", host.restoredShellSession, saved.Session)
	}
}

// Closing the terminal itself is explicit and asks first when something is
// running in it — a build or a dev server is not a keystroke to lose.
func TestShellCloseNeedsConfirm(t *testing.T) {
	tests := []struct {
		name    string
		current string
		shell   string
		want    bool
	}{
		{name: "an idle login shell closes freely", current: "zsh", shell: "/bin/zsh", want: false},
		{name: "a dash-prefixed login shell is the same shell", current: "-zsh", shell: "zsh", want: false},
		{name: "a running process asks", current: "npm", shell: "/bin/zsh", want: true},
		{name: "an unknown shell asks", current: "zsh", shell: "", want: true},
		{name: "nothing running closes freely", current: "", shell: "/bin/zsh", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellCloseNeedsConfirm(tc.current, tc.shell); got != tc.want {
				t.Fatalf("shellCloseNeedsConfirm(%q, %q) = %v, want %v", tc.current, tc.shell, got, tc.want)
			}
		})
	}
}

// The modal and the host agree on one placement vocabulary, so a button click
// reaches panelayout unchanged.
func TestPlacementActionsReachTheHost(t *testing.T) {
	for _, action := range []string{
		workspacecreate.ActionPlaceAuto,
		workspacecreate.ActionPlaceRight,
		workspacecreate.ActionPlaceBelow,
	} {
		if !workspacecreate.IsPlacementAction(action) {
			t.Fatalf("%q is not routed as a placement action", action)
		}
	}
	if workspacecreate.IsPlacementAction(createSubmitID) {
		t.Fatal("Create routed as a placement action")
	}
}

// A split terminal follows the selection onto the newly selected workspace's
// own session, so the name the create modal gave it on the workspace it was
// created in must not title another workspace's terminal. Real-app proof
// caught the leaf still reading "term · <other workspace>" after the selection
// moved; an unnamed leaf falls back to the auto-name for where it now is.
func TestSplitTerminalNameDoesNotFollowTheSelectionToAnotherWorkspace(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	stubTd(t)
	dir := t.TempDir()
	p := docPaneTestPlugin(t, dir, true)
	p.sidebarVisible = false
	p.View(p.width, p.height)

	p.createTerminalSplit("dev server", "auto")
	if got := p.shellLeafTitle(); got != "dev server" {
		t.Fatalf("leaf title = %q, want the name the modal gave it", got)
	}

	// The selection moved: the leaf is now showing a different session.
	p.termPanelSession = "sidecar-tp-first"
	p.forgetShellLeafName()
	if got := p.shellLeafTitle(); got == "dev server" {
		t.Fatal("the previous workspace's name titled the new workspace's terminal")
	}
	if want := p.terminalSplitAutoName(); p.shellLeafTitle() != want {
		t.Fatalf("leaf title = %q, want the auto-name %q", p.shellLeafTitle(), want)
	}
}
