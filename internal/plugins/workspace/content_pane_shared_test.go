package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// contentKindCase is one content kind expressed through the shared open and
// lifecycle helpers. Every kind answers the same four questions, which is what
// lets the tests below assert placement, refusal and hide/reopen once rather
// than four times — a kind that stops going through the shared helper stops
// matching its neighbours here.
type contentKindCase struct {
	name  string
	kind  PaneKind
	setup func(t *testing.T) *Plugin
	// open opens the kind's first or second target on the selected surface.
	open func(t *testing.T, p *Plugin, second bool) tea.Cmd
	// live is the kind's leaf and its tab count, or nil when nothing is open.
	live func(*Plugin) (*PaneNode, int)
	hide func(*Plugin) tea.Cmd
}

func contentKindCases() []contentKindCase {
	docSetup := func(t *testing.T) *Plugin {
		root := t.TempDir()
		writeDocPaneFixture(t, root, "README.md", "# readme\n")
		writeDocPaneFixture(t, root, "main.go", "package main\n")
		return docPaneTestPlugin(t, root, true)
	}
	issueSetup := func(t *testing.T) *Plugin {
		stubTd(t)
		return docPaneTestPlugin(t, t.TempDir(), true)
	}
	resourceSetup := func(t *testing.T) *Plugin {
		p, _, _ := resourceTestPlugin(t)
		return p
	}
	resourceRef := func(locator string) resourceview.Ref {
		return resourceview.Ref{Instance: "jira-work", Matcher: "issue-key", Locator: locator}
	}
	return []contentKindCase{{
		name:  "Document",
		kind:  PaneDoc,
		setup: docSetup,
		open: func(t *testing.T, p *Plugin, second bool) tea.Cmd {
			rel := "README.md"
			if second {
				rel = "main.go"
			}
			root, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no selected terminal surface")
			}
			return p.openDocPaneForSurface(root, surface, rel, 0)
		},
		live: func(p *Plugin) (*PaneNode, int) {
			doc, leaf := p.activeDocPane()
			if doc == nil {
				return nil, 0
			}
			return leaf, len(doc.tabs.Items)
		},
		hide: (*Plugin).hideDocPane,
	}, {
		name:  "Issue",
		kind:  PaneIssue,
		setup: issueSetup,
		open: func(t *testing.T, p *Plugin, second bool) tea.Cmd {
			id := "td-1111aa"
			if second {
				id = "td-2222bb"
			}
			root, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no selected terminal surface")
			}
			return p.openIssuePaneForSurface(root, surface, id)
		},
		live: func(p *Plugin) (*PaneNode, int) {
			issue, leaf := p.activeIssuePane()
			if issue == nil {
				return nil, 0
			}
			return leaf, len(issue.tabs.Items)
		},
		hide: (*Plugin).hideIssuePane,
	}, {
		name:  "Diff",
		kind:  PaneDiff,
		setup: func(t *testing.T) *Plugin { return docPaneTestPlugin(t, t.TempDir(), true) },
		open: func(t *testing.T, p *Plugin, second bool) tea.Cmd {
			target := workspacediff.WorkingTreeTarget()
			if second {
				target = workspacediff.Target{Kind: workspacediff.TargetCommit, A: "deadbeef"}
			}
			root, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no selected terminal surface")
			}
			return p.openDiffPaneForSurface(root, surface, target)
		},
		live: func(p *Plugin) (*PaneNode, int) {
			diff, leaf := p.activeDiffPane()
			if diff == nil {
				return nil, 0
			}
			return leaf, len(diff.tabs.Items)
		},
		hide: (*Plugin).hideDiffPane,
	}, {
		name:  "Resource",
		kind:  PaneResource,
		setup: resourceSetup,
		open: func(t *testing.T, p *Plugin, second bool) tea.Cmd {
			locator := "CASH-1245"
			if second {
				locator = "CASH-2000"
			}
			root, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no selected terminal surface")
			}
			return p.openResourcePaneForSurface(root, surface, resourceRef(locator))
		},
		live: func(p *Plugin) (*PaneNode, int) {
			res, leaf := p.activeResourcePane()
			if res == nil {
				return nil, 0
			}
			return leaf, len(res.tabs.References())
		},
		hide: (*Plugin).hideResourcePane,
	}}
}

