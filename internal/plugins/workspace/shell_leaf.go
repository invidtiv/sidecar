package workspace

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
)

// The terminal panel is a Shell leaf of the pane tree, and nothing more. Its
// box, its divider, its border and its hit regions are the shared frame's,
// exactly like a document's or a diff's — there is no second split system
// beside the tree, and no arithmetic here that panelayout does not already own.
//
// termPanelVisible remains the panel's LIFECYCLE flag: the tmux session it owns
// outlives a hidden panel, so "is the session ours" and "is a leaf on screen"
// are different questions. syncShellLeaf is the single place the tree is made to
// agree with it — exactly one Shell leaf while the panel is up, none while it is
// not — so no other path can leave a leaf drawn for a panel that was hidden, or
// a panel flagged visible with nowhere to draw.
//
// The flag is scoped by shellLeafSurface: a split terminal is a peer IN one
// workspace, not a plugin-wide preference that follows the sidebar around. The
// surface that opened the split owns it, and a selection that lands anywhere
// else releases it (releaseShellLeafOffSurface) before the tree is rebuilt — so
// a workspace nobody split is never badged, never persisted with a shell leaf,
// and never shows another workspace's session for a frame.

const (
	// shellSplitDefaultRatio is the primary terminal's share of a freshly opened
	// shell split. A split's ratio is its FIRST child's, and the first child is
	// the primary terminal, so this is the complement of the panel's own half:
	// stating it the other way round is what would put every panel on the far
	// side of its divider.
	shellSplitDefaultRatio = 50

	// shellSplitDefaultAxis is where a shell split opens when nothing has been
	// remembered: below the primary terminal, which is where the panel opened
	// before it was a leaf.
	shellSplitDefaultAxis = SplitRows

	// shellCapMessage is what a refused third live terminal says. The cap is a
	// rule the user can see and act on, not a click that quietly does nothing.
	// It is the toast a PROGRAMMATIC caller gets — ctrl+t, a CLI open — because
	// those arrive with no modal to state the rule in first.
	shellCapMessage = "Two live terminals at a time; close one first"

	// shellCapDisabledReason is the same rule said where the user is about to
	// break it: one line under the create modal's disabled Terminal split row.
	shellCapDisabledReason = "Two terminals are already on screen — close one first"
)

// terminalSplitDisabledReason is why the create modal must refuse a terminal
// split, or empty when it may offer one. It reads the same cap openShellLeaf
// enforces, so the modal and the refusal cannot disagree; asking it earlier is
// what turns a post-click toast into a row the user can see is unavailable.
func (p *Plugin) terminalSplitDisabledReason() string {
	if !terminalPanelEnabled() {
		return ""
	}
	if p.termPanelVisible || panelayout.LiveCapReached(p.paneRoot) {
		return shellCapDisabledReason
	}
	return ""
}

// shellLeaf is the terminal panel's leaf, or nil when the panel is not in the
// tree.
func (p *Plugin) shellLeaf() *PaneNode { return firstPaneLeafOfKind(p.paneRoot, PaneShell) }

// shellSplitID is the split node dividing the primary terminal from the shell
// leaf. Zero when there is no shell leaf.
func (p *Plugin) shellSplitID() int {
	leaf := p.shellLeaf()
	if leaf == nil {
		return 0
	}
	return parentSplitID(p.paneRoot, leaf.ID)
}

// shellSplitIsColumns reports that the panel sits beside the primary terminal
// rather than below it. It reads the tree, which is the only place the axis
// lives now.
func (p *Plugin) shellSplitIsColumns() bool {
	split := FindPane(p.paneRoot, p.shellSplitID())
	return split != nil && split.Split != nil && split.Split.Axis == SplitCols
}

// rememberShellSplit records the live split's axis and ratio as the shape the
// next ctrl+t opens at. A drag that moves the divider is a preference, not a
// one-off.
func (p *Plugin) rememberShellSplit() {
	split := FindPane(p.paneRoot, p.shellSplitID())
	if split == nil || split.Split == nil {
		return
	}
	p.shellSplitAxis = split.Split.Axis
	p.shellSplitRatio = clampPaneRatio(split.Split.Ratio)
}

