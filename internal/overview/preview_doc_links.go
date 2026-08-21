package overview

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
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
	Candidate contentlink.Pending
	Ref       contentlink.Ref
	Found     bool
}

func (m *Model) ensurePreviewDocLinkResolution() *contentlink.ResolutionIndex {
	if m.preview.docLinkResolution == nil {
		m.preview.docLinkResolution = contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions)
	}
	return m.preview.docLinkResolution
}

func (m *Model) decoratePreviewDocBody(doc *previewDoc, body string) string {
	if doc == nil || doc.editing() || doc.mode != nil {
		return body
	}
	view := doc.view()
	if view == nil || !view.ContentLinksSafe() {
		return body
	}
	frame := view.ScanContentLinks(body, contentlink.FrameOptions{
		Ready:        m.ensurePreviewDocLinkResolution().Snapshot(),
		Matchers:     m.resourceMatchers,
		AllowedKinds: docview.ContentLinkKinds(),
		Decorate:     true,
	})
	m.preview.docLinkHits = append(m.preview.docLinkHits, frameHits(frame)...)
	for _, candidate := range frame.Pending {
		m.queuePreviewDocLinkResolve(doc.root, candidate)
	}
	return frame.Output
}

func frameHits(frame docview.ContentLinkFrame) []previewDocLinkHit {
	hits := make([]previewDocLinkHit, 0, len(frame.Hits))
	for _, hit := range frame.Hits {
		hits = append(hits, previewDocLinkHit{Ref: hit.Ref, Rect: hit.Rect})
	}
	return hits
}

func (m *Model) queuePreviewDocLinkResolve(root string, candidate contentlink.Pending) {
	if m.preview.docLinkPending == nil {
		m.preview.docLinkPending = make(map[contentlink.Pending]bool)
	}
	if m.preview.docLinkPending[candidate] {
		return
	}
	m.preview.docLinkPending[candidate] = true
	m.queuePreviewCmd(resolvePreviewDocContentLink(root, candidate))
}

func resolvePreviewDocContentLink(root string, candidate contentlink.Pending) tea.Cmd {
	return func() tea.Msg {
		msg := previewDocLinkResolvedMsg{Candidate: candidate}
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

func (m *Model) applyPreviewDocLinkResolved(msg previewDocLinkResolvedMsg) {
	if m.preview.docLinkPending != nil {
		delete(m.preview.docLinkPending, msg.Candidate)
	}
	m.ensurePreviewDocLinkResolution().Put(msg.Candidate, msg.Ref, msg.Found)
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
		return m.openPreviewContent(ref, "Issue")
	case contentlink.KindDiff:
		return m.openPreviewContent(ref, "Diff")
	case contentlink.KindResource:
		return m.openPreviewContent(ref, "Resource")
	default:
		return nil
	}
}

func (m *Model) clearPreviewDocLinkHits() {
	m.preview.docLinkHits = m.preview.docLinkHits[:0]
}
