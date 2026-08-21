package workspace

import (
	"encoding/json"
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
	"github.com/marcus/sidecar/internal/ui"
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
	if p.issues[leafID].view().Loading() {
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
	leaves, _, _ := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
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

	leaves, dividers, fits := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	assertDividersDrawn(t, rows, dividers)
	assertPaneTreeRegions(t, p, leaves, dividers)

	// The golden holds every cell, but only reading the issue's own box proves
	// the issue landed in it rather than in the row above or the column beside.
	box := issueLeafBox(t, p, width, height)
	inner := insetPanelChrome(box)
	within := func(row int) string {
		cells := []rune(ansi.Strip(rows[inner.Y+row]))
		return string(cells[inner.X : inner.X+inner.W])
	}
	if header := within(0); !strings.Contains(header, "td-1a2b3c") {
		t.Fatalf("issue header row = %q, want the issue's identity", header)
	}
	body := make([]string, 0, inner.H-1)
	for row := 1; row < inner.H; row++ {
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
	p.issues[3].view().SetSize(40, 3)
	p.issues[3].view().SetData(issueData)
	p.issues[3].view().Scroll(4)

	layout := p.persistedPaneLayout()
	if layout == nil || layout.Split == nil || layout.Split.B.Split == nil {
		t.Fatalf("persisted layout lost the stack: %#v", layout)
	}
	saved := layout.Split.B.Split.B
	if saved.Kind != contentKindIssue || saved.Issue != "" || saved.Scroll != 0 {
		t.Fatalf("legacy issue fields still written: %#v", saved)
	}
	if len(saved.IssueTabs) != 1 || saved.IssueTabs[0].Issue != "td-1a2b3c" || saved.IssueTabs[0].Scroll != 4 {
		t.Fatalf("persisted issue leaf = %#v, want issueTabs targeting td-1a2b3c", saved)
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
	if issue.view().IssueID() != "td-1a2b3c" || issue.view().ScrollOffset() != 4 || !issue.view().Loading() {
		t.Fatalf("restored issue = %q loading=%v, want td-1a2b3c re-fetching",
			issue.view().IssueID(), issue.view().Loading())
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
	if issue == nil || issue.view().IssueID() != "td-1a2b3c" {
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

// The issue leaf answers the wheel over its own box. The header is the tab
// strip plus the shared X; there is no in-header "q close".
func TestIssuePaneAnswersTheWheelAndHasCloseButton(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)

	const width, height = 100, 24
	rows := composePaneTree(t, p, width, height)
	box := issueLeafBox(t, p, width, height)
	inner := insetPanelChrome(box)

	x, y := inner.X+1, inner.Y+inner.H-1
	body := p.mouseHandler.HitMap.Test(x, y)
	if body == nil || body.ID != regionPaneLeaf {
		t.Fatalf("the issue leaf's body resolves to %#v, want %s", body, regionPaneLeaf)
	}
	before := p.issues[3].view().View()
	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollDown, Region: body, Delta: 3, X: x, Y: y})
	if p.issues[3].view().View() == before {
		t.Fatal("a notch over the issue leaf scrolled nothing")
	}

	if paneCloseRegion(p, 3) == nil {
		t.Fatal("the issue leaf has no close button region")
	}
	header := strings.TrimSpace(ansi.Strip(rows[inner.Y]))
	if strings.Contains(header, "q close") {
		t.Fatalf("issue header still has q close: %q", header)
	}
	if !strings.Contains(header, ui.CloseButtonLabel) {
		t.Fatalf("issue header has no close button: %q", header)
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
			issue.view().SetData(&issueview.Data{
				ID: "td-aaaa11", Title: "Parent", Status: "open", Type: "epic",
				Children: []issueview.Ref{{ID: "td-bbbb22", Title: "Child", Status: "open", Type: "task"}},
			})

			p.mouseHandler.Clear()
			_ = p.renderListView(width, p.height)
			var child issueview.Hit
			found := false
			for _, hit := range issue.view().Hits() {
				if hit.Kind == issueview.HitChild && hit.ID == "td-bbbb22" {
					child, found = hit, true
					break
				}
			}
			if !found {
				t.Fatalf("rendered issue has no child hit: %+v", issue.view().Hits())
			}
			var pane *mouse.Region
			for _, region := range p.mouseHandler.HitMap.Regions() {
				// Document and issue leaves share one region, so the leaf ID
				// the region carries is what names the issue's box.
				if region.ID == regionPaneLeaf && region.Data == issue.leafID {
					copy := region
					pane = &copy
					break
				}
			}
			if pane == nil {
				t.Fatal("rendered issue has no pane hit region")
			}
			inner := insetPanelChrome(pane.Rect)
			x := inner.X + child.X
			y := inner.Y + terminalHeaderRows + child.Y
			resolved := p.mouseHandler.HitMap.Test(x, y)
			if resolved == nil || resolved.ID != regionPaneLeaf {
				t.Fatalf("raw child coordinate (%d,%d) resolves to %#v", x, y, resolved)
			}

			action := p.mouseHandler.HandleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if action.Type != mouse.ActionClick || action.Region == nil || action.Region.ID != regionPaneLeaf {
				t.Fatalf("raw click action = %#v, want an issue-pane click", action)
			}
			cmd := p.handleMouseClick(action)
			if cmd == nil || issue.view().IssueID() != "td-bbbb22" {
				lx, ly := issueViewLocal(action.X, action.Y, action.Region.Rect)
				t.Fatalf("raw child click local=(%d,%d) target=%+v remaining=%+v cmd=%v issue=%q, want a td-bbbb22 load",
					lx, ly, child, issue.view().Hits(), cmd != nil, issue.view().IssueID())
			}
			if len(issue.tabs.Items) != 2 || issue.tabs.Find("td-bbbb22") < 0 {
				t.Fatalf("child click tabs = %v, want parent kept and child appended", issueTabIDs(issue))
			}
			if issue.tabs.Items[0].Value == nil || issue.tabs.Items[0].Value.IssueID() != "td-aaaa11" {
				t.Fatalf("parent tab was retargeted: %v", issueTabIDs(issue))
			}
			deliverLoads(t, p, cmd)
			if issue.view().Data() == nil || issue.view().Data().ID != "td-bbbb22" {
				t.Fatalf("loaded issue = %#v, want td-bbbb22", issue.view().Data())
			}

			// The loaded child's parent row occupies the same rendered row as the
			// parent's child row. Bubble Tea now emits the double-click event for
			// that same raw cell; it must not replay navigation back to the parent.
			issue.view().SetData(&issueview.Data{
				ID: "td-bbbb22", Title: "Child", Status: "open", Type: "task",
				ParentID: "td-aaaa11",
				Parent:   &issueview.Ref{ID: "td-aaaa11", Title: "Parent", Status: "open", Type: "epic"},
			})
			p.mouseHandler.Clear()
			_ = p.renderListView(width, p.height)
			parentAtSameCell := false
			for _, hit := range issue.view().Hits() {
				if hit.Kind == issueview.HitParent && hit.Y == child.Y && x == inner.X+hit.X {
					parentAtSameCell = true
					break
				}
			}
			if !parentAtSameCell {
				t.Fatalf("loaded child did not render its parent at the original raw cell: %+v", issue.view().Hits())
			}
			double := p.mouseHandler.HandleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if double.Type != mouse.ActionDoubleClick {
				t.Fatalf("second raw event = %#v, want double click", double)
			}
			if cmd := p.handleMouseDoubleClick(double); cmd != nil {
				t.Fatal("issue double-click scheduled a second navigation")
			}
			if issue.view().IssueID() != "td-bbbb22" || issue.view().Data() == nil || issue.view().Data().ID != "td-bbbb22" {
				t.Fatalf("double-click navigated to %#v / %q", issue.view().Data(), issue.view().IssueID())
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
	p.issues[leaf.ContentID].view().SetSize(40, 3)
	before := p.issues[leaf.ContentID].view().View()
	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: 'j'}); !handled {
		t.Fatal("the focused issue leaf did not claim j")
	}
	if p.issues[leaf.ContentID].view().View() == before {
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

	if handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'y', Text: "y"}); !handled || cmd == nil {
		t.Fatalf("y on a loaded issue: handled=%v cmd=%v", handled, cmd != nil)
	}
	if handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'Y', Text: "Y"}); !handled || cmd == nil {
		t.Fatalf("Y on a loaded issue: handled=%v cmd=%v", handled, cmd != nil)
	}
	var yank, yankID, closeTab, prevTab, nextTab bool
	for _, cmd := range p.Commands() {
		switch cmd.ID {
		case "yank-issue":
			yank = true
		case "yank-issue-key":
			yankID = true
		case "close-tab":
			closeTab = cmd.Name == "Tab×"
		case "prev-tab":
			prevTab = cmd.Name == "Tab←"
		case "next-tab":
			nextTab = cmd.Name == "Tab→"
		}
	}
	if !yank || !yankID {
		t.Fatalf("workspace-issue Commands() omitted yank: %#v", p.Commands())
	}
	if !closeTab || !prevTab || !nextTab {
		t.Fatalf("workspace-issue Commands() omitted tab actions: %#v", p.Commands())
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
	fetch := issue.view().Load(issue.view().ModelID(), issue.root, "td-1a2b3c", p.ctx.Epoch)
	if !issue.view().Loading() {
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
	if issue.view().Loading() {
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
				t.Fatalf("a malformed persisted id was fetched: %q", issue.view().IssueID())
			}
			if doc, _ := p.activeDocPane(); doc == nil {
				t.Fatal("refusing the issue leaf took its sibling with it")
			}
		})
	}
}