// focusContentLeaf puts the keyboard on a content leaf the way a click or Tab
// does, which is what the hide keys require before they will answer.
func focusContentLeaf(p *Plugin, leafID int) {
	p.paneFocus = leafID
	p.activePane = PanePreview
}

// A second open of the same kind must land on the leaf that is already there.
// The shared helper is the only thing that decides this, so a kind that grew
// its own placement would split twice here.
func TestContentKindsShareTheRetargetRule(t *testing.T) {
	for _, tc := range contentKindCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.setup(t)
			tc.open(t, p, false)
			leaf, tabs := tc.live(p)
			if leaf == nil || tabs != 1 {
				t.Fatalf("first open = leaf %#v tabs=%d, want one leaf with one tab", leaf, tabs)
			}
			if p.contentDeck == nil || p.contentDeck.Leaf(tc.kind) != leaf.ID {
				t.Fatalf("first open was not owned by the shared deck: deck=%p leaf=%#v", p.contentDeck, leaf)
			}
			if items, active := p.contentDeck.Tabs(leaf.ID); len(items) != 1 || active != 0 {
				t.Fatalf("shared deck tabs = %d active=%d, want one active tab", len(items), active)
			}
			first := leaf.ID
			if p.paneFocus != first || p.activePane != PanePreview {
				t.Fatalf("first open left focus=%d active=%v, want the new leaf focused", p.paneFocus, p.activePane)
			}

			tc.open(t, p, true)
			leaf, tabs = tc.live(p)
			if leaf == nil || leaf.ID != first {
				t.Fatalf("second open = leaf %#v, want a retarget onto leaf %d", leaf, first)
			}
			if tabs != 2 {
				t.Fatalf("second open = %d tabs, want the second target appended", tabs)
			}
			if items, active := p.contentDeck.Tabs(first); len(items) != 2 || active != 1 {
				t.Fatalf("shared deck after retarget = %d tabs active=%d, want two tabs with the second active", len(items), active)
			}
			if got := len(p.contentLeafIDs()); got != 1 {
				t.Fatalf("second open produced %d content leaves, want one", got)
			}
			if p.paneFocus != first || p.activePane != PanePreview {
				t.Fatalf("retarget left focus=%d active=%v, want the retargeted leaf focused", p.paneFocus, p.activePane)
			}
		})
	}
}

// A box that cannot hold the split must leave the tree, the focus and the
// terminal's geometry exactly as they were, and say so. Half-applying the
// split is the failure this guards: a leaf in the tree that never gets drawn
// still drives the agent terminal's size.
func TestContentKindsShareTheRefusedSplit(t *testing.T) {
	for _, tc := range contentKindCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.setup(t)
			p.width = 40
			before := terminalLeafID(p.paneRoot)
			p.paneFocus = before

			tc.open(t, p, false)
			if leaf, _ := tc.live(p); leaf != nil {
				t.Fatalf("a 40-column preview accepted a split: leaf %#v", leaf)
			}
			if p.toastMessage == "" {
				t.Fatal("the refusal was silent; the user is owed the dimension it needed")
			}
			if p.paneRoot == nil || p.paneRoot.Split != nil {
				t.Fatalf("refused split still mutated the tree: %#v", p.paneRoot)
			}
			if p.paneFocus != before {
				t.Fatalf("refused split moved focus to %d, want the terminal %d", p.paneFocus, before)
			}
			if got := len(p.contentLeafIDs()); got != 0 {
				t.Fatalf("refused split left %d content leaves", got)
			}
		})
	}
}

