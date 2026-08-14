package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/state"
)

// stubTd puts a td on PATH that answers `td show <id> -f json` from its
// argument alone. The issue leaf's fetch is then the real one — component,
// command, JSON decode — against a project database no test may depend on.
func stubTd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// The body runs past any pane this test composes, which is what gives the
	// wheel somewhere to travel and the compositor something to clip.
	script := "#!/bin/sh\n" +
		"body=\"Body of $2.\"\n" +
		"i=1\n" +
		"while [ $i -le 8 ]; do body=\"$body\\n\\nParagraph $i of $2.\"; i=$((i+1)); done\n" +
		`printf '{"id":"%s","title":"Issue %s","status":"open","type":"task","priority":"P2","description":"%s"}\n' "$2" "$2" "$body"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// compositorIssueLeaf gives one leaf an issue whose fetch has already been
// applied, so a golden cell is the issue's own text rather than the loading
// state every issue leaf would otherwise share.
func compositorIssueLeaf(t *testing.T, p *Plugin, root string, leafID int, issueID string) {
	t.Helper()
	fetch := p.attachIssuePane(leafID, root, "shell:test-shell", issueID)
	if fetch == nil {
		t.Fatalf("issue %s did not return a fetch", issueID)
	}
	msg, ok := fetch().(issueview.LoadedMsg)
	if !ok {
		t.Fatalf("issue %s did not answer a load result", issueID)
	}
	if msg.Error != nil {
		t.Fatalf("issue %s fetch failed: %v", issueID, msg.Error)
	}
	p.applyIssueLoaded(msg)
	if p.issues[leafID].view.Loading() {
		t.Fatalf("issue %s pane did not apply its own fetch", issueID)
	}
}

// steelThreadPaneTree is the journey this work exists for: the terminal holding
// the left column full height, a clicked file above a clicked td issue in the
// right one.
func steelThreadPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
		}},
	}}
	p.paneFocus = 3
	p.paneNextID = 12
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	compositorDocLeaf(t, p, root, 2, "clicked.md", "# clicked\n\nfile body\n")
	compositorIssueLeaf(t, p, root, 3, "td-1a2b3c")
}

// issueLeafBox is the box the layout gave the issue leaf, which is where its
// cells must be if the compositor placed it.
func issueLeafBox(t *testing.T, p *Plugin, width, height int) Box {
	t.Helper()
	leaves, _, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	for _, placement := range leaves {
		if placement.Node.Kind == PaneIssue {
			return placement.Box
		}
	}
	t.Fatal("the issue leaf was not placed")
	return Box{}
}

func TestSteelThreadPaneTreeComposesTheIssueLeafsCells(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)

	const width, height = 100, 24
	rows := composePaneTree(t, p, width, height)
	assertPaneTreeGolden(t, rows, "pane-tree-steel-thread.txt")

	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	assertDividersDrawn(t, rows, dividers)
	assertPaneTreeRegions(t, p, leaves, dividers)

	// The golden holds every cell, but only reading the issue's own box proves
	// the issue landed in it rather than in the row above or the column beside.
	box := issueLeafBox(t, p, width, height)
	within := func(row int) string {
		cells := []rune(ansi.Strip(rows[box.Y+row]))
		return string(cells[box.X : box.X+box.W])
	}
	if header := within(0); !strings.Contains(header, "td-1a2b3c") {
		t.Fatalf("issue header row = %q, want the issue's identity", header)
	}
	body := make([]string, 0, box.H-1)
	for row := 1; row < box.H; row++ {
		body = append(body, within(row))
	}
	joined := strings.Join(body, "\n")
	for _, want := range []string{"td-1a2b3c: Issue td-1a2b3c", "[open]", "Body of td-1a2b3c"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issue body missing %q:\n%s", want, joined)
		}
	}
}

// The issue leaf persists like any other: its identity is written, and a
// restore re-fetches it rather than reviving a body td may have moved on from.
func TestIssueLeafRoundTripsThroughThePersistedLayout(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 60,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 40,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
		}},
	}}
	p.paneFocus, p.paneNextID = 1, 12
	p.issues = make(map[int]*issuePane)
	compositorDocLeaf(t, p, resolved, 2, "clicked.md", "# clicked\n")
	p.attachIssuePane(3, resolved, "shell:test-shell", "td-1a2b3c")

	layout := p.persistedPaneLayout()
	if layout == nil || layout.Split == nil || layout.Split.B.Split == nil {
		t.Fatalf("persisted layout lost the stack: %#v", layout)
	}
	saved := layout.Split.B.Split.B
	if saved.Kind != contentKindIssue || saved.Issue != "td-1a2b3c" {
		t.Fatalf("persisted issue leaf = %#v, want kind %q targeting td-1a2b3c", saved, contentKindIssue)
	}

	restored := docPaneTestPlugin(t, root, true)
	if cmd := restored.restorePaneLayout(layout); cmd == nil {
		t.Fatal("restored layout did not schedule its loads")
	}
	if restored.paneRoot.Split == nil || restored.paneRoot.Split.Ratio != 60 ||
		restored.paneRoot.Split.B.Split == nil || restored.paneRoot.Split.B.Split.Ratio != 40 {
		t.Fatalf("restored tree = %#v, want the persisted ratios", restored.paneRoot)
	}
	issue, leaf := restored.activeIssuePane()
	if issue == nil || leaf == nil {
		t.Fatal("the issue leaf was not restored")
	}
	if issue.view.IssueID() != "td-1a2b3c" || !issue.view.Loading() {
		t.Fatalf("restored issue = %q loading=%v, want td-1a2b3c re-fetching",
			issue.view.IssueID(), issue.view.Loading())
	}
	if issue.root != resolved || issue.surface != "shell:test-shell" {
		t.Fatalf("restored issue surface = %q %q, want the selected terminal's", issue.root, issue.surface)
	}
}

// An issue leaf with no target left in the saved layout costs its own pane and
// nothing else: the split collapses onto its sibling and the rest of the layout
// is what the user left.
func TestUnresolvableIssueLeafCollapsesWithoutResettingTheLayout(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolved, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: "rows", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: []state.PaneDocTabJSON{{Path: "clicked.md"}}},
			B: &state.PaneLayoutJSON{Kind: contentKindIssue},
		}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("the surviving document did not schedule its load")
	}
	if doc, _ := p.activeDocPane(); doc == nil || p.paneRoot.Split == nil {
		t.Fatalf("a targetless issue leaf reset the whole layout: root=%#v", p.paneRoot)
	}
	if issue, _ := p.activeIssuePane(); issue != nil {
		t.Fatal("a targetless issue leaf was restored")
	}
}

// The issue leaf answers the wheel over its own box and the close chip in its
// own header, both at the regions the canvas registered them from.
func TestIssuePaneAnswersTheWheelAndItsCloseChip(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)

	const width, height = 100, 24
	composePaneTree(t, p, width, height)
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	box := issueLeafBox(t, p, width, height)

	x, y := origin.X+box.X+1, origin.Y+box.Y+box.H-1
	body := p.mouseHandler.HitMap.Test(x, y)
	if body == nil || body.ID != regionIssuePane {
		t.Fatalf("the issue leaf's body resolves to %#v, want %s", body, regionIssuePane)
	}
	before := p.issues[3].view.View()
	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollDown, Region: body, Delta: 3, X: x, Y: y})
	if p.issues[3].view.View() == before {
		t.Fatal("a notch over the issue leaf scrolled nothing")
	}

	var closeChip *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionIssueClose {
			chip := region
			closeChip = &chip
			break
		}
	}
	if closeChip == nil {
		t.Fatal("the issue leaf drew no close chip")
	}
	if cmd := p.handleMouseClick(mouse.MouseAction{Type: mouse.ActionClick, Region: closeChip}); cmd == nil {
		t.Fatal("closing the issue leaf did not schedule the resize of the terminal it gave its box back to")
	}
	if issue, _ := p.activeIssuePane(); issue != nil || len(p.issues) != 0 {
		t.Fatalf("the issue leaf survived its close chip: %#v", p.issues)
	}
	if doc, _ := p.activeDocPane(); doc == nil {
		t.Fatal("closing the issue leaf took its sibling with it")
	}
}