func issueTabIDs(issue *issuePane) []string {
	if issue == nil {
		return nil
	}
	ids := make([]string, 0, len(issue.tabs.Items))
	for _, item := range issue.tabs.Items {
		if item.Value != nil {
			ids = append(ids, item.Value.IssueID())
		} else {
			ids = append(ids, item.Key)
		}
	}
	return ids
}

func openTwoIssueTabs(t *testing.T, p *Plugin) *issuePane {
	t.Helper()
	p.shells[0].Agent.OutputBuf.Update("first is td-1111aa\nsecond is td-2222bb\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "td-1111aa"))
	deliverLoads(t, p, clickTerminalLink(t, p, "td-2222bb"))
	issue, _ := p.activeIssuePane()
	if issue == nil {
		t.Fatal("no issue pane after opening two links")
	}
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" {
		t.Fatalf("tabs after two links = %v, want [td-1111aa td-2222bb]", got)
	}
	if issue.view() == nil || issue.view().IssueID() != "td-2222bb" || issue.tabs.Active != 1 {
		t.Fatalf("active after two links = %q idx=%d, want td-2222bb", issue.view().IssueID(), issue.tabs.Active)
	}
	if issue.tabs.Items[0].Value.ModelID() == issue.tabs.Items[1].Value.ModelID() {
		t.Fatal("two tabs share a model ID")
	}
	return issue
}

