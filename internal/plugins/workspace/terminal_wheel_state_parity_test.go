package workspace

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

// The window every parity gesture starts from, far enough off the live edge that
// a pin back to it and a step further away from it land on different rows.
const parityWheelStart = 3

// wheelOutcome is everything a reader can tell about what one notch did: what
// reached the pane, which of the pane's cells the pointer named, and where this
// surface's own window ended up. Two states that produce the same outcome are
// the same experience, which is the whole claim.
type wheelOutcome struct {
	report string
	col    int
	row    int
	window int
}

func (o wheelOutcome) forwarded() bool { return o.report != "" }

// parityWheelPlugin draws one of this plugin's two terminal surfaces over a pane
// deep enough to scroll, in the named keyboard state. Everything else — the
// pane, the mouse tracking it has asked for, its observed grid, the window the
// notch starts from — is held identical between the two states, so the only
// difference between a watched fixture and a live one is where the keyboard is.
func parityWheelPlugin(t *testing.T, termPanel, live, reporting bool) *Plugin {
	t.Helper()
	p := watchedWheelPlugin(t, reporting)
	p.width, p.height = 120, 40
	// The pane's grid is recorded from the component producing it, which this
	// surface holds open in either state; recording it here is what makes the two
	// fixtures answer pane coordinates from the same geometry.
	p.recordPaneGeometry("shell", "sidecar-sh-one", 80, 20)

	if termPanel {
		panel := testTerminalBuffer(strings.Repeat("panel row\n", 60))
		showTermPanel(t, p, SplitRows, 50)
		p.termPanelSession = "sidecar-panel"
		p.termPanelPaneID = "%9"
		p.termPanelOutput = panel
		model := p.newWorkspaceTerminal(workspaceTerminalPrimary)
		model.State = &tty.State{
			Active:                true,
			TargetSession:         "sidecar-panel",
			TargetPane:            "%9",
			MouseReportingEnabled: reporting,
			PaneWidth:             80,
			PaneHeight:            20,
			OutputBuf:             panel,
		}
		p.panelTerminal = model
		p.recordPaneGeometry("panel", "sidecar-panel", 80, 20)
	}

	if live {
		target, pane := "sidecar-sh-one", "%7"
		if termPanel {
			target, pane = "sidecar-panel", "%9"
		}
		p.viewMode = ViewModeInteractive
		p.interactiveState = &InteractiveState{
			Active: true, TargetSession: target, TargetPane: pane,
			TermPanel: termPanel, PaneWidth: 80, PaneHeight: 20,
			// A live pane reports where its cursor is, and the window a clipped
			// grid draws follows it. Leaving it unset would place the live window
			// against the top of the pane and make the two fixtures differ in
			// something other than where the keyboard is.
			CursorRow: 19, CursorVisible: true,
		}
	}

	bound := p.terminalMaxScroll(false)
	if termPanel {
		bound = p.terminalMaxScroll(true)
	}
	if bound <= parityWheelStart+mouse.WheelScrollLines {
		t.Fatalf("test premise: a bound of %d rows cannot hold a notch past the starting window", bound)
	}
	return p
}

// wheelSends is what this run sent to tmux, and clearing the log leaves the next
// run of the same gesture reading only its own.
func wheelSends(t *testing.T, logPath string) string {
	t.Helper()
	logged := readTmuxLog(t, logPath)
	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	sends := make([]string, 0, 2)
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "send-keys") {
			sends = append(sends, strings.TrimSpace(line))
		}
	}
	return strings.Join(sends, "\n")
}

// parityWheelOutcome replays one notch over a named surface in a named state and
// reports what it did. The pointer is placed by the surface's own drawn box, so
// the same screen position is the same pane cell in both states.
func parityWheelOutcome(t *testing.T, logPath string, termPanel, live, reporting, alt bool) wheelOutcome {
	t.Helper()
	p := parityWheelPlugin(t, termPanel, live, reporting)

	surface := p.terminalSurfaceGeometry(termPanel)
	if !surface.OK {
		t.Fatal("test premise: the surface under the pointer is not on screen")
	}
	region := regionPreviewPane
	if termPanel {
		region = regionTermPanelContent
		p.termPanelScroll = parityWheelStart
	} else {
		p.previewScroll = parityWheelStart
	}

	// Read from the window the notch is about to be taken over, which is the one
	// the handler asks about: a pane cell is a position in what is drawn.
	x, y := surface.X+2, surface.Y+3
	col, row, ok := p.terminalMouseCoords(termPanel, x, y)
	if !ok {
		t.Fatalf("test premise: (%d,%d) does not land on a cell of the pane", x, y)
	}

	runCommandTree(p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: x, Y: y, Alt: alt,
		Region: &mouse.Region{ID: region},
	}))

	window := p.previewScroll
	if termPanel {
		window = p.termPanelScroll
	}
	return wheelOutcome{report: wheelSends(t, logPath), col: col, row: row, window: window}
}

