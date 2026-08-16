package filebrowser

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/livewatch"
)

// The Files preview already reloaded on a filesystem change before this file
// existed: TreeWatcher reports PreviewChanged for the single previewed path and
// handleWatchEvent issues a load. Two things were missing, and both are named
// in td-03c21c.
//
// The first is the no-change gate. Every reload was applied, and
// applyPreviewResult clears the selection and drops the rendered markdown, so a
// tool that rewrites a file without changing it — a formatter with nothing to
// do, a save with no edits, a build touching an output path — visibly wiped the
// user's selection and re-rendered the pane. That is now gated on the content
// actually differing.
//
// The second is the inline editor. Its buffer lives in a real editor in a tmux
// session, not in this process, so a reload could never destroy unsaved text —
// but the editor is what the preview pane is drawing while a session is live,
// and reloading underneath it churns state for a pane nobody can see. Vim in
// particular writes a probe file, a swap file and a backup for every save. The
// refresh is suppressed for the duration and lands when the editor exits.

// previewRefreshSuppressed reports whether a preview reload must be held back.
//
// A suppressed change is not lost. The Refresher keeps it owed, and
// refreshPreview is re-offered on the next watcher signal and on editor exit,
// so the pane catches up as soon as it is safe.
func (p *Plugin) previewRefreshSuppressed() bool {
	// The inline editor owns the pane and is itself the thing writing the file.
	if p.inlineEditMode {
		return true
	}
	// An overlay is drawn over the preview; reloading behind it buys nothing and
	// would rebuild state the overlay is reading.
	return p.infoMode || p.blameMode
}

// refreshPreview re-reads the previewed file if a change is owed and nothing is
// suppressing it.
func (p *Plugin) refreshPreview() tea.Cmd {
	if p.previewFile == "" || p.ctx == nil {
		return nil
	}
	p.previewLive.Observe()
	if !p.previewLive.Begin(p.previewRefreshSuppressed()) {
		return nil
	}
	path, workDir, epoch := p.previewFile, p.ctx.WorkDir, p.ctx.Epoch
	load := LoadPreview(workDir, path, epoch)
	return func() tea.Msg {
		msg, ok := load().(PreviewLoadedMsg)
		if !ok {
			return nil
		}
		return previewRefreshedMsg{Path: path, Epoch: epoch, Result: msg.Result}
	}
}

// previewRefreshedMsg is a watcher-driven re-read of the previewed file. It is
// deliberately a distinct type from PreviewLoadedMsg: a refresh must not be
// mistaken for a navigation, which resets scroll and consumes the pending
// jump-to-line.
type previewRefreshedMsg struct {
	Path   string
	Epoch  uint64
	Result filepreview.PreviewResult
}

// GetEpoch implements plugin.EpochMessage.
func (m previewRefreshedMsg) GetEpoch() uint64 { return m.Epoch }

// applyPreviewRefresh installs a watcher-driven re-read.
//
// Scroll is preserved and then clamped, which is what "preserve where the
// content still supports it" means: a file that shrank pulls the viewport back
// to the new end rather than showing blank space, and a file that grew leaves
// the reader exactly where they were.
func (p *Plugin) applyPreviewRefresh(msg previewRefreshedMsg) tea.Cmd {
	stillOwed := p.previewLive.Done()
	var followUp tea.Cmd
	if stillOwed {
		followUp = p.refreshPreview()
	}

	// The user navigated to a different file while this read was running.
	if msg.Path != p.previewFile {
		return followUp
	}
	// A file caught mid-write reads as empty or truncated. Holding the last good
	// content for the moment it takes the writer to finish beats flickering
	// through nothing; the write that follows produces another signal.
	if msg.Result.Error != nil {
		return followUp
	}
	if !p.previewLive.Changed(fingerprintPreview(msg.Result)) {
		return followUp
	}

	scroll := p.previewScroll
	p.applyPreviewResult(msg.Result)
	p.updateActiveTabResult(msg.Result)
	p.previewScroll = scroll
	p.clampPreviewScroll()
	if p.activeTab >= 0 && p.activeTab < len(p.tabs) {
		p.tabs[p.activeTab].Scroll = p.previewScroll
	}
	// A content search is showing match positions computed against the old text.
	if p.contentSearchMode && p.contentSearchQuery != "" {
		p.updateContentMatches()
	}
	return followUp
}

// adoptPreviewFingerprint records what an explicit load put on screen, so the
// first watcher signal after a navigation is measured against it rather than
// reporting the file as changed.
func (p *Plugin) adoptPreviewFingerprint(result filepreview.PreviewResult) {
	p.previewLive.Reset()
	p.previewLive.Adopt(fingerprintPreview(result))
}

// fingerprintPreview reduces a loaded preview to a change detector. Content
// alone is not enough: an image carries none, and truncation or a switch to
// binary changes what is drawn without changing the visible text.
func fingerprintPreview(r filepreview.PreviewResult) string {
	return livewatch.Fingerprint(struct {
		Content   string
		Binary    bool
		Image     bool
		Truncated bool
		Size      int64
		ModTime   int64
	}{
		Content:   r.Content,
		Binary:    r.IsBinary,
		Image:     r.IsImage,
		Truncated: r.IsTruncated,
		Size:      r.TotalSize,
		ModTime:   r.ModTime.UnixNano(),
	})
}