func TestOpeningTwoIssueLinksCreatesTwoTabs(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	openTwoIssueTabs(t, p)
}

func TestIssueTabClickAndCycleSelectsWithoutDuplicating(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.width, p.height = 120, 24
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview

	view := p.View(p.width, p.height)
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("preview peer box is unplaced")
	}
	box := issueLeafBox(t, p, peer.W, peer.H)
	y := insetPanelChrome(box).Y
	lines := strings.Split(view, "\n")
	if y < 0 || y >= len(lines) {
		t.Fatalf("issue header row %d is outside the view", y)
	}
	plain := ansi.Strip(lines[y])
	at := strings.Index(plain, "td-1111aa")
	if at < 0 {
		t.Fatalf("td-1111aa is not on the issue header row: %q", plain)
	}
	x := ansi.StringWidth(plain[:at]) + ansi.StringWidth("td-1111aa")/2
	resolved := p.mouseHandler.HitMap.Test(x, y)
	if resolved == nil || resolved.ID != regionIssueTab {
		t.Fatalf("visible title at (%d,%d) resolves to %#v, want %s\nheader=%q", x, y, resolved, regionIssueTab, plain)
	}
	if hit, ok := resolved.Data.(issueTabHit); !ok || hit.Index != 0 {
		t.Fatalf("visible title hit = %#v, want tab 0", resolved.Data)
	}
	_ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if issue.view().IssueID() != "td-1111aa" || issue.tabs.Active != 0 {
		t.Fatalf("clicking td-1111aa selected %q", issue.view().IssueID())
	}
	if len(issue.tabs.Items) != 2 {
		t.Fatalf("click created a tab: %v", issueTabIDs(issue))
	}

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: '}', Text: "}"}); !handled || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("}} selected %q, want td-2222bb", issue.view().IssueID())
	}
	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"}); !handled || issue.view().IssueID() != "td-1111aa" {
		t.Fatalf("{{ selected %q, want td-1111aa", issue.view().IssueID())
	}
}

func TestIssueTabsKeepIndependentScroll(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview

	second := issue.view()
	second.SetSize(40, 3)
	second.Scroll(4)
	scroll2 := second.ScrollOffset()
	if scroll2 == 0 {
		t.Fatal("second tab did not scroll")
	}

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"}); !handled {
		t.Fatal("{ did not cycle")
	}
	first := issue.view()
	if first == second || first.IssueID() != "td-1111aa" {
		t.Fatalf("cycle selected %q", first.IssueID())
	}
	first.SetSize(40, 3)
	if first.ScrollOffset() != 0 {
		t.Fatalf("first tab inherited second's scroll %d", first.ScrollOffset())
	}
	first.Scroll(2)
	scroll1 := first.ScrollOffset()

	p.handleIssueKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if issue.view().ScrollOffset() != scroll2 {
		t.Fatalf("second tab scroll = %d, want %d", issue.view().ScrollOffset(), scroll2)
	}
	p.handleIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	if issue.view().ScrollOffset() != scroll1 {
		t.Fatalf("first tab scroll = %d, want %d", issue.view().ScrollOffset(), scroll1)
	}
}

func TestOpeningAnAlreadyOpenIssueFocusesWithoutDuplicating(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	issue := openTwoIssueTabs(t, p)
	if cmd, ok := p.activateIssueLink("td-1111aa"); !ok {
		t.Fatalf("reopening td-1111aa failed, cmd=%v", cmd != nil)
	}
	if got := issueTabIDs(issue); len(got) != 2 || issue.view().IssueID() != "td-1111aa" || issue.tabs.Active != 0 {
		t.Fatalf("reopen = tabs %v active=%d, want focus without a third tab", got, issue.tabs.Active)
	}
}

func TestEnterOpensParentOrSubtaskAsATab(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("start td-1111aa\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "td-1111aa"))
	issue, leaf := p.activeIssuePane()
	p.paneFocus = leaf.ID
	p.activePane = PanePreview
	issue.view().SetData(&issueview.Data{
		ID: "td-1111aa", Title: "Parent", Status: "open", Type: "epic",
		Children: []issueview.Ref{{ID: "td-2222bb", Title: "Child", Status: "open", Type: "task"}},
	})
	issue.view().SetActive(true)
	issue.view().SetFocused(true)
	issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if issue.view().SelectedID() != "td-2222bb" {
		t.Fatalf("selected %q, want the child row", issue.view().SelectedID())
	}

	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd == nil || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("enter: handled=%v cmd=%v issue=%q", handled, cmd != nil, issue.view().IssueID())
	}
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" {
		t.Fatalf("enter tabs = %v, want parent kept and child appended", got)
	}
	deliverLoads(t, p, cmd)
	issue.view().SetData(&issueview.Data{
		ID: "td-2222bb", Title: "Child", Status: "open", Type: "task",
		ParentID: "td-1111aa",
		Parent:   &issueview.Ref{ID: "td-1111aa", Title: "Parent", Status: "open", Type: "epic"},
	})
	issue.view().SetActive(true)
	_, _ = issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if issue.view().SelectedID() != "td-1111aa" {
		t.Fatalf("selected %q, want the parent row", issue.view().SelectedID())
	}

	handled, cmd = p.handleIssueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled {
		t.Fatal("enter on the parent row was not handled")
	}
	if issue.view().IssueID() != "td-1111aa" || len(issue.tabs.Items) != 2 {
		t.Fatalf("enter on existing parent = %v, want a focus not a third tab", issueTabIDs(issue))
	}
	if cmd != nil {
		t.Fatal("focusing an already-open parent scheduled a load")
	}
}

