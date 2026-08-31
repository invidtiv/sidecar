package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestDocPaneContentLinksRegisterAndActivateIssueFileAndDiff(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# hello\n")
	writeDocPaneFixture(t, root, "links.txt", "README.md\ntd-22f35f\nabcdef0\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("links.txt", 1))
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatal("no document pane")
	}
	p.ensureDocLinkResolution().PutForRoot(doc.root,
		contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"},
		contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"},
		true,
	)
	p.ensureDocLinkResolution().PutForRoot(doc.root,
		contentlink.Pending{Kind: contentlink.KindDiff, Raw: "abcdef0"},
		contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:abcdef0"},
		true,
	)

	p.mouseHandler.Clear()
	view, ok := p.renderDocumentSplit(p.width, p.height)
	if !ok {
		t.Fatal("document split was not rendered")
	}
	if !strings.Contains(view, "\x1b[4m") {
		t.Fatalf("document body was not decorated: %q", ansi.Strip(view))
	}

	issueHit := docLinkHit(t, p, contentlink.KindIssue, "td-22f35f")
	fileHit := docLinkHit(t, p, contentlink.KindFile, "README.md")
	diffHit := docLinkHit(t, p, contentlink.KindDiff, "c:abcdef0")

	header := docPaneRegion(p, regionDocTab)
	if header == nil {
		t.Fatal("document tab strip has no hit region")
	}
	if resolved := p.mouseHandler.HitMap.Test(header.Rect.X+1, header.Rect.Y); resolved == nil || resolved.ID != regionDocTab {
		t.Fatalf("tab row resolved to %#v, want %s", resolved, regionDocTab)
	}

	applyDocOpen(t, p, p.handleMouse(tea.MouseClickMsg(tea.Mouse{
		X: issueHit.Rect.X, Y: issueHit.Rect.Y, Button: tea.MouseLeft,
	})))
	issue, _ := p.activeIssuePane()
	if issue == nil || issue.view() == nil || issue.view().IssueID() != "td-22f35f" {
		t.Fatalf("issue pane = %#v", issue)
	}

	p.mouseHandler.Clear()
	if _, ok := p.renderDocumentSplit(p.width, p.height); !ok {
		t.Fatal("split disappeared after issue open")
	}
	fileHit = docLinkHit(t, p, contentlink.KindFile, "README.md")
	applyDocOpen(t, p, p.handleMouse(tea.MouseClickMsg(tea.Mouse{
		X: fileHit.Rect.X, Y: fileHit.Rect.Y, Button: tea.MouseLeft,
	})))
	doc, _ = p.activeDocPane()
	if doc == nil || doc.view() == nil || doc.view().Title() != "README.md" {
		t.Fatalf("document after file click = %#v", doc)
	}

	p.mouseHandler.Clear()
	applyDocOpen(t, p, p.openTerminalPath("links.txt", 1))
	p.ensureDocLinkResolution().PutForRoot(doc.root,
		contentlink.Pending{Kind: contentlink.KindDiff, Raw: "abcdef0"},
		contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:abcdef0"},
		true,
	)
	if _, ok := p.renderDocumentSplit(p.width, p.height); !ok {
		t.Fatal("split disappeared after retarget")
	}
	diffHit = docLinkHit(t, p, contentlink.KindDiff, "c:abcdef0")
	applyDocOpen(t, p, p.handleMouse(tea.MouseClickMsg(tea.Mouse{
		X: diffHit.Rect.X, Y: diffHit.Rect.Y, Button: tea.MouseLeft,
	})))
	diff, _ := p.activeDiffPane()
	if diff == nil || diff.tabs.Find("c:abcdef0") < 0 {
		t.Fatalf("diff pane = %#v", diff)
	}
}

func docLinkHit(t *testing.T, p *Plugin, kind contentlink.Kind, value string) mouse.Region {
	t.Helper()
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionDocLink {
			continue
		}
		hit, ok := region.Data.(docContentLinkHit)
		if !ok || hit.Ref.Kind != kind || hit.Ref.Value != value {
			continue
		}
		resolved := p.mouseHandler.HitMap.Test(region.Rect.X, region.Rect.Y)
		if resolved == nil || resolved.ID != regionDocLink {
			t.Fatalf("%s %s resolves to %#v, want %s", kind, value, resolved, regionDocLink)
		}
		return region
	}
	t.Fatalf("no %s link for %q", kind, value)
	return mouse.Region{}
}
