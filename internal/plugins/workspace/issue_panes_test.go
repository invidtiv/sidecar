package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
		"if [ \"$1\" = tree ]; then\n" +
		`printf '{"id":"%s","title":"Issue %s","status":"open","type":"task","priority":"P2","children":[]}\n' "$2" "$2"` + "\n" +
		"exit 0\n" +
		"fi\n" +
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
func compositorIssueLeaf(t *testing.T, p *Plugin, leafID int, issueID string) {
	t.Helper()
	// Attached against the surface the plugin itself names, which is what every
	// production entrance passes it: the delivery below is refused for a pane
	// bound to any other one.
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatalf("issue %s has no selected terminal surface to belong to", issueID)
	}
	fetch := p.attachIssuePane(leafID, root, surface, issueID)
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
	compositorIssueLeaf(t, p, 3, "td-1a2b3c")
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
	for _, want := range []string{"OPEN", "Issue td-1a2b3c", "Body of td-1a2b3c"} {
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
	issueData := &issueview.Data{ID: "td-1a2b3c", Title: "Persist me", Description: strings.Repeat("line\n\n", 20)}
	p.issues[3].view.SetSize(40, 3)
	p.issues[3].view.SetData(issueData)
	p.issues[3].view.Scroll(4)

	layout := p.persistedPaneLayout()
	if layout == nil || layout.Split == nil || layout.Split.B.Split == nil {
		t.Fatalf("persisted layout lost the stack: %#v", layout)
	}
	saved := layout.Split.B.Split.B
	if saved.Kind != contentKindIssue || saved.Issue != "td-1a2b3c" || saved.Scroll != 4 {
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
	if issue.view.IssueID() != "td-1a2b3c" || issue.view.ScrollOffset() != 4 || !issue.view.Loading() {
		t.Fatalf("restored issue = %q loading=%v, want td-1a2b3c re-fetching",
			issue.view.IssueID(), issue.view.Loading())
	}
	if issue.root != resolved || issue.surface != "shell:test-shell" {
		t.Fatalf("restored issue surface = %q %q, want the selected terminal's", issue.root, issue.surface)
	}
}

// The steel thread has to outlive the session that built it. This is the round
// trip a quit and a reopen actually make: three leaves built by clicks, written
// by the saves those clicks trigger, and read back by a second plugin that has
// nothing but the state on disk — no hand-built tree at either end.
func TestTheSteelThreadSurvivesQuitAndReopen(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n\nfile body\n")

	var saved state.WorkspaceState
	store := shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}

	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shellStartupHooks = store
	p.shells[0].Agent.OutputBuf.Update("wrote clicked.md:1\nfollow-up is td-1a2b3c\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "clicked.md"))
	deliverLoads(t, p, clickTerminalLink(t, p, "td-1a2b3c"))
	before, _ := paneLeafBoxes(t, p)
	if len(before) != 3 {
		t.Fatalf("the clicks built %d leaves, want the steel thread's three", len(before))
	}

	// Quitting writes nothing of its own: what the reopen has to work from is
	// whatever the session already saved.
	if saved.ShellTmuxName != "test-shell" || workspacePaneLayout(saved, "shell:test-shell") == nil {
		t.Fatalf("the session left nothing to reopen: %#v", saved)
	}

	reopened := docPaneTestPlugin(t, root, true)
	reopened.ctx.ProjectRoot = root
	reopened.shellStartupHooks = store
	if !reopened.restoreSelectionState() {
		t.Fatal("the reopened session restored no selection")
	}
	if reopened.paneRestoreCmd == nil {
		t.Fatal("the restored layout scheduled no loads")
	}
	deliverLoads(t, reopened, reopened.paneRestoreCmd)

	after, content := paneLeafBoxes(t, reopened)
	if len(after) != 3 || after[PaneTerminal] != before[PaneTerminal] ||
		after[PaneDoc] != before[PaneDoc] || after[PaneIssue] != before[PaneIssue] {
		t.Fatalf("reopened boxes %#v, want the ones the session was quit on %#v", after, before)
	}
	doc, _ := reopened.activeDocPane()
	if doc == nil || doc.view().Title() != "clicked.md" {
		t.Fatalf("reopened document = %#v, want the clicked file", doc)
	}
	issue, _ := reopened.activeIssuePane()
	if issue == nil || issue.view.IssueID() != "td-1a2b3c" {
		t.Fatalf("reopened issue = %#v, want td-1a2b3c", issue)
	}

	// The layout came back; so must the cells, in the boxes the layout gave.
	rows := composePaneTree(t, reopened, content.W, content.H)
	within := func(box Box) string {
		lines := make([]string, 0, box.H)
		for row := 0; row < box.H; row++ {
			cells := []rune(ansi.Strip(rows[box.Y-content.Y+row]))
			lines = append(lines, string(cells[box.X-content.X:box.X-content.X+box.W]))
		}
		return strings.Join(lines, "\n")
	}
	if cells := within(after[PaneDoc]); !strings.Contains(cells, "clicked.md") {
		t.Fatalf("reopened document box does not hold the file:\n%s", cells)
	}
	if cells := within(after[PaneIssue]); !strings.Contains(cells, "td-1a2b3c") {
		t.Fatalf("reopened issue box does not hold the issue:\n%s", cells)
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

func TestIssueChildRawCoordinateClickLoadsTheChild(t *testing.T) {
	for _, width := range []int{72, 100} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			stubTd(t)
			root := t.TempDir()
			p := docPaneTestPlugin(t, root, true)
			p.width, p.height = width, 24
			steelThreadPaneTree(t, p, root)
			issue := p.issues[3]
			issue.view.SetData(&issueview.Data{
				ID: "td-parent", Title: "Parent", Status: "open", Type: "epic",
				Children: []issueview.Ref{{ID: "td-child", Title: "Child", Status: "open", Type: "task"}},
			})

			p.mouseHandler.Clear()
			_ = p.renderListView(width, p.height)
			var child issueview.Hit
			found := false
			for _, hit := range issue.view.Hits() {
				if hit.Kind == issueview.HitChild && hit.ID == "td-child" {
					child, found = hit, true
					break
				}
			}
			if !found {
				t.Fatalf("rendered issue has no child hit: %+v", issue.view.Hits())
			}
			var pane *mouse.Region
			for _, region := range p.mouseHandler.HitMap.Regions() {
				if region.ID == regionIssuePane {
					copy := region
					pane = &copy
					break
				}
			}
			if pane == nil {
				t.Fatal("rendered issue has no pane hit region")
			}
			x := pane.Rect.X + child.X
			y := pane.Rect.Y + terminalHeaderRows + child.Y
			resolved := p.mouseHandler.HitMap.Test(x, y)
			if resolved == nil || resolved.ID != regionIssuePane {
				t.Fatalf("raw child coordinate (%d,%d) resolves to %#v", x, y, resolved)
			}

			action := p.mouseHandler.HandleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if action.Type != mouse.ActionClick || action.Region == nil || action.Region.ID != regionIssuePane {
				t.Fatalf("raw click action = %#v, want an issue-pane click", action)
			}
			cmd := p.handleMouseClick(action)
			if cmd == nil || issue.view.IssueID() != "td-child" {
				lx, ly := issueViewLocal(action.X, action.Y, action.Region.Rect)
				t.Fatalf("raw child click local=(%d,%d) target=%+v remaining=%+v cmd=%v issue=%q, want a td-child load",
					lx, ly, child, issue.view.Hits(), cmd != nil, issue.view.IssueID())
			}
			deliverLoads(t, p, cmd)
			if issue.view.Data() == nil || issue.view.Data().ID != "td-child" {
				t.Fatalf("loaded issue = %#v, want td-child", issue.view.Data())
			}

			// The loaded child's parent row occupies the same rendered row as the
			// parent's child row. Bubble Tea now emits the double-click event for
			// that same raw cell; it must not replay navigation back to the parent.
			issue.view.SetData(&issueview.Data{
				ID: "td-child", Title: "Child", Status: "open", Type: "task",
				ParentID: "td-parent",
				Parent:   &issueview.Ref{ID: "td-parent", Title: "Parent", Status: "open", Type: "epic"},
			})
			p.mouseHandler.Clear()
			_ = p.renderListView(width, p.height)
			parentAtSameCell := false
			for _, hit := range issue.view.Hits() {
				if hit.Kind == issueview.HitParent && hit.Y == child.Y && x == pane.Rect.X+hit.X {
					parentAtSameCell = true
					break
				}
			}
			if !parentAtSameCell {
				t.Fatalf("loaded child did not render its parent at the original raw cell: %+v", issue.view.Hits())
			}
			double := p.mouseHandler.HandleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if double.Type != mouse.ActionDoubleClick {
				t.Fatalf("second raw event = %#v, want double click", double)
			}
			if cmd := p.handleMouseDoubleClick(double); cmd != nil {
				t.Fatal("issue double-click scheduled a second navigation")
			}
			if issue.view.IssueID() != "td-child" || issue.view.Data() == nil || issue.view.Data().ID != "td-child" {
				t.Fatalf("double-click navigated to %#v / %q", issue.view.Data(), issue.view.IssueID())
			}
		})
	}
}

