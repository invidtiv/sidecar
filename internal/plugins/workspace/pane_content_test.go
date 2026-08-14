package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
)

// docPaneSplitTree builds terminal | doc, the shipped two-leaf journey, and
// returns the two leaves.
func docPaneSplitTree(t *testing.T, p *Plugin, root, rel string) (terminal, leaf *PaneNode, doc *docPane) {
	t.Helper()
	writeDocPaneFixture(t, root, rel, "# "+rel+"\n\nbody\n")
	viewer := docview.New(nil)
	viewer.Load(2, root, rel, 0, p.ctx.Epoch)
	doc = &docPane{leafID: 2, root: root, surface: "shell:test-shell", view: viewer}
	p.docs = map[int]*docPane{2: doc}
	terminal = &PaneNode{ID: 1, Kind: PaneTerminal}
	leaf = &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}
	p.paneRoot = &PaneNode{ID: 9, Split: &PaneSplit{Axis: SplitCols, Ratio: 50, A: terminal, B: leaf}}
	p.paneFocus = 2
	p.paneNextID = 10
	return terminal, leaf, doc
}

func TestPaneContentAdaptsEveryLeafKind(t *testing.T) {
	for _, shell := range []bool{true, false} {
		name := "workspace"
		if shell {
			name = "shell"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			p := docPaneTestPlugin(t, root, shell)
			terminal, leaf, _ := docPaneSplitTree(t, p, root, "one.md")

			term := p.paneContent(terminal)
			if term == nil || term.Kind() != contentKindTerminal {
				t.Fatalf("terminal leaf adapted to %#v", term)
			}
			wantTitle := "selected"
			if shell {
				wantTitle = "Shell"
			}
			if got := term.Title(); got != wantTitle {
				t.Fatalf("terminal title = %q, want %q", got, wantTitle)
			}

			document := p.paneContent(leaf)
			if document == nil || document.Kind() != contentKindDoc {
				t.Fatalf("document leaf adapted to %#v", document)
			}
			if got := document.Title(); got != "one.md" {
				t.Fatalf("document title = %q, want the document it shows", got)
			}

			// A leaf whose content is gone has none: the canvas leaves its box
			// blank rather than letting a neighbour spread into it.
			delete(p.docs, leaf.ContentID)
			if got := p.paneContent(leaf); got != nil {
				t.Fatalf("document leaf without a document adapted to %#v", got)
			}
			if got := p.paneContent(nil); got != nil {
				t.Fatalf("nil leaf adapted to %#v", got)
			}
		})
	}
}

// A leaf encoded under one name and adapted under another is a layout that
// restores as the wrong content, so the two keys are the same string.
func TestContentKindIsThePersistedLeafKey(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	terminal, leaf, _ := docPaneSplitTree(t, p, root, "one.md")

	for _, node := range []*PaneNode{terminal, leaf} {
		saved := p.encodePaneNode(node)
		if saved == nil {
			t.Fatalf("leaf %d did not encode", node.ID)
		}
		if got := p.paneContent(node).Kind(); saved.Kind != got {
			t.Fatalf("leaf %d persists as %q and adapts as %q", node.ID, saved.Kind, got)
		}
	}
}

func TestDocContentDrawsItsHeaderAboveTheViewerBox(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	_, leaf, doc := docPaneSplitTree(t, p, root, "one.md")

	const width, height = 48, 14
	content := p.paneContent(leaf)
	if cmd := content.SetSize(Size{Width: width, Height: height}); cmd != nil {
		t.Fatal("sizing a document leaf must not command a resize from inside a frame")
	}
	got := content.View(Render{})

	header, body, split := strings.Cut(got, "\n")
	if !split {
		t.Fatalf("document leaf drew no body under its header: %q", got)
	}
	if want := p.docPaneHeaderRow(doc, content.Title(), width, false); header != want {
		t.Fatalf("header row = %q, want %q", header, want)
	}
	if cells := ansi.StringWidth(header); cells != width {
		t.Fatalf("header row width = %d, want %d", cells, width)
	}
	// The viewer is sized to the box below the header row — the same
	// subtraction termpreview.SurfaceIn makes for a terminal leaf.
	doc.view.SetSize(width, height-terminalHeaderRows)
	if want := doc.view.View(); body != want {
		t.Fatalf("document body was drawn against a different box than the header row left it")
	}
}