// q/esc hides; the tab set survives in the hidden snapshot; the next open of
// the same kind brings it back rather than starting empty.
func TestContentKindsShareTheHideAndReopenLifecycle(t *testing.T) {
	for _, tc := range contentKindCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.setup(t)
			tc.open(t, p, false)
			tc.open(t, p, true)
			leaf, tabs := tc.live(p)
			if leaf == nil || tabs != 2 {
				t.Fatalf("setup = leaf %#v tabs=%d, want two tabs on one leaf", leaf, tabs)
			}
			focusContentLeaf(p, leaf.ID)

			tc.hide(p)
			if leaf, _ := tc.live(p); leaf != nil {
				t.Fatalf("hide left the leaf live: %#v", leaf)
			}
			if p.paneRoot == nil || p.paneRoot.Split != nil {
				t.Fatalf("hide left a split behind: %#v", p.paneRoot)
			}
			if p.hiddenPaneLayout == nil || !paneLayoutHasRetainedTabs(p.hiddenPaneLayout) {
				t.Fatalf("hide forgot the tab set: %#v", p.hiddenPaneLayout)
			}

			// Reopening the FIRST target is the real test: if the hidden set were
			// dropped, this would come back as a single fresh tab.
			tc.open(t, p, false)
			leaf, tabs = tc.live(p)
			if leaf == nil {
				t.Fatal("reopen did not rebuild the leaf")
			}
			if tabs != 2 {
				t.Fatalf("reopen = %d tabs, want the remembered two", tabs)
			}
			if p.hiddenPaneLayout != nil {
				t.Fatalf("reopen left a stale hidden snapshot: %#v", p.hiddenPaneLayout)
			}
		})
	}
}

func TestHiddenDocumentDoesNotResurrectWhenInflightLoadLands(t *testing.T) {
	tc := contentKindCases()[0]
	p := tc.setup(t)
	inFlight := tc.open(t, p, false)
	leaf, _ := tc.live(p)
	if leaf == nil || inFlight == nil {
		t.Fatal("document setup did not open with an in-flight load")
	}
	focusContentLeaf(p, leaf.ID)
	tc.hide(p)
	deliverLoads(t, p, inFlight)
	if doc, _ := p.activeDocPane(); doc != nil {
		t.Fatalf("in-flight result resurrected hidden document: %#v", doc)
	}
	if got := p.contentDeck.Leaf(PaneDoc); got != 0 {
		t.Fatalf("shared deck restored hidden Document leaf %d", got)
	}
}

// Hiding one kind while another content leaf is still live cannot restore the
// whole remembered layout — that would duplicate the live leaf. The shared
// helper reinserts just the hidden kind's leaf beside it.
func TestContentKindsReinsertBesideALiveLeaf(t *testing.T) {
	for _, tc := range contentKindCases() {
		if tc.kind == PaneDoc {
			// The document is the companion leaf in this test; pairing it with
			// itself would be the retarget case, not the reinsert one.
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			stubTd(t)
			p := tc.setup(t)
			// The companion document is opened against the same surface, so both
			// leaves belong to the selection and neither is collapsed.
			docRoot, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no selected terminal surface")
			}
			writeDocPaneFixture(t, docRoot, "README.md", "# readme\n")
			p.openDocPaneForSurface(docRoot, surface, "README.md", 0)
			if doc, _ := p.activeDocPane(); doc == nil {
				t.Fatal("the companion document pane did not open")
			}

			tc.open(t, p, false)
			leaf, tabs := tc.live(p)
			if leaf == nil || tabs != 1 {
				t.Fatalf("%s open beside a document = leaf %#v tabs=%d", tc.name, leaf, tabs)
			}
			if got := len(p.contentLeafIDs()); got != 2 {
				t.Fatalf("want a document and a %s leaf, got %d content leaves", tc.name, got)
			}
			focusContentLeaf(p, leaf.ID)

			tc.hide(p)
			if leaf, _ := tc.live(p); leaf != nil {
				t.Fatalf("hide left the %s leaf live", tc.name)
			}
			if doc, _ := p.activeDocPane(); doc == nil {
				t.Fatalf("hiding the %s leaf took the document with it", tc.name)
			}

			tc.open(t, p, false)
			leaf, tabs = tc.live(p)
			if leaf == nil || tabs != 1 {
				t.Fatalf("reinsert = leaf %#v tabs=%d, want the remembered %s leaf", leaf, tabs, tc.name)
			}
			if doc, _ := p.activeDocPane(); doc == nil {
				t.Fatalf("reinserting the %s leaf dropped the document beside it", tc.name)
			}
			if got := len(p.contentLeafIDs()); got != 2 {
				t.Fatalf("reinsert produced %d content leaves, want the document and the %s leaf", got, tc.name)
			}
		})
	}
}
