package filebrowser

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/app"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectsearch"
	"github.com/marcus/sidecar/internal/tty"
)

// The frame memo.
//
// Every Bubble Tea message re-renders the whole application, so an idle Files
// tab used to rebuild both panes, their gutters, scrollbars and hit maps for a
// frame that was byte-identical to the one before it. The memo keeps the last
// rendered frame and returns it while it is still true.
//
// Validity is decided by explicit invalidation, never by a timer: a timer would
// hide a missed invalidation instead of surfacing it. Three rules keep that
// honest:
//
//  1. Every message the plugin actually handles marks the frame dirty at the
//     top of update(), before any handler runs. A message the plugin ignores
//     must not — that is the whole point — and the ignore path is the type
//     switch falling through to its final `return p, nil`, which mutates
//     nothing.
//  2. Every setter the app can call that changes what is drawn (focus, pane
//     focus, the focus ring, Init/Start/Stop) marks it dirty too — but only
//     when the value it is given actually changes something. The deck's
//     syncInnerFocus re-asserts pane focus on every pass with the value the
//     plugin already holds, so an unconditional invalidation there would cost
//     a rebuild on every frame and the memo would never hit.
//  3. State whose pixels come from tmux rather than from plugin state — the
//     inline editor and a terminal-graphics image preview — bypasses the memo
//     entirely, because it changes without any plugin-visible message.
//
// A cached frame carries its hit regions with it: renderView clears and
// re-registers p.mouseHandler during a render, so the regions on file are the
// ones belonging to the frame that is on screen. Frame and regions are one
// snapshot, and skipping the render keeps them together.

// invalidateView marks the memoized frame stale. The next View rebuilds.
func (p *Plugin) invalidateView() {
	p.viewDirty = true
}

// keepViewCache says the message just handled changed nothing that is drawn, so
// the memoized frame is still true. Only the held-wheel burst path uses it: the
// notch was absorbed into a pending delta rather than moving anything.
func (p *Plugin) keepViewCache() {
	p.reuseViewOnce = true
	p.viewDirty = false
}

// viewMemoBypassed reports state whose pixels do not come from plugin state and
// therefore can change without a message this plugin ever sees.
func (p *Plugin) viewMemoBypassed() bool {
	if p.edit.Active {
		return true
	}
	if p.edit.Model != nil && p.edit.Model.IsActive() {
		return true
	}
	// A terminal-graphics image is re-emitted by renderImagePreview each frame
	// and belongs to the host terminal's graphics state, not to ours.
	if p.isImage && p.previewFile != "" {
		return true
	}
	return false
}

// viewMemoHit reports whether the stored frame can be returned as-is.
func (p *Plugin) viewMemoHit(width, height int, bypassed bool) bool {
	return !bypassed && p.viewCacheOK && !p.viewDirty &&
		p.viewCacheW == width && p.viewCacheH == height
}

// viewInvalidatingMsg lists every message type update() acts on, including the
// ones it only acts on while the inline editor holds the surface. It must stay
// exactly in step with the case types of update()'s type switches;
// TestViewInvalidationCoversEveryHandledMessage compares the two lists in the
// source and fails when one of them grows a type the other does not have.
func viewInvalidatingMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case WatchStartedMsg,
		WatchEventMsg,
		treePreviewQuietMsg,
		tea.WindowSizeMsg,
		tea.MouseMsg,
		tea.KeyPressMsg,
		tty.EscapeTimerMsg,
		tty.CaptureResultMsg,
		tty.PollTickMsg,
		tty.PaneResizedMsg,
		tty.SessionDeadMsg,
		tty.PasteResultMsg,
		app.PluginFocusedMsg,
		appmsg.ThemeChangedMsg,
		TreeBuiltMsg,
		StateRestoredMsg,
		previewRefreshedMsg,
		plugin.HostInventoryMsg,
		remotePreviewLoadedMsg,
		remotePreviewUnchangedMsg,
		PreviewLoadedMsg,
		app.RefreshMsg,
		FileCacheBuiltMsg,
		NavigateToFileMsg,
		RevealErrorMsg,
		DragSpringLoadMsg,
		FileOpErrorMsg,
		FileOpSuccessMsg,
		DragMoveResultMsg,
		CreateSuccessMsg,
		DeleteSuccessMsg,
		PasteSuccessMsg,
		GitInfoMsg,
		BlameLoadedMsg,
		projectsearch.DebounceMsg,
		projectsearch.ResultsMsg,
		InlineEditStartedMsg,
		InlineEditExitedMsg,
		tea.PasteMsg:
		return true
	}
	return false
}
