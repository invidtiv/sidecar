package overview

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspacediff"
)

const previewDocLinkKind = "global-preview-doc-link"

// previewDocLinkHit is one scanned span in the global document preview.
type previewDocLinkHit struct {
	Ref  contentlink.Ref
	Rect mouse.Rect
}

type previewDocLinkResolvedMsg struct {
	Result contentlink.ResolutionResult
}

func (m *Model) ensurePreviewDocLinkResolution() *contentlink.ResolutionIndex {
	if m.preview.docLinkResolution == nil {
		m.preview.docLinkResolution = contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions)
	}
	return m.preview.docLinkResolution
}

func (m *Model) preparePreviewDocFrame(doc *previewDoc) tea.Cmd {
	if doc == nil {
		return nil
	}
	view := doc.view()
	if view == nil {
		return nil
	}
	index := m.ensurePreviewDocLinkResolution()
	frame := view.PrepareFrame(docview.PrepareOptions{
		Root:              doc.root,
		Resolution:        index.SnapshotForRoot(doc.root),
		Matchers:          m.resourceMatchers,
		MatcherGeneration: m.linkMatcherGeneration,
		AllowedKinds:      docview.ContentLinkKinds(),
		Decorate:          true,
		Links:             !doc.editing() && doc.mode == nil,
	})
	src := contentpanes.SourceContext{Root: doc.root}
	var source contentpanes.Source = contentpanes.LocalSource{}
	if m.preview.deck != nil {
		ctx := m.preview.deck.Context()
		if ctx.Source.Root != "" || ctx.Source.HostID != "" || ctx.Source.WorkspaceID != "" {
			src = ctx.Source
		} else if src.Root == "" {
			src.Root = ctx.Root
		}
		source = m.preview.deck.ContentSource()
	}
	var cmds []tea.Cmd
	docview.BeginResolutions(index, doc.root, frame, func(request contentlink.ResolutionRequest) {
		cmds = append(cmds, resolvePreviewDocContentLink(source, src, request))
	})
	return tea.Batch(cmds...)
}

func (m *Model) preparedPreviewDocBody(doc *previewDoc, originX, originY int) string {
	if doc == nil || doc.view() == nil {
		return ""
	}
	view := doc.view()
	frame := view.PreparedFrame()
	if !frame.Valid() {
		return view.View()
	}
	frame.EachHitAt(originX, originY, func(hit docview.ContentLinkHit) {
		m.preview.docLinkHits = append(m.preview.docLinkHits, previewDocLinkHit{Ref: hit.Ref, Rect: hit.Rect})
	})
	return frame.Output()
}

func resolvePreviewDocContentLink(source contentpanes.Source, src contentpanes.SourceContext, request contentlink.ResolutionRequest) tea.Cmd {
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
			if src.Remote() {
				if src.Root == "" {
					src.Root = request.Root
				}
				ref, err := contentpanes.ResolveDocument(source, src, request.Candidate)
				result.Ref, result.Found = ref, err == nil && ref.Value != ""
				return previewDocLinkResolvedMsg{Result: result}
			}
			target, ok := workspacediff.ParseSpec(request.Candidate.Raw)
			if !ok {
				return previewDocLinkResolvedMsg{Result: result}
			}
			resolved, err := workspacediff.ResolveSpec(context.Background(), request.Root, target)
			result.Ref, result.Found = contentlink.Ref{Kind: contentlink.KindDiff, Value: resolved.Identity()}, err == nil
		}
		return previewDocLinkResolvedMsg{Result: result}
	}
}

func (m *Model) applyPreviewDocLinkResolved(msg previewDocLinkResolvedMsg) {
	m.ensurePreviewDocLinkResolution().Apply(msg.Result)
}

func (m *Model) registerPreviewDocLinkHits(inner paneframe.Box) {
	for _, hit := range m.preview.docLinkHits {
		if hit.Rect.W <= 0 || hit.Rect.H <= 0 || hit.Rect.Y <= inner.Y {
			continue
		}
		m.workspacesMouse.HitMap.AddRect(previewDocLinkKind, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit)
	}
}

func (m *Model) activatePreviewDocLink(ref contentlink.Ref) tea.Cmd {
	switch ref.Kind {
	case contentlink.KindURL:
		m.clearPreviewSelection()
		return terminallink.OpenHTTP(ref.Value)
	case contentlink.KindFile:
		return m.openPreviewContent(ref, "Document")
	case contentlink.KindIssue:
		return m.openPreviewIssue(ref.Value)
	case contentlink.KindInternal:
		if ref.Namespace == "note" {
			return m.openPreviewNote(ref.Value)
		}
		return nil
	case contentlink.KindDiff:
		return m.openPreviewContent(ref, "Diff")
	case contentlink.KindResource:
		if m.previewRemoteHostID() != "" {
			return nil
		}
		return m.openPreviewContent(ref, "Resource")
	default:
		return nil
	}
}

func (m *Model) clearPreviewDocLinkHits() {
	m.preview.docLinkHits = m.preview.docLinkHits[:0]
}
