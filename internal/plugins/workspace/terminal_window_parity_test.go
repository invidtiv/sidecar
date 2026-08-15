package workspace

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// scrolledBackPanelPlugin draws a passive terminal panel scrolled back over a
// buffer whose live grid ends in blank rows — the shape tmux leaves under a
// prompt, and the state where trimming those rows moves every window row.
func scrolledBackPanelPlugin(t *testing.T) *Plugin {
	t.Helper()
	rows := make([]string, 0, 44)
	for i := range 40 {
		rows = append(rows, fmt.Sprintf("row %02d alpha bravo", i))
	}
	rows = append(rows, "", "", "", "")

	p := New()
	p.width, p.height = 100, 30
	p.sidebarWidth = 40
	p.viewMode = ViewModeList
	p.termPanelVisible = true
	p.termPanelSession = "term-1"
	p.termPanelOutput = testTerminalBuffer(strings.Join(rows, "\n"))
	p.termPanelScroll = 6
	p.truncateCache = ui.NewTruncateCache(64)
	p.selectionTermPanel = true
	p.selection.Clear()
	return p
}

// Hit testing and rendering must resolve a screen row to the same buffer row.
// A second construction of the window that omits the renderer's trailing-row
// trim reports a different EffectiveCount, which moves MaxOffset and with it
// every row of a window placed from the bottom.
func TestPassiveScrolledBackPanelHitTestsTheRowsItDraws(t *testing.T) {
	p := scrolledBackPanelPlugin(t)
	surface := p.terminalSurfaceGeometry(true)
	if !surface.OK {
		t.Fatal("test premise: the terminal panel is not on screen")
	}
	p.selection.ViewRect = mouse.Rect{X: surface.X, Y: surface.HeaderY, W: surface.Width, H: surface.Height + terminalHeaderRows}

	drawn := strings.Split(p.renderTermPanelOutput(surface.Width, surface.Height+terminalHeaderRows), "\n")
	if len(drawn) < 3 {
		t.Fatalf("the panel drew %d rows, too few to hit-test", len(drawn))
	}

	// The second content row, well clear of both edges of the window. The
	// scrollbar owns the final column, so the drawn row is compared without it.
	const contentRow = 1
	row := ansi.Strip(drawn[terminalHeaderRows+contentRow])
	if p.terminalSelectionViewportLayout().ShowScrollbar {
		row = ansi.Truncate(row, max(ansi.StringWidth(row)-1, 0), "")
	}
	want := strings.TrimRight(row, " ")
	line, _, ok := p.interactiveCharAtXY(surface.X+1, surface.Y+contentRow)
	if !ok {
		t.Fatal("a click inside the drawn panel mapped to no buffer row")
	}
	got, ok := p.terminalLineText(line)
	if !ok {
		t.Fatalf("hit testing reported buffer line %d, which the panel has no text for", line)
	}
	if strings.TrimRight(ansi.Strip(got), " ") != want {
		t.Fatalf("a click on the row drawn as %q landed on buffer line %d (%q)", want, line, got)
	}
}

// The same window, stated as the layout: hit testing sees the trimmed count the
// renderer laid the rows out with.
func TestScrolledBackHitTestLayoutTrimsWithTheRenderer(t *testing.T) {
	p := scrolledBackPanelPlugin(t)
	layout := p.terminalSelectionViewportLayout()
	if layout.EffectiveCount != 40 {
		t.Fatalf("hit-test window counted %d lines, want the 40 the renderer draws after trimming",
			layout.EffectiveCount)
	}
}

// A wheel notch is a scroll made outside a pointer gesture wherever it lands, so
// the primary pane drops the selection exactly as the terminal panel does: a
// relative buffer renumbers as it grows, and a surviving anchor would cover rows
// the user never picked — and copy-on-select would put them on the clipboard.
func TestWheelOverLivePrimaryPaneDropsTheSelection(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{OutputBuf: testTerminalBuffer(strings.Repeat("selectable row\n", 60))},
	}}
	attachLiveTerminal(p, false)
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 10, Col: 0},
		ui.SelectionPoint{Line: 12, Col: 4},
		false,
	)
	if tty.ScrollKeepsSelection(p.interactiveOutputBuffer()) {
		t.Fatal("test premise: this buffer numbers its lines absolutely, so a scroll keeps the selection")
	}

	p.wheelTerminal(false, mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 60, Y: 8,
	}, -mouse.WheelScrollLines)

	if p.selection.HasSelection() {
		t.Fatalf("the wheel left a selection at %+v..%+v over rows the user never picked",
			p.selection.Start, p.selection.End)
	}
}

