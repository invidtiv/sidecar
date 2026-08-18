package docview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/livewatch"
)

// This file is the document pane's binding to internal/livewatch.
//
// The motivating case is an agent writing a markdown file while the user reads
// it. That makes scroll preservation the whole game: the reader is a third of
// the way down a document that just gained two paragraphs, and Load's
// unconditional `m.scroll = 0` would throw them back to line one on every save.
// Refresh keeps the offset and clamps it, so the position holds exactly as long
// as the document is still long enough to support it.
//
// Only the single previewed path is watched. Nothing here walks a tree.

// WatchTarget returns what to watch for changes to the document this model is
// showing, or an empty target when the model has no resolvable path.
//
// It is the file itself, not its directory: a document pane is open on one
// document, and the surrounding directory can be enormous. [livewatch] still
// registers the parent with the kernel, because atomic writers replace a file
// rather than writing through it, but only events naming this exact path are
// reported.
func (m *Model) WatchTarget() livewatch.Target {
	if m == nil || m.root == "" || m.path == "" {
		return livewatch.Target{}
	}
	return livewatch.File(filepath.Join(m.root, m.path))
}

// SetRoot records the directory that m.path is relative to.
//
// [Model.Load] takes it as an argument, but [Model.LoadFile] is handed an
// already-open file and never learns where it came from. A host that loads by
// file descriptor must call this, or the pane cannot be refreshed — it has no
// path to re-read.
func (m *Model) SetRoot(root string) {
	if m == nil {
		return
	}
	m.root = root
}

// Observe records that the document's file changed on disk.
//
// A document that has never loaded declines to be owed a re-read: the host's own
// load path owns it, [Model.Refresh] refuses before it reaches the refresher,
// and a dirty flag nothing can clear reads to a host as a refresh owed forever.
func (m *Model) Observe() {
	if m == nil || m.requestGeneration == 0 {
		return
	}
	m.live.Observe()
}

// Refresh returns a command that re-reads the document in place, or nil when no
// re-read is owed.
//
// Unlike [Model.Load] it preserves the scroll offset, the render mode and the
// wrap setting, and it leaves the current text on screen while the re-read runs
// rather than flashing a loading placeholder.
//
// suppressed is the host's veto. A vetoed refresh stays owed and lands as soon
// as the veto lifts, so a host may safely block refreshes while something else
// owns the pane without losing the update.
func (m *Model) Refresh(suppressed bool) tea.Cmd {
	if m == nil || m.root == "" || m.path == "" {
		return nil
	}
	// Nothing loaded yet, or a load already in flight whose result the
	// generation bump below would discard. The host's own load path owns both.
	if m.requestGeneration == 0 || m.loading {
		return nil
	}
	if !m.live.Begin(suppressed) {
		return nil
	}

	m.requestGeneration++
	generation := m.requestGeneration
	modelID, epoch, relPath := m.modelID, m.epoch, m.path
	load := filepreview.LoadPreview(m.root, relPath, epoch)
	return func() tea.Msg {
		msg, ok := load().(filepreview.PreviewLoadedMsg)
		if !ok {
			return nil
		}
		return LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: epoch,
			Path: relPath, Result: msg.Result, Refresh: true,
		}
	}
}

// RefreshPending reports whether a re-read is owed but has not started.
func (m *Model) RefreshPending() bool {
	if m == nil {
		return false
	}
	return m.live.Pending()
}

// applyRefresh handles a result produced by [Model.Refresh], reporting whether
// the document changed and therefore needs repainting.
//
// A re-read that produced identical bytes is dropped. Editors and formatters
// touch a file more often than they change it — a save with no edits, a
// chmod-adjacent rewrite, a tool that writes the same content back — and each
// of those would otherwise cost a visible repaint of the pane the user is
// reading.
//
// A failed re-read is dropped too. A file caught mid-write is briefly empty or
// truncated, and rendering that flicker of nothing would be worse than holding
// the last good content for the moment it takes the writer to finish; the next
// signal picks up the finished file.
func (m *Model) applyRefresh(msg LoadedMsg) bool {
	stillOwed := m.live.Done()
	defer func() {
		if stillOwed {
			m.live.Observe()
		}
	}()

	if msg.Result.Error != nil {
		return false
	}
	if !m.live.Changed(fingerprintResult(msg.Result)) {
		return false
	}

	m.loading = false
	m.result = msg.Result
	m.invalidateRender()
	// Scroll, rendered and wrap deliberately survive. clampScroll is what makes
	// "preserve where the content still supports it" true: a document that lost
	// most of its length pulls the viewport back to the new end instead of
	// showing blank space.
	m.clampScroll()
	return true
}

// fingerprintResult reduces a loaded preview to a change detector.
//
// Content alone is not enough: an image preview carries no content at all, and
// a file that becomes binary or is truncated at the preview cap renders
// differently with the same visible text. Size and modification time cover
// those without reading anything extra, since the loader already stat-ed.
func fingerprintResult(r filepreview.PreviewResult) string {
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