// shellSplitShape is the axis and ratio a shell split opens at: what was last
// remembered, or the defaults.
func (p *Plugin) shellSplitShape() (SplitAxis, int) {
	axis := p.shellSplitAxis
	if axis != SplitCols && axis != SplitRows {
		axis = shellSplitDefaultAxis
	}
	ratio := p.shellSplitRatio
	if ratio <= 0 {
		ratio = shellSplitDefaultRatio
	}
	return axis, clampPaneRatio(ratio)
}

// syncShellLeaf makes the pane tree agree with termPanelVisible. It is called
// wherever that flag moves and wherever the tree is rebuilt, so the two cannot
// drift apart for a frame.
//
// It reports whether the tree changed, which is a caller's cue that terminal
// geometry moved.
func (p *Plugin) syncShellLeaf() bool {
	if p.paneRoot == nil {
		return false
	}
	p.releaseShellLeafOffSurface()
	leaf := p.shellLeaf()
	switch {
	case p.termPanelVisible && leaf == nil:
		return p.openShellLeaf()
	case !p.termPanelVisible && leaf != nil:
		p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, leaf.ID)
		return true
	}
	return false
}

// claimShellLeafSurface records the workspace a split terminal is being opened
// on. It is the ownership half of termPanelVisible and moves with it: opening
// without claiming would leave the split adoptable by whatever the sidebar
// lands on next.
func (p *Plugin) claimShellLeafSurface() {
	_, surface, ok := p.selectedTerminalSurface()
	if !ok {
		surface = ""
	}
	p.shellLeafSurface = surface
}

// releaseShellLeafOffSurface drops the split when the selection is no longer
// the workspace that owns it. A split terminal belongs to its workspace
// (settled decision 6), so it neither follows the selection nor opens itself on
// a workspace the user never split; the owning workspace's persisted layout
// brings it back when the selection returns.
//
// A claim that was never made — a legacy panel preference restored at startup,
// or a layout decoded before the surface was known — adopts the current
// selection rather than being thrown away, which is what the pre-ownership
// build did with every split.
func (p *Plugin) releaseShellLeafOffSurface() {
	if !p.termPanelVisible {
		p.shellLeafSurface = ""
		return
	}
	_, surface, ok := p.selectedTerminalSurface()
	if !ok || surface == "" {
		return
	}
	if p.shellLeafSurface == "" {
		p.shellLeafSurface = surface
		return
	}
	if surface == p.shellLeafSurface {
		return
	}
	p.termPanelVisible = false
	p.termPanelFocused = false
	p.shellLeafSurface = ""
	p.forgetShellLeafName()
}

// shellLeafOwnsSelection reports that the open split terminal is the selected
// workspace's own.
func (p *Plugin) shellLeafOwnsSelection() bool {
	if !p.termPanelVisible {
		return false
	}
	_, surface, ok := p.selectedTerminalSurface()
	return ok && surface != "" && (p.shellLeafSurface == "" || surface == p.shellLeafSurface)
}

// shellSessionSelector picks a shell leaf's durable tmux target: the selector
// persisted with the leaf when it names a session, else the one derived from the
// current selection. A tmux pane id is never durable — the server reassigns
// them — so anything that is not a session name of ours falls back to derived.
func shellSessionSelector(persisted, derived string) string {
	persisted = strings.TrimSpace(persisted)
	if strings.HasPrefix(persisted, termPanelSessionPrefix) {
		return persisted
	}
	return derived
}

// shellCloseNeedsConfirm reports that closing a shell leaf must ask first,
// because something other than the login shell is running in it. It is a
// state-free rule so a headless caller could adopt it unchanged: currentCommand
// is tmux's pane_current_command, shellCommand the session's own shell.
func shellCloseNeedsConfirm(currentCommand, shellCommand string) bool {
	current := strings.TrimSpace(currentCommand)
	if current == "" {
		return false
	}
	shell := strings.TrimSpace(shellCommand)
	if shell == "" {
		return true
	}
	return !strings.EqualFold(baseCommand(current), baseCommand(shell))
}

func baseCommand(command string) string {
	if idx := strings.LastIndexByte(command, '/'); idx >= 0 {
		command = command[idx+1:]
	}
	return strings.TrimPrefix(command, "-")
}

// shellSplitPlacement is where the next shell split lands, in the `--split`
// vocabulary the create modal's placement buttons share with the CLI: "auto"
// (or empty) follows panelayout's auto rules, "right"/"below" override the
// axis. It is set immediately before the flag that opens the leaf, and
// openShellLeaf consumes it, so no two paths can disagree about placement.
func (p *Plugin) setShellSplitPlacement(placement string) {
	p.shellSplitPlacement = placement
}

