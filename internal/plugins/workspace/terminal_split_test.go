package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panecodec"
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
	p.requestShellLeaf()
	p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, before.ID)
	extra := &PaneNode{Kind: PaneShell}
	p.paneRoot, _ = SplitLeaf(p.paneRoot, terminalLeafID(p.paneRoot), SplitCols, extra)
	p.releaseShellTermPane()
	if !panelayout.LiveCapReached(p.paneRoot) {
		t.Fatal("premise: the tree is at the cap")
	}
	p.requestShellLeaf()
	if p.openShellLeaf() {
		t.Fatal("openShellLeaf opened a third live terminal")
	}
	if p.shellLeafRequested() {
		t.Fatal("a refused open left the panel requested")
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
	p.requireShellTermPane().Session = termPanelSessionPrefix + "peer"
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
	if p.requireShellTermPane().Session != termPanelSessionPrefix+"peer" {
		t.Fatalf("session = %q, want the leaf to keep owning it", p.requireShellTermPane().Session)
	}
}

// The leaf's identity is persisted as a durable selector — the session name it
// owns — and restore reattaches that, not a pane id.
func TestShellLeafPersistsItsSessionSelector(t *testing.T) {
	p := &Plugin{}
	p.requireShellTermPane().Session = termPanelSessionPrefix + "sidecar-main"
	encoded := p.paneLayoutJSON(&PaneNode{ID: 1, Kind: PaneShell})
	if encoded == nil || encoded.Kind != contentKindShell {
		t.Fatalf("encoded = %+v, want a shell leaf", encoded)
	}
	if encoded.Session != p.requireShellTermPane().Session {
		t.Fatalf("persisted session = %q, want %q", encoded.Session, p.requireShellTermPane().Session)
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
	saved := &state.PaneLayoutJSON{Kind: contentKindShell, Session: termPanelSessionPrefix + "restored"}
	_, live := panecodec.Decode(saved, panecodec.Options{})
	got := liveOfKind(live, panecodec.KindShell)
	if got == nil || got.Session != saved.Session {
		t.Fatalf("decoded live = %+v, want session %q", live, saved.Session)
	}
}

func TestRestorePaneLayoutReattachesShellSessionAndDropsExtras(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	session := termPanelSessionPrefix + "restored"
	layout := &state.PaneLayoutJSON{
		Root: resolved, Surface: "shell:test-shell", Open: true,
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
			B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
				Axis: "rows", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: contentKindShell, Session: session},
				B: &state.PaneLayoutJSON{Kind: contentKindShell, Session: termPanelSessionPrefix + "extra"},
			}},
		},
	}
	_ = p.restorePaneLayout(layout)
	if p.restoredShellSession != session {
		t.Fatalf("restored selector = %q, want %q", p.restoredShellSession, session)
	}
	if n := countLeavesOfKind(p.paneRoot, PaneShell); n != 1 {
		t.Fatalf("shell leaves = %d, want 1 (extras collapsed)", n)
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

// The name the create modal gave a split belongs to the workspace the split
// belongs to. The split does not follow the selection at all now, so the name
// goes with it: a selection landing on another workspace leaves nothing named,
// and the owning workspace still reads what it was called. Real-app proof
// caught the leaf still reading "term \u00b7 <other workspace>" after the
// selection moved.
func TestSplitTerminalNameDoesNotFollowTheSelectionToAnotherWorkspace(t *testing.T) {
	p, _ := twoShellPlugin(t)

	p.createTerminalSplit("dev server", "auto")
	if got := p.shellLeafTitle(); got != "dev server" {
		t.Fatalf("leaf title = %q, want the name the modal gave it", got)
	}

	selectShell(p, 1)
	if p.shellLeafName != "" {
		t.Fatalf("the previous workspace's name %q survived onto another workspace", p.shellLeafName)
	}
	if got := p.shellLeafTitle(); got != p.terminalSplitAutoName() {
		t.Fatalf("leaf title = %q, want the auto-name %q", got, p.terminalSplitAutoName())
	}
}