// A focused issue leaf owns the keyboard. Without a context of its own the keys
// under a pane drawn as focused are still the agent terminal's, and the host's
// root-context rule makes `q` — the key that closes the document pane one click
// earlier — open the quit confirmation instead.
func TestFocusedIssueLeafOwnsItsKeysRatherThanTheTerminals(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	_, leaf := p.activeIssuePane()
	p.paneFocus = leaf.ID
	p.activePane = PanePreview

	if !p.issueFocused() || p.docFocused() {
		t.Fatalf("focus answers: issue=%v doc=%v", p.issueFocused(), p.docFocused())
	}
	if got := p.FocusContext(); got != "workspace-issue" {
		t.Fatalf("keymap context = %q, want the issue leaf's own context", got)
	}

	// Scrolling reaches the component the wheel reaches; nothing else escapes.
	p.issues[leaf.ContentID].view.SetSize(40, 3)
	before := p.issues[leaf.ContentID].view.View()
	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: 'j'}); !handled {
		t.Fatal("the focused issue leaf did not claim j")
	}
	if p.issues[leaf.ContentID].view.View() == before {
		t.Fatal("j over a focused issue leaf scrolled nothing")
	}
	// Every other key is absorbed: routed through the plugin's own key path, it
	// must leave the terminal, the selection and the tree exactly as they were.
	tree, focus, offset := p.paneRoot, p.paneFocus, p.previewOffset
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: 'a'}, {Code: 'n'}, {Code: 'r'}, {Code: tea.KeyRight}, {Code: tea.KeyDown},
	} {
		if handled, cmd := p.handleIssueKey(key); !handled || cmd != nil {
			t.Fatalf("key %v escaped the focused issue leaf: handled=%v cmd=%v", key, handled, cmd != nil)
		}
		p.handleListKeys(key)
		if p.viewMode != ViewModeList || p.interactiveState != nil {
			t.Fatalf("key %v reached the terminal behind the pane: mode=%v interactive=%#v",
				key, p.viewMode, p.interactiveState)
		}
		if p.paneRoot != tree || p.paneFocus != focus || p.activePane != PanePreview || p.previewOffset != offset {
			t.Fatalf("key %v moved the workspace behind the pane: focus=%d pane=%v offset=%d",
				key, p.paneFocus, p.activePane, p.previewOffset)
		}
	}

	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'q'}); cmd == nil {
		t.Fatal("q did not close the issue leaf back onto its sibling")
	}
	if issue, _ := p.activeIssuePane(); issue != nil || len(p.issues) != 0 {
		t.Fatalf("the issue leaf survived q: %#v", p.issues)
	}
	if doc, _ := p.activeDocPane(); doc == nil {
		t.Fatal("q took the document leaf with it")
	}
}