// shellLeafOpenPlan answers where a shell split goes. With no placement asked
// for, it is the remembered shape on the primary terminal — what ctrl+t has
// always done. With one, it is panelayout's auto plan, axis-overridden by the
// same function `sidecar open --split` uses.
func (p *Plugin) shellLeafOpenPlan(placement string) (panelayout.OpenPlan, int, bool) {
	axis, ratio := p.shellSplitShape()
	if placement == "" {
		terminal := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
		if terminal == nil {
			return panelayout.OpenPlan{}, 0, false
		}
		return panelayout.OpenPlan{Split: terminal.ID, Axis: axis}, ratio, true
	}
	plan, ok := planPaneOpen(p.paneRoot, PaneShell, p.lastPaneBoxes())
	if !ok {
		return panelayout.OpenPlan{}, 0, false
	}
	return panelayout.ApplyAxisOverride(plan, placement), shellSplitDefaultRatio, true
}

// openShellLeaf splits the tree with a Shell leaf where shellLeafOpenPlan says.
// Two things are refused rather than squeezed or hidden: a third live terminal
// on screen (panelayout.LiveLeafCap), and a split the viewport cannot hold
// (Law 2). Both turn the panel back off so no state claims a leaf that is not
// there, and both say why in a toast — a refusal nobody can see is a bug
// report.
func (p *Plugin) openShellLeaf() bool {
	placement := strings.TrimSpace(p.shellSplitPlacement)
	p.shellSplitPlacement = ""
	if panelayout.LiveCapReached(p.paneRoot) {
		p.abandonShellLeaf()
		p.toastMessage = shellCapMessage
		p.toastTime = time.Now()
		return false
	}
	plan, ratio, planned := p.shellLeafOpenPlan(placement)
	peer, placed := p.previewPeerBox()
	if !planned || !placed {
		p.abandonShellLeaf()
		return false
	}
	trial, _ := SplitLeaf(clonePaneTree(p.paneRoot), plan.Split, plan.Axis, &PaneNode{Kind: PaneShell})
	if _, _, fits := LayoutPanes(trial, peer, paneTreeFloors()); !fits {
		p.abandonShellLeaf()
		return false
	}
	node := &PaneNode{Kind: PaneShell}
	root, focus := SplitLeaf(p.paneRoot, plan.Split, plan.Axis, node)
	if focus != node.ID {
		p.abandonShellLeaf()
		return false
	}
	p.paneRoot = root
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	SetRatio(p.paneRoot, p.shellSplitID(), ratio)
	// Focus follows the panel the same way it did before the panel was a leaf:
	// termPanelFocused says which terminal has the keyboard, and paneFocus stays
	// on the terminal leaf the ring names.
	p.paneFocus = terminalLeafID(p.paneRoot)
	return true
}

// abandonShellLeaf is every refusal's exit: the flag, its focus, and its
// ownership go together. Leaving termPanelFocused set behind a refused split is
// how a surface ends up with no leaf drawing focused chrome.
func (p *Plugin) abandonShellLeaf() {
	p.termPanelVisible = false
	p.termPanelFocused = false
	p.shellLeafSurface = ""
}

// createTerminalSplit is the create modal's Terminal split row: a live terminal
// peer of this workspace's own, placed where the modal's placement row asked.
// A split that is already open is not opened twice — a second leaf would draw a
// session that is already on screen — and a refused split explains itself in
// openShellLeaf's toast.
func (p *Plugin) createTerminalSplit(name, placement string) tea.Cmd {
	if !terminalPanelEnabled() {
		return nil
	}
	if p.termPanelVisible {
		p.shellLeafName = strings.TrimSpace(name)
		p.toastMessage = shellCapMessage
		p.toastTime = time.Now()
		return nil
	}
	p.shellLeafName = strings.TrimSpace(name)
	p.setShellSplitPlacement(placement)
	return p.toggleTermPanel()
}