func TestCloseActiveIssueTabThenLastTabClosesThePane(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview

	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if still, _ := p.activeIssuePane(); !handled || cmd != nil || still == nil {
		t.Fatalf("x closed the pane with two tabs: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got := issueTabIDs(issue); len(got) != 1 || got[0] != "td-1111aa" {
		t.Fatalf("x left %v, want the first tab", got)
	}

	handled, cmd = p.handleIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || cmd == nil {
		t.Fatalf("last x: handled=%v cmd=%v", handled, cmd != nil)
	}
	if issue, _ := p.activeIssuePane(); issue != nil || len(p.issues) != 0 {
		t.Fatalf("last x left the pane: %#v", p.issues)
	}
}

func TestStaleIssueLoadedMsgIsIgnored(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("first is td-1111aa\nsecond is td-2222bb\n")
	firstCmd := clickTerminalLink(t, p, "td-1111aa")
	var first issueview.LoadedMsg
	if batch, ok := firstCmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if loaded, ok := child().(issueview.LoadedMsg); ok {
				first = loaded
			}
		}
	}
	if first.IssueID != "td-1111aa" {
		t.Fatalf("first load = %#v", first)
	}
	deliverLoads(t, p, clickTerminalLink(t, p, "td-2222bb"))
	issue, leaf := p.activeIssuePane()
	p.paneFocus = leaf.ID
	p.activePane = PanePreview
	secondID := issue.view().ModelID()

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"}); !handled {
		t.Fatal("could not select the first tab to close it")
	}
	p.handleIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if issue, _ = p.activeIssuePane(); issue == nil || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("after closing first tab: %v", issueTabIDs(issue))
	}

	p.applyIssueLoaded(first)
	if issue.view().IssueID() != "td-2222bb" || issue.view().Data() == nil || issue.view().Data().ID != "td-2222bb" {
		t.Fatalf("closed tab's result landed on the survivor: %#v", issue.view().Data())
	}

	p.applyIssueLoaded(issueview.LoadedMsg{
		ModelID:           secondID + 99,
		RequestGeneration: 1,
		Epoch:             p.ctx.Epoch,
		IssueID:           "td-3333cc",
		Data:              &issueview.Data{ID: "td-3333cc", Title: "stale"},
	})
	if issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("foreign model id retargeted the tab to %q", issue.view().IssueID())
	}
}

func TestIssueTabHeaderHasNoCloseChipOrHint(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 80, 20
	issue := openTwoIssueTabs(t, p)
	strip := layoutIssueTabStrip(issue, 48, true)
	got := ansi.Strip(strip.Row)
	if strings.Contains(got, "q close") {
		t.Fatalf("issue strip still has chips/hints: %q", got)
	}
	if strings.Count(got, "×") != 2 {
		t.Fatalf("issue strip = %q, want one × per tab", got)
	}
	if !strings.Contains(got, "td-1111aa") || !strings.Contains(got, "td-2222bb") {
		t.Fatalf("issue strip dropped a tab: %q", got)
	}
}

func firstIssueLeafTabs(layout *state.PaneLayoutJSON) (tabs []state.PaneIssueTabJSON, active int) {
	if layout == nil {
		return nil, 0
	}
	if len(layout.IssueTabs) > 0 || layout.Issue != "" {
		if len(layout.IssueTabs) > 0 {
			return layout.IssueTabs, layout.Active
		}
		return []state.PaneIssueTabJSON{{Issue: layout.Issue, Scroll: layout.Scroll}}, 0
	}
	if layout.Split == nil {
		return nil, 0
	}
	if tabs, active = firstIssueLeafTabs(layout.Split.B); len(tabs) > 0 {
		return tabs, active
	}
	return firstIssueLeafTabs(layout.Split.A)
}

func layoutHasIssueID(layout *state.PaneLayoutJSON, id string) bool {
	if layout == nil {
		return false
	}
	if layout.Issue == id {
		return true
	}
	for _, tab := range layout.IssueTabs {
		if tab.Issue == id {
			return true
		}
	}
	if layout.Split == nil {
		return false
	}
	return layoutHasIssueID(layout.Split.A, id) || layoutHasIssueID(layout.Split.B, id)
}

func assertIssueLeafOmitsLegacy(t *testing.T, leaf *state.PaneLayoutJSON) {
	t.Helper()
	if leaf == nil {
		t.Fatal("missing issue leaf")
	}
	if leaf.Issue != "" || leaf.Scroll != 0 {
		t.Fatalf("legacy fields still set: %#v", leaf)
	}
	raw, err := json.Marshal(leaf)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["issue"]; ok {
		t.Fatalf("legacy issue field present: %s", raw)
	}
	if _, ok := decoded["scroll"]; ok {
		t.Fatalf("legacy scroll field present: %s", raw)
	}
	if _, ok := decoded["issueTabs"]; !ok {
		t.Fatalf("issueTabs missing: %s", raw)
	}
}

