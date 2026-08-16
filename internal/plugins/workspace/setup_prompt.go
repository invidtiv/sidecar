package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/styles"
)

// An empty Workspaces list has two very different causes, and only one of them
// is Sidecar's fault. A user who has configured a project and has tmux simply
// has no workspaces yet: "press n to create one" is the whole answer, and
// pointing them at Configuration would be a detour away from the thing they
// came here to do.
//
// The other cause is a missing prerequisite — no project is configured, or
// tmux is not installed, so a shell cannot be created at all. There, "press n"
// is advice that cannot work. That state gets one contextual route into
// Sidecar Setup, which is where the missing piece is repaired.
//
// The check is deliberately cheap: the configured project list is already in
// memory, and tmux availability is the process-wide cached lookup the shell
// code already uses. Nothing here touches the filesystem on the render path.

// tmuxAvailable is the process-wide cached PATH lookup the shell code already
// does. It is a variable so a test can state which world it is describing
// instead of depending on the machine it runs on.
var tmuxAvailable = isTmuxInstalled

// setupPrompt is the contextual empty state for a blocked Workspaces list.
type setupPrompt struct {
	headline string
	copy     string
}

// regionOpenSetupButton is the pressable pill in the blocked empty state.
const regionOpenSetupButton = "open-setup-button"

// setupPromptFor reports the prompt for a blocked Workspaces list, if it is
// blocked. It answers false whenever the list is merely empty.
func (p *Plugin) setupPromptFor() (setupPrompt, bool) {
	if p.ctx != nil && p.ctx.Config != nil && len(p.ctx.Config.Projects.List) == 0 {
		return setupPrompt{
			headline: "No workspaces yet",
			copy:     "Start in Sidecar Setup to add a project and finish the parts Sidecar needs.",
		}, true
	}
	if !tmuxAvailable() {
		return setupPrompt{
			headline: "No workspaces yet",
			copy:     "Shells and worktrees need tmux. Sidecar Setup explains how to install it.",
		}, true
	}
	return setupPrompt{}, false
}

// setupPromptActive reports whether the blocked empty state is on screen, which
// is what makes Enter open Configuration instead of doing nothing.
func (p *Plugin) setupPromptActive() bool {
	if p.filterActive() || p.sharedSidebarRowCount() > 0 {
		return false
	}
	for _, section := range p.sidebarNavSections() {
		if len(section.items) > 0 {
			return false
		}
	}
	_, ok := p.setupPromptFor()
	return ok
}

// openSetupCmd asks the host for Configuration. The plugin does not own the
// surface and does not render it; it sends the same message the header gear's
// handler ends up at, and escape returns here.
func (p *Plugin) openSetupCmd() tea.Cmd {
	if _, ok := p.setupPromptFor(); !ok {
		return nil
	}
	// Setup, not a deeper route: the pill says "Open Sidecar Setup", and a
	// control that lands somewhere other than where it says it will is worse
	// than one extra keystroke. Setup's first row is the repair for whichever
	// prerequisite is missing.
	return func() tea.Msg {
		return app.OpenConfigurationMsg{Page: configui.PageSetup}
	}
}

// setupPromptLines renders the prompt into sidebar-width lines and reports
// which of them is the pressable pill, so the caller can register its hit
// region. A narrow sidebar keeps the pill and drops words from the prose
// rather than clipping the one line that says what to press.
func (p *Plugin) setupPromptLines(prompt setupPrompt, width int) (lines []string, actionLine int) {
	pill := styles.RenderPillWithStyle(setupPillLabel(width), styles.ButtonHover, nil)
	lines = []string{
		styles.Title.Render(ansi.Truncate(prompt.headline, width, "…")),
		"",
	}
	for _, line := range wrapPromptText(prompt.copy, width) {
		lines = append(lines, styles.Muted.Render(line))
	}
	lines = append(lines, "")
	actionLine = len(lines)
	lines = append(lines, pill)
	lines = append(lines, "")
	for _, line := range wrapPromptText("Or select the gear in the header.", width) {
		lines = append(lines, styles.Muted.Render(line))
	}
	return lines, actionLine
}

// wrapPromptText breaks a sentence over the sidebar's width. The prose wraps
// rather than truncating, because a half sentence is worse than two lines —
// and drops entirely below the width where even wrapping produces fragments,
// where the pill above it is still the whole instruction.
func wrapPromptText(text string, width int) []string {
	if width < 12 {
		return nil
	}
	wrapped := ansi.Wordwrap(text, width, "")
	return strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
}

// setupPillLabel is the action's widest form that fits.
func setupPillLabel(width int) string {
	for _, label := range []string{"Enter  Open Sidecar Setup", "Enter  Setup", "Setup"} {
		if ansi.StringWidth(label)+2 <= width {
			return label
		}
	}
	return "Setup"
}