// closeShellLeaf collapses the split terminal and hands the keyboard back to
// the primary terminal. It is the one exit every close takes — the header ✕,
// the confirm modal's Close, and a session that ended under the user — so the
// three flags that decide who owns the keyboard (termPanelVisible, its focus,
// and the surface claim) can never be left disagreeing with the tree. Leaving
// any of them set is exactly the wedge that made "exit" cost a restart.
//
// sessionGone says the tmux session is already dead: its buffer and pane id go
// with the leaf, because reattaching to it would only reopen an empty pane.
func (p *Plugin) closeShellLeaf(sessionGone bool) tea.Cmd {
	if !p.termPanelVisible && p.shellLeaf() == nil {
		return nil
	}
	if p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel {
		p.exitInteractiveMode()
	}
	// The shape it was closed at is what the next ctrl+t opens at.
	p.rememberShellSplit()
	p.termPanelVisible = false
	p.termPanelFocused = false
	p.shellLeafSurface = ""
	p.termPanelScroll = 0
	p.forgetShellLeafName()
	p.syncShellLeaf()
	if sessionGone {
		p.cleanupTermPanelSession()
	}
	// Focus returns to the primary terminal rather than to nothing: a surface
	// whose only live leaf is unfocusable is a surface with no keyboard.
	p.activePane = PanePreview
	if id := terminalLeafID(p.paneRoot); id != 0 {
		p.paneFocus = id
	}
	p.saveSelectionState()
	return p.resizeSelectedPaneCmd()
}

// noteShellLeafSessionEnded is the split terminal's own session-end signal. The
// primary terminal's end is noteSessionEnded's — the surface keeps its leaf and
// says so — but a split has no sidebar row to go back to, so its leaf closes.
// No confirm: the process the confirm would ask about has already ended.
func (p *Plugin) noteShellLeafSessionEnded() tea.Cmd {
	// The shared notice runs first and unchanged: it leaves interactive mode and
	// raises shell-death suspicion. Closing first would let leaveInteractiveMode
	// put termPanelFocused back on a leaf that is gone.
	shared := p.noteSessionEnded()
	if !p.termPanelVisible {
		return shared
	}
	return tea.Batch(shared, p.closeShellLeaf(true))
}

// shellLeafTitle is what the shell leaf's header calls it: the name the create
// modal gave it, or the auto-name the modal would have prefilled — the split's
// own workspace — so an unnamed split reads the same either way.
func (p *Plugin) shellLeafTitle() string {
	if name := strings.TrimSpace(p.shellLeafName); name != "" {
		return name
	}
	return p.terminalSplitAutoName()
}

// forgetShellLeafName drops a name that belonged to a workspace the split has
// left. The leaf follows the selection onto that workspace's own session, so
// carrying the previous workspace's label over would title one workspace's
// terminal with another's name.
func (p *Plugin) forgetShellLeafName() {
	p.shellLeafName = ""
}

// shellLeafBox is the panel's INNER box — header row plus viewport, inside its
// own chrome — or !ok when the panel is not on screen at this size. A layout
// that had to zoom answers with the zoomed leaf alone, so a panel the frame did
// not draw has no box here either.
func (p *Plugin) shellLeafBox() (Box, bool) {
	leaf := p.shellLeaf()
	if leaf == nil {
		return Box{}, false
	}
	geom, ok := p.leafGeometryFor(leaf.ID)
	if !ok || geom.Inner.W <= 0 || geom.Inner.H <= 0 {
		return Box{}, false
	}
	return geom.Inner, true
}

// terminalSlotBox is one terminal surface's inner box, selected by the same
// bool every terminal path is parameterized by. ok is false when that surface is
// not on screen.
func (p *Plugin) terminalSlotBox(termPanel bool) (Box, bool) {
	if termPanel {
		return p.shellLeafBox()
	}
	box, ok := p.terminalLeafBox()
	if !ok || box.W <= 0 || box.H <= 0 {
		return Box{}, false
	}
	return box, true
}

// terminalSlotSize is the tmux size for a slot's box: the box less the one
// header row every embedded terminal spends.
func terminalSlotSize(box Box) (width, height int) {
	return box.W, max(box.H-terminalHeaderRows, 1)
}

// parentSplitID names the split node one leaf hangs from, or zero for a leaf
// that is the whole tree.
func parentSplitID(node *PaneNode, leafID int) int {
	if node == nil || node.Split == nil {
		return 0
	}
	if childLeafID(node.Split.A) == leafID || childLeafID(node.Split.B) == leafID {
		return node.ID
	}
	if id := parentSplitID(node.Split.A, leafID); id != 0 {
		return id
	}
	return parentSplitID(node.Split.B, leafID)
}

func childLeafID(node *PaneNode) int {
	if node == nil || node.Split != nil {
		return 0
	}
	return node.ID
}