func TestIssueTabsPersistAcrossShellSwitch(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview
	second := issue.view()
	second.SetSize(40, 3)
	second.Scroll(4)
	scroll2 := second.ScrollOffset()
	if scroll2 == 0 {
		t.Fatal("second tab did not scroll")
	}
	p.handleIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	first := issue.view()
	first.SetSize(40, 3)
	first.Scroll(2)
	scroll1 := first.ScrollOffset()
	p.handleIssueKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	p.saveSelectionState()

	p.selectTopShellAt(1)
	p.saveSelectionState()
	tabs, active := firstIssueLeafTabs(workspacePaneLayout(*saved, "shell:test-shell"))
	if len(tabs) != 2 || active != 1 || tabs[0].Issue != "td-1111aa" || tabs[1].Issue != "td-2222bb" {
		t.Fatalf("A tabs missing after selecting B: %#v active=%d", tabs, active)
	}
	if tabs[0].Scroll != scroll1 || tabs[1].Scroll != scroll2 {
		t.Fatalf("A scroll missing after selecting B: %#v want %d/%d", tabs, scroll1, scroll2)
	}
	if issue, _ := p.activeIssuePane(); issue != nil || p.paneRoot.Split != nil {
		t.Fatalf("B live tree is not terminal-only: %#v", p.paneRoot)
	}

	p.shells[1].Agent.OutputBuf.Update("B only has td-3333cc\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "td-3333cc"))
	bIssue, _ := p.activeIssuePane()
	if got := issueTabIDs(bIssue); len(got) != 1 || got[0] != "td-3333cc" {
		t.Fatalf("B tabs = %v, want [td-3333cc]", got)
	}
	if layoutHasIssueID(workspacePaneLayout(*saved, "shell:test-shell"), "td-3333cc") {
		t.Fatalf("B leaked onto A: %#v", saved.PaneLayouts)
	}
	if !layoutHasIssueID(workspacePaneLayout(*saved, "shell:test-shell-b"), "td-3333cc") {
		t.Fatalf("B did not persist its own tab: %#v", saved.PaneLayouts)
	}

	p.selectTopShellAt(0)
	restored, _ := p.activeIssuePane()
	if got := issueTabIDs(restored); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || restored.tabs.Active != 1 {
		t.Fatalf("selecting A again = %v active=%d", got, restored.tabs.Active)
	}
	if restored.view() == nil || restored.view().IssueID() != "td-2222bb" || restored.view().ScrollOffset() != scroll2 {
		t.Fatalf("A active scroll = %q %d, want td-2222bb @ %d", restored.view().IssueID(), restored.view().ScrollOffset(), scroll2)
	}
	if restored.tabs.Items[0].Value == nil || restored.tabs.Items[0].Value.ScrollOffset() != scroll1 {
		t.Fatalf("A first-tab scroll = %d, want %d", restored.tabs.Items[0].Value.ScrollOffset(), scroll1)
	}
	assertIssueLeafOmitsLegacy(t, workspacePaneLayout(*saved, "shell:test-shell").Split.B)
}

func TestIssueTabsSurviveQuitAndReopen(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	issue.view().SetSize(40, 3)
	issue.view().Scroll(5)
	p.saveSelectionState()
	if !state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) {
		t.Fatal("open session wrote Open=false")
	}

	reopened := docPaneTestPlugin(t, root, true)
	reopened.ctx.ProjectRoot = root
	reopened.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return *saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { *saved = next; return nil },
	}
	if !reopened.restoreSelectionState() {
		t.Fatal("relaunch restored no selection")
	}
	if reopened.paneRestoreCmd == nil {
		t.Fatal("relaunch scheduled no loads")
	}
	restored, _ := reopened.activeIssuePane()
	if got := issueTabIDs(restored); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || restored.tabs.Active != 1 {
		t.Fatalf("relaunch tabs = %v active=%d", got, restored.tabs.Active)
	}
	if restored.view() == nil || restored.view().IssueID() != "td-2222bb" || restored.view().ScrollOffset() != 5 || !restored.view().Loading() {
		t.Fatalf("relaunch active = %q scroll=%d loading=%v", restored.view().IssueID(), restored.view().ScrollOffset(), restored.view().Loading())
	}
	if restored.tabs.Items[0].Value == nil || !restored.tabs.Items[0].Value.NeedsLoad() {
		t.Fatal("inactive relaunch tab was fetched eagerly")
	}
	if restored.tabs.Items[1].Value == nil || restored.tabs.Items[1].Value.NeedsLoad() {
		t.Fatal("active relaunch tab was not fetched")
	}
}

func TestRestoreIssueLayoutLoadsOnlyActiveTab(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolved, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
		B: &state.PaneLayoutJSON{Kind: contentKindIssue, Active: 1, IssueTabs: []state.PaneIssueTabJSON{
			{Issue: "td-1111aa", Scroll: 2},
			{Issue: "td-2222bb", Scroll: 4},
			{Issue: "td-3333cc", Scroll: 1},
		}},
	}}
	cmd := p.restorePaneLayout(layout)
	if cmd == nil {
		t.Fatal("restore scheduled no load")
	}
	issue, _ := p.activeIssuePane()
	if issue == nil || len(issue.tabs.Items) != 3 || issue.tabs.Active != 1 {
		t.Fatalf("restored tabs = %v active=%d", issueTabIDs(issue), issue.tabs.Active)
	}
	for i, item := range issue.tabs.Items {
		if item.Value == nil {
			t.Fatalf("tab %d has no model", i)
		}
		if item.Value.NeedsLoad() == (i == 1) {
			t.Fatalf("tab %d NeedsLoad=%v, want only the active tab loaded", i, item.Value.NeedsLoad())
		}
		if item.Value.IssueID() != layout.Split.B.IssueTabs[i].Issue {
			t.Fatalf("tab %d id = %q", i, item.Value.IssueID())
		}
		if item.Value.ModelID() == 0 {
			t.Fatalf("tab %d has no model id", i)
		}
	}
	if issue.tabs.Items[0].Value.ModelID() == issue.tabs.Items[1].Value.ModelID() {
		t.Fatal("restored tabs share a model ID")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		t.Fatalf("restore issued a batch, want one Load: %T", msg)
	}
	loaded, ok := msg.(issueview.LoadedMsg)
	if !ok || loaded.IssueID != "td-2222bb" {
		t.Fatalf("restore load = %#v, want td-2222bb", msg)
	}

	p.paneFocus = issue.leafID
	p.activePane = PanePreview
	handled, load := p.handleIssueKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if !handled || load == nil || issue.tabs.Active != 2 || issue.tabs.Items[2].Value.NeedsLoad() {
		t.Fatalf("cycle onto a lazy tab: handled=%v load=%v active=%d needsLoad=%v",
			handled, load != nil, issue.tabs.Active, issue.tabs.Items[2].Value != nil && issue.tabs.Items[2].Value.NeedsLoad())
	}
}

