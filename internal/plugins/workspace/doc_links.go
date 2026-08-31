package workspace

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// docContentLinkHit is one scanned span in a document leaf. It is registered
// on the body after chrome so a tab or close click cannot land on it.
type docContentLinkHit struct {
	LeafID int
	Ref    contentlink.Ref
	Rect   mouse.Rect
}

type docLinkResolvedMsg struct {
	Candidate contentlink.Pending
	Ref       contentlink.Ref
	Found     bool
}

func (p *Plugin) ensureDocLinkResolution() *contentlink.ResolutionIndex {
	if p.docLinkResolution == nil {
		p.docLinkResolution = contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions)
	}
	return p.docLinkResolution
}

func (p *Plugin) decorateDocBody(doc *docPane, body string) string {
	if doc == nil || doc.editing() || doc.mode != nil {
		return body
	}
	view := doc.view()
	if view == nil || !view.ContentLinksSafe() {
		return body
	}
	frame := view.ScanContentLinks(body, contentlink.FrameOptions{
		Ready:        p.ensureDocLinkResolution().Snapshot(),
		Matchers:     p.resourceMatchers,
		AllowedKinds: docview.ContentLinkKinds(),
		Decorate:     true,
	})
	for _, hit := range frame.Hits {
		p.docLinkHits = append(p.docLinkHits, docContentLinkHit{LeafID: doc.leafID, Ref: hit.Ref, Rect: hit.Rect})
	}
	for _, candidate := range frame.Pending {
		p.queueDocLinkResolve(doc.root, candidate)
	}
	return frame.Output
}

func (p *Plugin) queueDocLinkResolve(root string, candidate contentlink.Pending) {
	if p.docLinkPending == nil {
		p.docLinkPending = make(map[contentlink.Pending]bool)
	}
	if p.docLinkPending[candidate] {
		return
	}
	p.docLinkPending[candidate] = true
	terminalperf.Record(terminalperf.DocumentResolutionRequest)
	p.paneSizeCmds = append(p.paneSizeCmds, resolveDocContentLink(root, candidate))
}

func resolveDocContentLink(root string, candidate contentlink.Pending) tea.Cmd {
	return func() tea.Msg {
		msg := docLinkResolvedMsg{Candidate: candidate}
		switch candidate.Kind {
		case contentlink.KindFile:
			rel, _, ok := terminallink.ResolveFile(root, candidate.Raw)
			msg.Ref, msg.Found = contentlink.Ref{Kind: contentlink.KindFile, Value: rel}, ok
		case contentlink.KindDiff:
			target, ok := workspacediff.ParseSpec(candidate.Raw)
			if !ok {
				return msg
			}
			resolved, err := workspacediff.ResolveSpec(context.Background(), root, target)
			msg.Ref, msg.Found = contentlink.Ref{Kind: contentlink.KindDiff, Value: resolved.Identity()}, err == nil
		}
		return msg
	}
}

func (p *Plugin) applyDocLinkResolved(msg docLinkResolvedMsg) {
	if p.docLinkPending != nil {
		delete(p.docLinkPending, msg.Candidate)
	}
	p.ensureDocLinkResolution().Put(msg.Candidate, msg.Ref, msg.Found)
}

func (p *Plugin) registerDocLinkHits(node *PaneNode, inner Box) {
	if node == nil || node.Kind != PaneDoc {
		return
	}
	for _, hit := range p.docLinkHits {
		if hit.LeafID != node.ID || hit.Rect.W <= 0 || hit.Rect.H <= 0 {
			continue
		}
		// Body is already below the header in origin math; skip anything that
		// still landed on the tab strip so a path in the first source row
		// cannot steal a tab click.
		if hit.Rect.Y <= inner.Y {
			continue
		}
		p.mouseHandler.HitMap.AddRect(regionDocLink, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit)
	}
}

func (p *Plugin) activateDocContentLink(hit docContentLinkHit) tea.Cmd {
	leaf := FindPane(p.paneRoot, hit.LeafID)
	if leaf == nil || leaf.Kind != PaneDoc {
		return nil
	}
	doc := p.docs[leaf.ContentID]
	return p.activateDocContentRef(doc, hit.Ref)
}

func (p *Plugin) activateDocContentRef(doc *docPane, ref contentlink.Ref) tea.Cmd {
	if doc == nil || p.paneRoot == nil {
		return nil
	}
	switch ref.Kind {
	case contentlink.KindURL:
		return openInBrowser(ref.Value)
	case contentlink.KindFile:
		return p.openWorkspaceContent(doc.root, doc.surface, ref, "Document")
	case contentlink.KindIssue:
		return p.openWorkspaceContent(doc.root, doc.surface, ref, "Issue")
	case contentlink.KindDiff:
		return p.openWorkspaceContent(doc.root, doc.surface, ref, "Diff")
	case contentlink.KindResource:
		return p.openWorkspaceContent(doc.root, doc.surface, ref, "Resource")
	default:
		return nil
	}
}

func (p *Plugin) clearDocLinkHits() {
	p.docLinkHits = p.docLinkHits[:0]
}
