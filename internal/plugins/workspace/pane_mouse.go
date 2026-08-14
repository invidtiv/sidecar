package workspace

import "github.com/marcus/sidecar/internal/tty"

// recordPaneMouseReporting stores tmux's #{mouse_any_flag} for a pane as a
// producer observed it — a poll capture, or the terminal model while one is open
// on the pane. Who owns a wheel notch is a property of the pane and is asked
// whether or not the pane holds the keyboard, so the observation has to outlive
// the producer that made it. The key space is the one terminal history and pane
// geometry already use, so a pane has one identity across all three.
func (p *Plugin) recordPaneMouseReporting(kind, target string, reporting bool) {
	if target == "" {
		return
	}
	if p.paneMouseReports == nil {
		p.paneMouseReports = make(map[string]bool)
	}
	p.paneMouseReports[terminalHistoryKey(kind, target)] = reporting
}

// paneMouseReporting reports that the application drawn on a terminal surface
// has asked for mouse events. The component answers for the pane it is
// producing; a pane no component owns is answered from the last producer that
// observed the flag, and a pane nothing has observed answers no.
func (p *Plugin) paneMouseReporting(termPanel bool) bool {
	if model := p.terminalModelForSurface(termPanel); model != nil && model.IsActive() {
		return model.PaneMouseReporting()
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok {
		return false
	}
	return p.paneMouseReports[source.Key]
}

// terminalModelForSurface is the component drawing a surface. Reconciliation
// closes a model whose target is no longer the pane being drawn, so an active
// model is the pane under its own surface.
func (p *Plugin) terminalModelForSurface(termPanel bool) *tty.Model {
	if termPanel {
		return p.panelTerminal
	}
	return p.primaryTerminal
}