func TestInvalidIssueTabEntriesArePruned(t *testing.T) {
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
			B: &state.PaneLayoutJSON{Kind: contentKindIssue, Active: 3, IssueTabs: []state.PaneIssueTabJSON{
				{Issue: "--force"},
				{Issue: "td-1111aa", Scroll: 2},
				{Issue: "td-xyz"},
				{Issue: "td-2222bb", Scroll: 3},
			}},
		}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("the surviving tabs did not schedule a load")
	}
	if doc, _ := p.activeDocPane(); doc == nil {
		t.Fatal("pruning issue tabs took the document sibling")
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || issue.tabs.Active != 1 {
		t.Fatalf("pruned tabs = %v active=%d", got, issue.tabs.Active)
	}
	if issue.view() == nil || issue.view().ScrollOffset() != 3 {
		t.Fatalf("active scroll = %d, want 3", issue.view().ScrollOffset())
	}
}

func TestAllInvalidIssueTabsCollapseTheLeaf(t *testing.T) {
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
			B: &state.PaneLayoutJSON{Kind: contentKindIssue, IssueTabs: []state.PaneIssueTabJSON{
				{Issue: "--force"},
				{Issue: "td-xyz"},
			}},
		}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("the surviving document did not schedule its load")
	}
	if issue, _ := p.activeIssuePane(); issue != nil {
		t.Fatalf("all-invalid issue leaf was restored: %v", issueTabIDs(issue))
	}
	if doc, _ := p.activeDocPane(); doc == nil || p.paneRoot.Split == nil {
		t.Fatalf("all-invalid issue leaf reset the layout: root=%#v", p.paneRoot)
	}
}

func TestLegacyIssueScrollRestoresAsOneTabThenSavesIssueTabs(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolved, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
		B: &state.PaneLayoutJSON{Kind: contentKindIssue, Issue: "td-1a2b3c", Scroll: 4},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("legacy issue did not schedule a load")
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 1 || got[0] != "td-1a2b3c" || issue.view().ScrollOffset() != 4 || !issue.view().Loading() {
		t.Fatalf("legacy restore = %v scroll=%d loading=%v", got, issue.view().ScrollOffset(), issue.view().Loading())
	}

	saved := p.encodePaneNode(p.paneRoot)
	if saved == nil || saved.Split == nil {
		t.Fatalf("re-encode lost the tree: %#v", saved)
	}
	leaf := saved.Split.B
	if len(leaf.IssueTabs) != 1 || leaf.IssueTabs[0].Issue != "td-1a2b3c" || leaf.IssueTabs[0].Scroll != 4 {
		t.Fatalf("save after legacy = %#v", leaf)
	}
	assertIssueLeafOmitsLegacy(t, leaf)
}

func TestIssuePaneQHidesAndRestoresAcrossSwitch(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview

	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if still, _ := p.activeIssuePane(); !handled || cmd == nil || still != nil || p.paneRoot.Split != nil {
		t.Fatalf("q did not hide to full-width terminal: handled=%v root=%#v", handled, p.paneRoot)
	}
	hidden := workspacePaneLayout(*saved, "shell:test-shell")
	tabs, active := firstIssueLeafTabs(hidden)
	if state.PaneLayoutOpen(hidden) || len(tabs) != 2 || active != 1 || tabs[0].Issue != "td-1111aa" || tabs[1].Issue != "td-2222bb" {
		t.Fatalf("q persist = %#v tabs=%#v active=%d", hidden, tabs, active)
	}

	p.selectTopShellAt(1)
	p.saveSelectionState()
	if issue, _ := p.activeIssuePane(); issue != nil || p.paneRoot.Split != nil {
		t.Fatal("B live tree is not terminal-only")
	}
	if !layoutHasIssueID(workspacePaneLayout(*saved, "shell:test-shell"), "td-1111aa") {
		t.Fatalf("switch-away dropped A's hidden tabs: %#v", saved.PaneLayouts)
	}

	p.selectTopShellAt(0)
	reopened, _ := p.activeIssuePane()
	if got := issueTabIDs(reopened); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || reopened.view().IssueID() != "td-2222bb" {
		t.Fatalf("switch-back after q = %v active=%q", got, reopened.view().IssueID())
	}
}

func TestIssuePaneEscHidesLikeQ(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	p.shells[0].Agent.OutputBuf.Update("only td-1111aa\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "td-1111aa"))
	issue, leaf := p.activeIssuePane()
	p.paneFocus = leaf.ID
	p.activePane = PanePreview

	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || cmd == nil || issue == nil {
		t.Fatalf("esc: handled=%v cmd=%v", handled, cmd != nil)
	}
	if still, _ := p.activeIssuePane(); still != nil || p.paneRoot.Split != nil {
		t.Fatalf("esc did not hide: root=%#v", p.paneRoot)
	}
	if state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) || !layoutHasIssueID(workspacePaneLayout(*saved, "shell:test-shell"), "td-1111aa") {
		t.Fatalf("esc persist = %#v", saved.PaneLayouts)
	}
}