func TestTabCyclesThroughTheIssueLeaf(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.sidebarVisible = false
	p.activePane = PanePreview
	p.paneFocus = terminalLeafID(p.paneRoot)

	// Forward: terminal → doc → issue → terminal.
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.docFocused() != true || p.issueFocused() {
		t.Fatalf("first tab: doc=%v issue=%v", p.docFocused(), p.issueFocused())
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if !p.issueFocused() || p.docFocused() {
		t.Fatalf("second tab: doc=%v issue=%v", p.docFocused(), p.issueFocused())
	}
	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: tea.KeyTab}); handled {
		t.Fatal("the issue leaf claimed Tab instead of yielding it to the cycle")
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.issueFocused() || p.docFocused() || p.paneFocus != terminalLeafID(p.paneRoot) {
		t.Fatalf("third tab did not return to the terminal: focus=%d issue=%v doc=%v",
			p.paneFocus, p.issueFocused(), p.docFocused())
	}
}

func TestApplyIssueLoadedDoesNotDropALiveLeaf(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	issue, _ := p.activeIssuePane()
	fetch := issue.view.Load(issue.leafID, issue.root, "td-1a2b3c", p.ctx.Epoch)
	if !issue.view.Loading() {
		t.Fatal("load did not enter loading")
	}
	p.shellSelected = false
	p.worktrees = nil
	if _, _, ok := p.selectedTerminalSurface(); ok {
		t.Fatal("expected no current surface")
	}
	loaded, ok := fetch().(issueview.LoadedMsg)
	if !ok {
		t.Fatal("load did not return LoadedMsg")
	}
	p.applyIssueLoaded(loaded)
	if issue.view.Loading() {
		t.Fatal("applyIssueLoaded left a live leaf on Loading issue…")
	}
}

// The restore path is the other entrance to the fetch, and the only one whose
// input is a file a hand can edit. It holds the id to the shape a click can
// produce rather than passing an arbitrary string to `td show`, where a leading
// dash would arrive as a flag.
func TestRestoreRefusesAnIssueIDTheClickPathCouldNotHaveProduced(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range []string{"--force", "-f", "td-1a2b3c extra", "../etc/passwd", "td-xyz"} {
		t.Run(saved, func(t *testing.T) {
			p := docPaneTestPlugin(t, root, true)
			layout := &state.PaneLayoutJSON{Root: resolved, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
				B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
					Axis: "rows", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: []state.PaneDocTabJSON{{Path: "clicked.md"}}},
					B: &state.PaneLayoutJSON{Kind: contentKindIssue, Issue: saved},
				}},
			}}
			if cmd := p.restorePaneLayout(layout); cmd == nil {
				t.Fatal("the surviving document did not schedule its load")
			}
			if issue, _ := p.activeIssuePane(); issue != nil {
				t.Fatalf("a malformed persisted id was fetched: %q", issue.view.IssueID())
			}
			if doc, _ := p.activeDocPane(); doc == nil {
				t.Fatal("refusing the issue leaf took its sibling with it")
			}
		})
	}
}