func TestPaneLeafTakesFocusFromTheFrame(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	terminal, leaf, _ := docPaneSplitTree(t, p, root, "one.md")

	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	placement := Placement{Node: leaf, Box: Box{W: 48, H: 14}}

	p.paneFocus = leaf.ID
	focused := p.renderPaneLeaf(placement, origin, false)
	p.paneFocus = terminal.ID
	unfocused := p.renderPaneLeaf(placement, origin, false)
	if focused == unfocused {
		t.Fatal("the focused and unfocused document leaf drew the same bytes")
	}

	content := p.paneContent(leaf)
	content.SetSize(Size{Width: placement.Box.W, Height: placement.Box.H})
	if want := content.View(Render{Focused: true}); focused != want {
		t.Fatal("the frame did not report the focused leaf as focused")
	}
	if want := content.View(Render{}); unfocused != want {
		t.Fatal("the frame reported an unfocused leaf as focused")
	}
}

// The terminal leaf keeps its whole box: its header row is drawn from inside
// its body by the legacy renderer until M1 absorbs the panel into the tree. The
// legacy renderer draws it in the box it was given, cell for cell — nothing is
// dropped or moved by the leaf holding itself to that rectangle.
func TestTerminalLeafDrawsTheLegacyPreviewInItsWholeBox(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	terminal, _, _ := docPaneSplitTree(t, p, root, "one.md")

	origin, _ := p.previewContentBox()
	box := Box{W: 60, H: 18}
	got := p.renderPaneLeaf(Placement{Node: terminal, Box: box}, origin, false)
	legacy := strings.Split(p.renderPreviewContentLegacy(box.W, box.H), "\n")
	for row, line := range strings.Split(got, "\n") {
		want := ""
		if row < len(legacy) {
			want = legacy[row]
		}
		if trim(line) != trim(want) {
			t.Fatalf("row %d = %q, want the legacy preview's %q", row, trim(line), trim(want))
		}
	}
}

// Every content owes its frame exactly the rectangle it was sized to. The
// legacy preview answers several of its states in their own shape — a header
// row over two lines of "no agent running" is three ragged rows whatever the
// box — and a leaf that hands back less than its box is a leaf whose neighbours
// get placed by the width of its longest line. The compositor would clip and
// pad it anyway; the contract is that it never has to.
func TestEveryContentDrawsExactlyItsBox(t *testing.T) {
	for name, state := range map[string]struct {
		shell bool
		setup func(p *Plugin)
	}{
		"shell with a live agent":     {shell: true},
		"shell with no agent":         {shell: true, setup: func(p *Plugin) { p.shells[0].Agent = nil }},
		"workspace with a live agent": {},
		"workspace with no agent":     {setup: func(p *Plugin) { p.worktrees[0].Agent = nil }},
		"workspace on the diff tab":   {setup: func(p *Plugin) { p.previewTab = PreviewTabDiff }},
		"workspace with an orphan tree": {setup: func(p *Plugin) {
			p.worktrees[0].Agent = nil
			p.worktrees[0].IsOrphaned = true
		}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			p := docPaneTestPlugin(t, root, state.shell)
			if state.setup != nil {
				state.setup(p)
			}
			terminal, leaf, _ := docPaneSplitTree(t, p, root, "one.md")
			for _, box := range []Box{{W: 80, H: 24}, {W: 40, H: 10}, {W: 31, H: 4}} {
				for _, node := range []*PaneNode{terminal, leaf} {
					content := p.paneContent(node)
					content.SetSize(Size{Width: box.W, Height: box.H})
					rows := strings.Split(content.View(Render{}), "\n")
					if len(rows) != box.H {
						t.Fatalf("%s leaf in %dx%d drew %d rows", content.Kind(), box.W, box.H, len(rows))
					}
					for row, line := range rows {
						if cells := ansi.StringWidth(line); cells != box.W {
							t.Fatalf("%s leaf in %dx%d drew row %d %d cells wide: %q",
								content.Kind(), box.W, box.H, row, cells, ansi.Strip(line))
						}
					}
				}
			}
		})
	}
}

// trim drops the padding a row was held to its box with, so two renderings are
// compared on their cells rather than on where each stopped.
func trim(row string) string {
	return strings.TrimRight(ansi.Strip(row), " ")
}

// A box too small for the tree gives the box to the focused leaf, so which
// document it shows is a decision, not whichever one a map ranged over first.
func TestZoomedLeafDrawsTheFocusedDocument(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	nestedDocPaneTree(t, p, root)

	const width, height = 30, 10
	for _, focus := range []int{2, 3} {
		p.paneFocus = focus
		p.activePane = PanePreview
		got, ok := p.renderDocumentSplit(width, height)
		if !ok {
			t.Fatalf("focus %d: zoomed document leaf was not rendered", focus)
		}
		stripped := ansi.Strip(got)
		want, other := "one.md", "two.md"
		if focus == 3 {
			want, other = other, want
		}
		if !strings.Contains(stripped, want) || strings.Contains(stripped, other) {
			t.Fatalf("focus %d zoomed to %q, want only %q", focus, stripped, want)
		}
	}
}