func TestIssuePaneLastXForgetsAcrossSwitch(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); !handled || p.paneRoot.Split == nil {
		t.Fatal("first x closed the pane")
	}
	if !state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) {
		t.Fatal("x on a non-last tab hid the pane")
	}
	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || cmd == nil {
		t.Fatalf("last x: handled=%v cmd=%v", handled, cmd != nil)
	}
	if still, _ := p.activeIssuePane(); still != nil || p.paneRoot.Split != nil {
		t.Fatalf("last x did not forget: root=%#v", p.paneRoot)
	}
	forgotten := workspacePaneLayout(*saved, "shell:test-shell")
	if layoutHasIssueID(forgotten, "td-1111aa") || layoutHasIssueID(forgotten, "td-2222bb") {
		t.Fatalf("last x kept tabs: %#v", forgotten)
	}

	p.selectTopShellAt(1)
	p.saveSelectionState()
	p.selectTopShellAt(0)
	if issue, _ := p.activeIssuePane(); issue != nil || p.paneRoot.Split != nil {
		t.Fatalf("forgotten pane came back: %#v", p.paneRoot)
	}
}

func TestIssuePaneClickWhileHiddenReopensRememberedSet(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p, saved := persistDocPanePlugin(t, root)
	issue := openTwoIssueTabs(t, p)
	p.paneFocus = issue.leafID
	p.activePane = PanePreview
	if p.paneRoot.Split == nil {
		t.Fatal("expected an issue split before hide")
	}
	p.paneRoot.Split.Ratio = 45

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.paneRoot.Split != nil {
		t.Fatal("q did not hide")
	}
	if _, ok := p.activateIssueLink("td-1111aa"); !ok {
		t.Fatal("click existing while hidden failed")
	}
	reopened, _ := p.activeIssuePane()
	if got := issueTabIDs(reopened); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || reopened.view().IssueID() != "td-1111aa" {
		t.Fatalf("click existing while hidden = %v active=%q", got, reopened.view().IssueID())
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 45 {
		t.Fatalf("reopen ratio = %#v, want 45", p.paneRoot.Split)
	}
	if !state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) {
		t.Fatal("click while hidden left Open=false")
	}

	if handled, _ := p.handleIssueKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.paneRoot.Split != nil {
		t.Fatal("second q did not hide")
	}
	p.shells[0].Agent.OutputBuf.Update("first is td-1111aa\nsecond is td-2222bb\nthird is td-3333cc\n")
	if _, ok := p.activateIssueLink("td-3333cc"); !ok {
		t.Fatal("click new while hidden failed")
	}
	appended, _ := p.activeIssuePane()
	if got := issueTabIDs(appended); len(got) != 3 || got[2] != "td-3333cc" || appended.view().IssueID() != "td-3333cc" {
		t.Fatalf("click new while hidden = %v active=%q", got, appended.view().IssueID())
	}
}

func TestRestoreHiddenIssueLayoutKeepsTabsWithoutSplit(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayouts: map[string]*state.PaneLayoutJSON{
			"shell:test-shell": {Root: resolved, Surface: "shell:test-shell", Open: false, Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 41,
				A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
				B: &state.PaneLayoutJSON{Kind: contentKindIssue, Active: 1, IssueTabs: []state.PaneIssueTabJSON{
					{Issue: "td-1111aa", Scroll: 2},
					{Issue: "td-2222bb", Scroll: 3},
				}},
			}},
		},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("saved shell selection was not restored")
	}
	if issue, _ := p.activeIssuePane(); issue != nil || p.paneRoot.Split != nil {
		t.Fatalf("relaunch restored a hidden split: root=%#v", p.paneRoot)
	}
	if !layoutHasIssueID(workspacePaneLayout(saved, "shell:test-shell"), "td-2222bb") {
		t.Fatalf("relaunch dropped hidden tabs: %#v", saved.PaneLayouts)
	}

	if _, ok := p.activateIssueLink("td-2222bb"); !ok {
		t.Fatal("click after relaunch hide failed")
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("click after relaunch hide = %v active=%q", got, issue.view().IssueID())
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 41 {
		t.Fatalf("relaunch reopen ratio = %#v, want 41", p.paneRoot.Split)
	}
	if issue.view().ScrollOffset() != 3 {
		t.Fatalf("reopened active scroll = %d, want 3", issue.view().ScrollOffset())
	}
}

func openSteelThreadTwoIssues(t *testing.T, p *Plugin) (*docPane, *issuePane) {
	t.Helper()
	applyDocOpen(t, p, p.openTerminalPath("clicked.md", 0))
	issue := openTwoIssueTabs(t, p)
	doc, _ := p.activeDocPane()
	if doc == nil || docTabTitles(doc)[0] != "clicked.md" {
		t.Fatalf("steel thread missing clicked.md: %v", docTabTitles(doc))
	}
	return doc, issue
}

func hideFocusedIssue(t *testing.T, p *Plugin) {
	t.Helper()
	issue, leaf := p.activeIssuePane()
	if issue == nil || leaf == nil {
		t.Fatal("no issue to hide")
	}
	p.paneFocus = leaf.ID
	p.activePane = PanePreview
	if handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || cmd == nil {
		t.Fatalf("q did not hide issue: handled=%v cmd=%v", handled, cmd != nil)
	}
	if still, _ := p.activeIssuePane(); still != nil {
		t.Fatal("issue leaf survived q")
	}
}

