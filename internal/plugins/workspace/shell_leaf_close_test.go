package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

// The create modal states the cap instead of discovering it after the click:
// with a split already on screen the Terminal split row is disabled, its reason
// is one line, and every create path refuses.
func TestCreateModalDisablesTerminalSplitAtTheCap(t *testing.T) {
	for _, tc := range []struct {
		name       string
		splitOpen  bool
		wantReason string
	}{
		{name: "one terminal offers the row", splitOpen: false, wantReason: ""},
		{name: "two terminals disable the row", splitOpen: true, wantReason: shellCapDisabledReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
			var p *Plugin
			if tc.splitOpen {
				p = shellLeafTestPlugin(t, SplitCols)
			} else {
				stubTd(t)
				p = docPaneTestPlugin(t, t.TempDir(), true)
				p.sidebarVisible = false
				p.View(p.width, p.height)
			}
			if got := p.terminalSplitDisabledReason(); got != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got, tc.wantReason)
			}

			form := workspacecreate.Open(p.createOpenOpts(workspacecreate.KindTerminalSplit, false, ""))
			if got := form.KindDisabledReason(); got != tc.wantReason {
				t.Fatalf("form reason = %q, want %q", got, tc.wantReason)
			}
			p.createForm = form
			p.viewMode = ViewModeCreate

			before := p.shellLeaf()
			p.submitCreateForm()
			if tc.wantReason == "" {
				return
			}
			if leaf := p.shellLeaf(); leaf != before {
				t.Fatalf("a disabled row still created a split: %+v", leaf)
			}
			// The placement buttons are a create path too, and they refuse for
			// the same reason rather than by a second rule.
			if form.ApplyPlacementAction(workspacecreate.ActionPlaceRight) {
				t.Fatal("a placement button created past the cap")
			}
			if got := panelayout.LiveLeafCount(p.paneRoot); got > panelayout.LiveLeafCap {
				t.Fatalf("live leaves = %d, past the cap", got)
			}
		})
	}
}

// The disabled row's modal says the reason once, in the modal's own muted
// styling, and offers no enabled Create.
func TestCreateModalRendersTheDisabledReasonOnce(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitCols)
	form := workspacecreate.Open(p.createOpenOpts(workspacecreate.KindTerminalSplit, false, ""))
	m := form.Build(60)
	if m == nil {
		t.Fatal("no modal built")
	}
	rendered := m.Render(120, 40, nil)
	if strings.Count(rendered, "close one first") != 1 {
		t.Fatalf("reason line count = %d, want exactly one:\n%s",
			strings.Count(rendered, "close one first"), rendered)
	}
}

// The split's header carries the same ✕ every other non-primary leaf has, and
// the primary terminal does not: closing it would leave the workspace with no
// terminal to select into.
func TestShellLeafHasACloseRegionAndThePrimaryDoesNot(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitCols)
	shell := p.shellLeaf()
	if shell == nil {
		t.Fatal("premise: a shell leaf is on screen")
	}
	primary := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
	if primary == nil {
		t.Fatal("premise: a primary terminal leaf is on screen")
	}

	seen := map[int]bool{}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneClose {
			continue
		}
		if id, ok := region.Data.(int); ok {
			seen[id] = true
		}
	}
	if !seen[shell.ID] {
		t.Fatal("the split terminal has no close region")
	}
	if seen[primary.ID] {
		t.Fatal("the primary terminal grew a close region")
	}
}

// The probe decides between the two closes: a running process opens the
// confirm, an idle shell (or a session that is already gone) closes outright.
func TestShellLeafCloseProbeRoutesConfirmOrClose(t *testing.T) {
	for _, tc := range []struct {
		name        string
		current     string
		err         error
		wantConfirm bool
	}{
		{name: "idle closes", current: "zsh", wantConfirm: false},
		{name: "process confirms", current: "node", wantConfirm: true},
		{name: "dead session closes", err: errors.New("no current target"), wantConfirm: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
			p := shellLeafTestPlugin(t, SplitCols)
			p.termPanelSession = termPanelSessionPrefix + "probe"
			p.handleShellLeafCloseProbe(ShellLeafCloseProbeMsg{
				Session:        p.termPanelSession,
				CurrentCommand: tc.current,
				ShellCommand:   "zsh",
				Err:            tc.err,
			})
			if tc.wantConfirm {
				if p.viewMode != ViewModeConfirmCloseSplit {
					t.Fatalf("viewMode = %v, want the confirm", p.viewMode)
				}
				if p.shellLeaf() == nil {
					t.Fatal("the confirm closed the split before it was answered")
				}
				// Answering it closes; cancelling leaves the split alone.
				p.cancelCloseSplit()
				if p.shellLeaf() == nil {
					t.Fatal("cancel closed the split")
				}
				p.viewMode = ViewModeConfirmCloseSplit
				p.confirmCloseSplit()
			}
			assertSplitClosedAndPrimaryFocusable(t, p)
		})
	}
}

// The wedge: a split terminal whose session ends (the user typed exit) closed
// nothing and left termPanelFocused set, so no terminal could be focused and
// Sidecar needed a restart. Session end now closes the leaf, and the primary
// terminal is focusable again.
func TestShellLeafSessionEndClosesTheSplitAndUnwedgesFocus(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitRows)
	p.termPanelSession = termPanelSessionPrefix + "exit"
	p.termPanelFocused = true
	p.activePane = PanePreview

	p.noteShellLeafSessionEnded()

	assertSplitClosedAndPrimaryFocusable(t, p)
	if p.termPanelSession != "" || p.termPanelPaneID != "" {
		t.Fatalf("a dead session outlived its leaf: %q/%q", p.termPanelSession, p.termPanelPaneID)
	}
	// No confirm: the process the confirm would ask about has already ended.
	if p.viewMode == ViewModeConfirmCloseSplit {
		t.Fatal("session end asked for confirmation")
	}
	// And the cap is free again, so the modal offers the row once more.
	if got := p.terminalSplitDisabledReason(); got != "" {
		t.Fatalf("reason = %q, want the row offered again", got)
	}
}

// assertSplitClosedAndPrimaryFocusable is the shape every close must leave the
// surface in: no shell leaf, none of the three ownership flags set, and focus
// on a primary terminal the keyboard can reach.
func assertSplitClosedAndPrimaryFocusable(t *testing.T, p *Plugin) {
	t.Helper()
	if leaf := p.shellLeaf(); leaf != nil {
		t.Fatalf("the split leaf survived the close: %+v", leaf)
	}
	if p.termPanelVisible {
		t.Fatal("termPanelVisible outlived the leaf")
	}
	if p.termPanelFocused {
		t.Fatal("termPanelFocused outlived the leaf — the keyboard is wedged")
	}
	if p.shellLeafSurface != "" {
		t.Fatalf("shellLeafSurface = %q, want released", p.shellLeafSurface)
	}
	primary := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
	if primary == nil {
		t.Fatal("no primary terminal leaf to focus")
	}
	if p.paneFocus != primary.ID {
		t.Fatalf("paneFocus = %d, want the primary terminal %d", p.paneFocus, primary.ID)
	}
	if p.activePane != PanePreview {
		t.Fatalf("activePane = %v, want the preview", p.activePane)
	}
	// The proof that focus is not wedged: the surface's own focus setter puts
	// the keyboard back on the primary terminal.
	p.focusLeaf(primary.ID)
	if p.termPanelFocused || p.paneFocus != primary.ID {
		t.Fatal("the primary terminal could not take focus back")
	}
}
