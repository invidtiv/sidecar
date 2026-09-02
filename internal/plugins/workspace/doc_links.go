package workspace

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
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
	Result contentlink.ResolutionResult
}

func (p *Plugin) ensureDocLinkResolution() *contentlink.ResolutionIndex {
	if p.docLinkResolution == nil {
		p.docLinkResolution = contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions)
	}
	return p.docLinkResolution
}

func (p *Plugin) prepareDocFrame(doc *docPane) {
	if doc == nil {
		return
	}
	view := doc.view()
	if view == nil {
		return
	}
	index := p.ensureDocLinkResolution()
	frame := view.PrepareFrame(docview.PrepareOptions{
		Root:              doc.root,
		Resolution:        index.SnapshotForRoot(doc.root),
		Matchers:          p.resourceMatchers,
		MatcherGeneration: p.linkMatcherGeneration,
		AllowedKinds:      docview.ContentLinkKinds(),
		Decorate:          true,
		Links:             !doc.editing() && doc.mode == nil,
	})
	src := contentpanes.SourceContext{Root: doc.root}
	var source contentpanes.Source = contentpanes.LocalSource{}
	if p.contentDeck != nil {
		src = p.contentDeck.Context().Source
		source = p.contentDeck.ContentSource()
	}
	docview.BeginResolutions(index, doc.root, frame, func(request contentlink.ResolutionRequest) {
		p.paneSizeCmds = append(p.paneSizeCmds, resolveDocContentLink(source, src, request))
	})
}

func (p *Plugin) preparedDocBody(doc *docPane, originX, originY int) string {
	if doc == nil {
		return ""
	}
	view := doc.view()
	if view == nil {
		return ""
	}
	frame := view.PreparedFrame()
	if !frame.Valid() {
		return view.View()
	}
	frame.EachHitAt(originX, originY, func(hit docview.ContentLinkHit) {
		p.docLinkHits = append(p.docLinkHits, docContentLinkHit{LeafID: doc.leafID, Ref: hit.Ref, Rect: hit.Rect})
	})
	return frame.Output()
}

func resolveDocContentLink(source contentpanes.Source, src contentpanes.SourceContext, request contentlink.ResolutionRequest) tea.Cmd {
	return func() tea.Msg {
		result := contentlink.ResolutionResult{Request: request}
		switch request.Candidate.Kind {
		case contentlink.KindFile:
			if src.Root == "" {
				src.Root = request.Root
			}
			ref, err := contentpanes.ResolveDocument(source, src, request.Candidate)
			result.Ref, result.Found = ref, err == nil && ref.Value != ""
		case contentlink.KindDiff:
			target, ok := workspacediff.ParseSpec(request.Candidate.Raw)
			if !ok {
				return docLinkResolvedMsg{Result: result}
			}
			resolved, err := workspacediff.ResolveSpec(context.Background(), request.Root, target)
			result.Ref, result.Found = contentlink.Ref{Kind: contentlink.KindDiff, Value: resolved.Identity()}, err == nil
		}
		return docLinkResolvedMsg{Result: result}
	}
}

func (p *Plugin) applyDocLinkResolved(msg docLinkResolvedMsg) {
	p.ensureDocLinkResolution().Apply(msg.Result)
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