func hideFocusedDoc(t *testing.T, p *Plugin) {
	t.Helper()
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatal("no document to hide")
	}
	p.paneFocus = leaf.ID
	p.activePane = PanePreview
	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || cmd == nil {
		t.Fatalf("q did not hide document: handled=%v cmd=%v", handled, cmd != nil)
	}
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("document leaf survived q")
	}
}

func TestSequentialHideIssueThenDocRestoresBothSets(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	p, saved := persistDocPanePlugin(t, root)
	openSteelThreadTwoIssues(t, p)

	hideFocusedIssue(t, p)
	if p.activeDocPaneOrNil() == nil {
		t.Fatal("hiding the issue took the document with it")
	}
	hideFocusedDoc(t, p)
	if p.paneRoot.Split != nil {
		t.Fatalf("sequential hide left a split: %#v", p.paneRoot)
	}
	hidden := workspacePaneLayout(*saved, "shell:test-shell")
	docs, _ := firstDocLeafTabs(hidden)
	issues, _ := firstIssueLeafTabs(hidden)
	if state.PaneLayoutOpen(hidden) || len(docs) != 1 || docs[0].Path != "clicked.md" ||
		len(issues) != 2 || issues[0].Issue != "td-1111aa" || issues[1].Issue != "td-2222bb" {
		t.Fatalf("sequential hide persist = docs=%#v issues=%#v", docs, issues)
	}

	if _, ok := p.activateIssueLink("td-1111aa"); !ok {
		t.Fatal("activate after sequential hide failed")
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || issue.view().IssueID() != "td-1111aa" {
		t.Fatalf("restored issues = %v active=%q", got, issue.view().IssueID())
	}
	if titles := docTabTitles(p.activeDocPaneOrNil()); len(titles) != 1 || titles[0] != "clicked.md" {
		t.Fatalf("restored docs = %v, want [clicked.md]", titles)
	}
}

func TestSequentialHideDocThenIssueRestoresBothSets(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	p, saved := persistDocPanePlugin(t, root)
	openSteelThreadTwoIssues(t, p)

	hideFocusedDoc(t, p)
	if issue, _ := p.activeIssuePane(); issue == nil {
		t.Fatal("hiding the document took the issue with it")
	}
	hideFocusedIssue(t, p)
	if p.paneRoot.Split != nil {
		t.Fatalf("reverse sequential hide left a split: %#v", p.paneRoot)
	}
	hidden := workspacePaneLayout(*saved, "shell:test-shell")
	docs, _ := firstDocLeafTabs(hidden)
	issues, _ := firstIssueLeafTabs(hidden)
	if state.PaneLayoutOpen(hidden) || len(docs) != 1 || docs[0].Path != "clicked.md" ||
		len(issues) != 2 || issues[0].Issue != "td-1111aa" || issues[1].Issue != "td-2222bb" {
		t.Fatalf("reverse sequential hide persist = docs=%#v issues=%#v", docs, issues)
	}

	applyDocOpen(t, p, p.openTerminalPath("clicked.md", 0))
	if titles := docTabTitles(p.activeDocPaneOrNil()); len(titles) != 1 || titles[0] != "clicked.md" {
		t.Fatalf("open file after reverse hide = %v", titles)
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" {
		t.Fatalf("issues after opening a file = %v", got)
	}
}

func TestHideIssueKeepsMutatedLiveDocsOnReopen(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	writeDocPaneFixture(t, root, "other.md", "# other\n")
	p, _ := persistDocPanePlugin(t, root)
	openSteelThreadTwoIssues(t, p)

	hideFocusedIssue(t, p)
	applyDocOpen(t, p, p.openTerminalPath("other.md", 0))
	if titles := docTabTitles(p.activeDocPaneOrNil()); len(titles) != 2 || titles[0] != "clicked.md" || titles[1] != "other.md" {
		t.Fatalf("mutated live docs = %v", titles)
	}

	if _, ok := p.activateIssueLink("td-1111aa"); !ok {
		t.Fatal("activate after hiding issue failed")
	}
	if titles := docTabTitles(p.activeDocPaneOrNil()); len(titles) != 2 || titles[0] != "clicked.md" || titles[1] != "other.md" {
		t.Fatalf("reopening the issue reset docs to %v, want [clicked.md other.md]", titles)
	}
	issue, _ := p.activeIssuePane()
	if got := issueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" || issue.view().IssueID() != "td-1111aa" {
		t.Fatalf("reinserted issues = %v active=%q", got, issue.view().IssueID())
	}
}

func TestHiddenLegacyIssueSaveWritesIssueTabsOnly(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayouts: map[string]*state.PaneLayoutJSON{
			"shell:test-shell": {Root: resolved, Surface: "shell:test-shell", Open: false, Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
				B: &state.PaneLayoutJSON{Kind: contentKindIssue, Issue: "td-1a2b3c", Scroll: 4},
			}},
		},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("saved shell selection was not restored")
	}
	if issue, _ := p.activeIssuePane(); issue != nil || p.paneRoot.Split != nil {
		t.Fatalf("relaunch restored a hidden legacy split: root=%#v", p.paneRoot)
	}
	leaf := firstLayoutLeafOfKind(workspacePaneLayout(saved, "shell:test-shell"), contentKindIssue)
	if leaf == nil || len(leaf.IssueTabs) != 1 || leaf.IssueTabs[0].Issue != "td-1a2b3c" || leaf.IssueTabs[0].Scroll != 4 {
		t.Fatalf("hidden legacy save = %#v", leaf)
	}
	assertIssueLeafOmitsLegacy(t, leaf)
}
