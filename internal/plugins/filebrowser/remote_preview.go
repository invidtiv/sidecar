package filebrowser

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/filepreview"
)

// Reading a previewed file from whichever machine owns it.
//
// The remote adapter produces the same filepreview.PreviewLoadedMsg the local
// read produces, because contentpanes.DocumentReadResult.Value already is a
// filepreview.PreviewResult. Highlighting, wrapping, the scrollbar, search,
// and every keybinding above this therefore do not change and cannot drift:
// there is one preview pipeline with two producers, not two pipelines.

// remotePreviewTimeout bounds one document read. Short, because the read
// answers a keypress the user is waiting on.
const remotePreviewTimeout = 20 * time.Second

// loadPreview reads path for the preview pane. Local reads this machine's
// disk; a bound surface reads the host and never falls through to a same-named
// path here.
func (p *Plugin) loadPreview(path string) tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	if !p.remoteBound() {
		return LoadPreview(p.ctx.WorkDir, path, p.ctx.Epoch)
	}
	if !p.remoteAvailable() {
		return nil
	}
	return p.loadRemotePreview(path, p.ctx.Epoch)
}

func (p *Plugin) loadRemotePreview(path string, epoch uint64) tea.Cmd {
	src := p.contentSource()
	if src == nil {
		return nil
	}
	srcCtx := p.remoteSourceContext()
	revision := p.heldPreviewRevision(path)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remotePreviewTimeout)
		defer cancel()
		result, err := src.LoadDocument(ctx, srcCtx, contentpanes.DocumentReadRequest{
			Ref:        contentlink.Ref{Kind: contentlink.KindFile, Value: path},
			IfRevision: revision,
		})
		if err != nil {
			return filepreview.PreviewLoadedMsg{Epoch: epoch, Path: path, Result: filepreview.PreviewResult{Error: err}}
		}
		if result.NotModified {
			// The pane already holds these bytes. Saying so costs one round
			// trip and no repaint, which is the whole point of IfRevision.
			return remotePreviewUnchangedMsg{Epoch: epoch, Path: path, Revision: result.Revision}
		}
		return remotePreviewLoadedMsg{
			Msg:      filepreview.PreviewLoadedMsg{Epoch: epoch, Path: path, Result: result.Value},
			Revision: result.Revision,
		}
	}
}

// remotePreviewLoadedMsg carries the host's revision alongside the payload the
// preview pane consumes, so the next read of the same file can be conditional.
type remotePreviewLoadedMsg struct {
	Msg      filepreview.PreviewLoadedMsg
	Revision string
}

// GetEpoch implements plugin.EpochMessage so a read from the project the user
// just left is dropped like every other stale message.
func (m remotePreviewLoadedMsg) GetEpoch() uint64 { return m.Msg.Epoch }

// remotePreviewUnchangedMsg is a conditional read that had nothing to send.
type remotePreviewUnchangedMsg struct {
	Epoch    uint64
	Path     string
	Revision string
}

// GetEpoch implements plugin.EpochMessage.
func (m remotePreviewUnchangedMsg) GetEpoch() uint64 { return m.Epoch }

// heldPreviewRevision is the revision to make a read of path conditional
// against: the host's revision for the bytes the pane is holding for that same
// file, and otherwise nothing. Asking conditionally about bytes this pane no
// longer has earns a NotModified answer it cannot render.
func (p *Plugin) heldPreviewRevision(path string) string {
	if path == "" || path != p.previewRevisionPath {
		return ""
	}
	return p.previewRevision
}

// rememberPreviewRevision records the revision of the bytes just installed in
// the preview pane. Callers invoke it after the payload lands, never before.
func (p *Plugin) rememberPreviewRevision(path, revision string) {
	if path == "" || revision == "" {
		return
	}
	p.previewRevisionPath = path
	p.previewRevision = revision
}

// forgetPreviewRevision drops the remembered revision because the bytes it
// described are no longer on screen.
func (p *Plugin) forgetPreviewRevision() {
	p.previewRevisionPath = ""
	p.previewRevision = ""
}

// contentSource is the host document adapter, or nil when this surface is
// local or cannot reach its host.
func (p *Plugin) contentSource() contentpanes.Source {
	if p.contentSourceOverride != nil {
		return p.contentSourceOverride
	}
	if !p.remoteAvailable() || p.ctx.RemoteRunner == nil {
		return nil
	}
	return contentpanes.NewRemoteSource(p.ctx.HostID, p.hostVerbs(), p.ctx.RemoteRunner)
}

// remoteSourceContext is the identity a host resolves a read against.
func (p *Plugin) remoteSourceContext() contentpanes.SourceContext {
	if p.ctx == nil {
		return contentpanes.SourceContext{}
	}
	return contentpanes.SourceContext{
		Root:            p.remoteRoot(),
		HostID:          p.ctx.HostID,
		HostIncarnation: p.ctx.HostIncarnation,
		ProjectKey:      p.ctx.ProjectKey,
		ProjectRoot:     p.remoteRoot(),
		WorkspaceID:     p.remoteWorkspaceID(),
		WorkspaceKind:   "worktree",
		WorkspaceKey:    p.remoteRoot(),
	}
}

// remoteFileCandidates is the find-by-name index while bound: the host's own
// catalog, filtered and capped by the host's rules rather than re-derived from
// a tree this viewer has only partly expanded.
func (p *Plugin) remoteFileCandidates() ([]string, error) {
	src := p.contentSource()
	if src == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), remotePreviewTimeout)
	defer cancel()
	return remoteCatalogFiles(ctx, p.ctx.RemoteRunner, p.ctx.HostID, p.remoteWorkspaceID())
}

// scanRemoteCandidates is the filefind.Cache scanner for a bound surface. The
// root argument is the host's and is deliberately unused: identity is the
// durable workspace id, which the host re-resolves on every request.
//
// Directory candidates have no host verb. They exist for path auto-complete in
// the file operations, and those are refused while bound, so this says so
// rather than answering with this machine's directories.
func (p *Plugin) scanRemoteCandidates(_ string, dirs bool) ([]string, string) {
	if dirs {
		return nil, "directory suggestions are unavailable on [" + p.ctx.HostID + "]"
	}
	files, err := p.remoteFileCandidates()
	if err != nil {
		return nil, err.Error()
	}
	return files, ""
}