// Every way out of interactive mode must close the component's escape window
// and drop its held mouse fragment. ExitReleasesInput keeps the model active
// behind this surface, so a mode left without releasing input still delivers
// the escape timer it scheduled to a pane the user has walked away from.
func TestClickAwayReleasesTheComponentsInput(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{OutputBuf: testTerminalBuffer(strings.Repeat("row\n", 20))},
	}}
	terminal := attachLiveTerminal(p, false)

	p.handleInteractiveKeys(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !terminal.State.EscapePressed || !terminal.State.EscapeTimerPending {
		t.Fatal("test premise: the escape was not held for a double-escape")
	}

	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionSidebar},
	})

	if !terminal.IsActive() {
		t.Fatal("test premise: the component closed, so it cannot prove input was released")
	}
	if terminal.State.EscapePressed || terminal.State.EscapeTimerPending {
		t.Fatalf("clicking away left the escape window open (pressed=%v pending=%v), so its timer still reaches the pane",
			terminal.State.EscapePressed, terminal.State.EscapeTimerPending)
	}
}

// A press away from every terminal region is decided by the action, not by
// which handler happens to see it: a double or triple click leaves the pane
// exactly as a single one does.
func TestDoubleAndTripleClickAwayLeaveTheTerminal(t *testing.T) {
	for name, click := range map[string]func(*Plugin, mouse.MouseAction) tea.Cmd{
		"double": (*Plugin).handleMouseDoubleClick,
		"triple": (*Plugin).handleMouseTripleClick,
	} {
		t.Run(name, func(t *testing.T) {
			p := newInteractiveInputTestPlugin()
			p.width, p.height = 100, 30
			p.shellSelected = true
			p.shells = []*ShellSession{{
				TmuxName: "shell-1",
				Agent:    &Agent{OutputBuf: testTerminalBuffer("row\n")},
			}}
			attachLiveTerminal(p, false)
			p.pointer.Arm(tty.ClickForward, 60, 8)

			action := mouse.MouseAction{Region: &mouse.Region{ID: regionSidebar}}
			if name == "double" {
				action.Type = mouse.ActionDoubleClick
			} else {
				action.Type = mouse.ActionTripleClick
			}
			click(p, action)

			if p.viewMode == ViewModeInteractive {
				t.Errorf("a %s click on the sidebar left the pane holding the keyboard", name)
			}
			if p.pointer.Resolution != tty.ClickNone {
				t.Errorf("a %s click away kept the armed resolution %v", name, p.pointer.Resolution)
			}
		})
	}
}

// A key that arrives before reconciliation has opened the component opens it
// here. Opening allocates the control client's mailbox and schedules the first
// capture, and both are work the caller has to run: dropped, the subprocess
// keeps running with nobody draining it and no frame is ever asked for.
func TestOpeningTheTerminalForAKeyReportsItsWork(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.interactiveState.TargetPane = "%1"

	terminal, open := p.interactiveTerminal()
	if terminal == nil || !terminal.IsActive() {
		t.Fatal("the live pane has no open component behind it")
	}
	if open == nil {
		t.Fatal("opening the terminal reported no work, so its control client has nobody draining it")
	}
	if _, again := p.interactiveTerminal(); again != nil {
		t.Fatal("an already-open component was opened a second time")
	}
	terminal.Close()
}

// Arming a gesture drops whatever the last one armed, before any branch can
// return without arming a new one. A resolution that survives a refused press
// fires at a stale position on the next release.
func TestArmingATerminalGestureDropsTheLastResolution(t *testing.T) {
	p := newMouseReportingTestPlugin()
	p.pointer.Arm(tty.ClickForward, 60, 8)

	// A press with no region: prepareInteractiveDrag refuses it and arms nothing.
	p.prepareInteractiveTerminalGesture(mouse.MouseAction{Type: mouse.ActionClick, X: 60, Y: 8})

	if p.pointer.Resolution != tty.ClickNone {
		t.Fatalf("resolution = %v after a refused press, want none", p.pointer.Resolution)
	}
}
