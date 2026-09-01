package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
)

// terminalLinkResolver is the app-owned filesystem/Git adapter. Hosts supply
// their bounded canonical surface roots, so a visible batch does not
// EvalSymlinks the same base once per candidate. Remote KindFile pending
// work is delegated; it must not ResolveFile the viewer's twin path.
type terminalLinkResolver struct {
	remoteFile func(hostID, root string, candidate contentlink.Pending) (contentlink.Ref, bool)
}

func newTerminalLinkCoordinator(remoteFile func(hostID, root string, candidate contentlink.Pending) (contentlink.Ref, bool)) termpreview.LinkCoordinator {
	resolver := &terminalLinkResolver{remoteFile: remoteFile}
	return termpreview.NewLinkCoordinator(resolver)
}

func (r *terminalLinkResolver) Resolve(req termpreview.LinkResolveRequest) (contentlink.Ref, bool) {
	if req.HostID != "" {
		return r.resolveRemote(req.HostID, req.Root, req.Candidate)
	}
	return r.resolveCanonical(filepath.Clean(req.Root), req.Candidate)
}

func (r *terminalLinkResolver) ResolveFresh(request termpreview.FreshLinkRequest) (contentlink.Ref, bool) {
	if request.HostID != "" {
		return r.resolveRemote(request.HostID, request.Root, request.Candidate)
	}
	raw := request.RawRoot
	if raw == "" {
		raw = request.Root
	}
	canonical, err := filepath.EvalSymlinks(raw)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(request.Root) {
		return contentlink.Ref{}, false
	}
	return r.resolveCanonical(filepath.Clean(canonical), request.Candidate)
}

func (r *terminalLinkResolver) resolveRemote(hostID, root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
	if r == nil || r.remoteFile == nil || candidate.Kind != contentlink.KindFile {
		return contentlink.Ref{}, false
	}
	return r.remoteFile(hostID, root, candidate)
}

func (r *terminalLinkResolver) resolveCanonical(root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
	switch candidate.Kind {
	case contentlink.KindFile:
		display, _, ok := terminallink.ResolveFileFromCanonicalBase(root, candidate.Raw)
		return contentlink.Ref{Kind: candidate.Kind, Value: display}, ok
	case contentlink.KindDiff:
		value, _, ok := terminallink.ResolveGitSpec(root, candidate.Raw)
		return contentlink.Ref{Kind: candidate.Kind, Value: value}, ok
	default:
		return contentlink.Ref{}, false
	}
}

type terminalLinkHost interface {
	SetTerminalLinkCoordinator(termpreview.LinkCoordinator)
	PrepareTerminalLinks()
}

func (m *Model) injectTerminalLinkCoordinator() {
	if m == nil || m.terminalLinks == nil || m.registry == nil {
		return
	}
	for _, p := range m.registry.Plugins() {
		if host, ok := p.(terminalLinkHost); ok {
			host.SetTerminalLinkCoordinator(m.terminalLinks)
		}
	}
}

func (m *Model) prepareTerminalLinks() {
	if m == nil || m.terminalLinks == nil {
		return
	}
	if m.overview != nil {
		m.overview.PrepareTerminalLinks()
	}
	if m.registry != nil {
		for _, p := range m.registry.Plugins() {
			if host, ok := p.(terminalLinkHost); ok {
				host.PrepareTerminalLinks()
			}
		}
	}
}

func (m *Model) terminalLinkCmd() tea.Cmd {
	if m == nil || m.terminalLinks == nil {
		return nil
	}
	return m.terminalLinks.TakeCmd()
}
