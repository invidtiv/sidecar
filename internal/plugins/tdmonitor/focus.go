package tdmonitor

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/td/pkg/monitor"

	"github.com/marcus/sidecar/internal/plugin"
)

// The embedded td monitor extends its own panel ring with the shell's
// notification centre: Current Work → Task List → Activity → centre → Current
// Work while the panel is open, and exactly td's three-panel cycle while it is
// closed.
//
// This surface is the one case where the ring is not sidecar's. td owns the
// panels, the cursor clamping and the scroll bookkeeping that go with moving
// between them, so this file asks td both questions rather than answering
// either itself: `ActivePanel` (exported by td) says where the cycle stands,
// and the handback is td's own `tab`/`shift+tab` — replayed into the hosted
// model, whose wrap is the `% 3` in td's CmdNextPanel. Reaching into the model
// to assign a panel would skip the clamp and the scroll fix-up that command
// runs, which is exactly the kind of second cycle this plumbing exists to
// avoid.
var _ plugin.FocusCycler = (*Plugin)(nil)

// AtFocusCycleEnd reports the wrap point of td's main panel ring. Only the main
// context has one: td binds `tab` to a modal's button cycle, to an epic's task
// section and to its forms and searches, and a shell stop must not take the key
// from any of them.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	if p.model == nil || p.model.CurrentContextString() != "td-monitor" {
		return false
	}
	if reverse {
		return p.model.ActivePanel == monitor.PanelCurrentWork
	}
	return p.model.ActivePanel == monitor.PanelActivity
}

// FocusCycleStart hands the keyboard back to the panel td's own cycle resumes
// at, by replaying the key that would have wrapped it.
func (p *Plugin) FocusCycleStart(reverse bool) tea.Cmd {
	if p.model == nil {
		return nil
	}
	key := tea.KeyPressMsg{Code: tea.KeyTab}
	if reverse {
		key.Mod = tea.ModShift
	}
	updated, cmd := p.model.Update(key)
	if m, ok := updated.(monitor.Model); ok {
		p.model = &m
	}
	return cmd
}