// The reported bug, as an executable claim: one notch over a given pane does the
// same thing whether or not the keyboard is in it, on both of this plugin's
// terminal surfaces. The two states are compared against each other rather than
// against a number written here twice, and what they are both held to — who owns
// the notch — is read from the shared rule for the pane's own facts, so a
// surface cannot pass by agreeing with itself.
func TestOneNotchLandsTheSameWayWatchedOrLive(t *testing.T) {
	for _, pane := range []struct {
		name      string
		reporting bool
		alt       bool
	}{
		{"an application that has asked for mouse reports", true, false},
		{"a plain shell", false, false},
		{"alt+wheel over an application that has asked for mouse reports", true, true},
	} {
		t.Run(pane.name, func(t *testing.T) {
			logPath := installSuccessfulFakeTmux(t)
			route, _ := tty.RouteWheel(tty.WheelInput{
				Delta: -mouse.WheelScrollLines, Alt: pane.alt,
				MouseReporting: pane.reporting, InPane: true, WritesEnabled: true,
			})

			windows := map[string]int{}
			for _, surface := range []struct {
				name      string
				termPanel bool
			}{
				{"the preview", false},
				{"the terminal panel", true},
			} {
				watched := parityWheelOutcome(t, logPath, surface.termPanel, false, pane.reporting, pane.alt)
				live := parityWheelOutcome(t, logPath, surface.termPanel, true, pane.reporting, pane.alt)

				if watched != live {
					t.Fatalf("%s answers the same notch differently: watched %+v, live %+v",
						surface.name, watched, live)
				}
				if watched.forwarded() != (route == tty.WheelPane) {
					t.Fatalf("%s forwarded=%v for a notch the shared rule routes %v: %+v",
						surface.name, watched.forwarded(), route, watched)
				}
				if route == tty.WheelPane {
					// While the application owns the wheel it owns what the pane
					// shows, so the window is handed back to the live edge.
					if watched.window != 0 {
						t.Fatalf("%s left its window %d rows back while the app owned the notch",
							surface.name, watched.window)
					}
					if watched.col < 1 || watched.row < 1 {
						t.Fatalf("%s forwarded the notch at (%d,%d), not the pane's own 1-indexed cell",
							surface.name, watched.col, watched.row)
					}
				}
				windows[surface.name] = watched.window
			}

			// The same notch over the same kind of pane moves both surfaces' windows
			// the same distance: how far one notch travels is not a property of which
			// terminal a reader happens to be looking at.
			if windows["the preview"] != windows["the terminal panel"] {
				t.Fatalf("one notch left the preview %d rows back and the panel %d",
					windows["the preview"], windows["the terminal panel"])
			}
			if route != tty.WheelPane && windows["the preview"] != parityWheelStart+mouse.WheelScrollLines {
				t.Fatalf("a local notch left the window at %d, want the notch's %d rows past %d",
					windows["the preview"], mouse.WheelScrollLines, parityWheelStart)
			}
		})
	}
}

// A forwarded notch is input, and input to a pane is gated exactly as typing is:
// with write support off nothing leaves either surface in either state, and the
// notch falls back to the window — the same fallback a pane that tracks no mouse
// gets.
func TestNoNotchIsForwardedFromAnySurfaceInAnyStateWithWritesDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.TmuxInteractiveInput.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	logPath := installSuccessfulFakeTmux(t)
	for _, termPanel := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			got := parityWheelOutcome(t, logPath, termPanel, live, true, false)
			if got.forwarded() {
				t.Fatalf("termPanel=%v live=%v forwarded a notch with writes disabled: %s",
					termPanel, live, got.report)
			}
			if got.window != parityWheelStart+mouse.WheelScrollLines {
				t.Fatalf("termPanel=%v live=%v left the window at %d, want the notch on it",
					termPanel, live, got.window)
			}
		}
	}
}

// Who owns a notch is a property of the pane, and the panel's pane has an
// identity of its own. The observation was stored for the agent and shell panes
// and never for the panel's, so a panel drawn with no model open on it — a
// hidden output surface, a split too small to host one — answered "no mouse
// reporting" for a pane that has it, and scrolled locally where the very same
// pane forwards when the preview draws it.
func TestThePanelsPaneKeepsTheMouseFlagItsModelObserved(t *testing.T) {
	p := parityWheelPlugin(t, true, false, true)
	p.panelTerminalTarget = workspaceTerminalTarget{
		Session: "sidecar-panel", Pane: "%9", Source: "panel", SourceID: "sidecar-panel",
	}
	p.syncTerminalModels()
	if !p.paneMouseReporting(true) {
		t.Fatal("the panel does not report the mouse its own model observed")
	}

	// A model closes whenever its surface stops being drawn; the pane it was
	// reading is still the pane a notch over that surface lands on.
	p.panelTerminal.State.Active = false
	if !p.paneMouseReporting(true) {
		t.Fatal("the panel's observed mouse flag died with the model that observed it")
	}
}
