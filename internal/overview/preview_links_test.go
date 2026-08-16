package overview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func stubPreviewTd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = show ]; then\n" +
		"  if [ \"$2\" = td-acde12 ]; then parent=',\"parent_id\":\"td-196c42\"'; else parent=''; fi\n" +
		`  printf '{"id":"%s","title":"Issue %s","status":"open","type":"task","priority":"P2"%s}\n' "$2" "$2" "$parent"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$2\" = td-196c42 ]; then\n" +
		`  printf '{"id":"td-196c42","title":"Parent","status":"open","type":"epic","priority":"P1","children":[{"id":"td-acde12","title":"Child issue","status":"open","type":"task","priority":"P2","children":[]}]}\n'` + "\n" +
		"else\n" +
		`  printf '{"id":"%s","title":"Child issue","status":"open","type":"task","priority":"P2","children":[]}\n' "$2"` + "\n" +
		"fi\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func linkPreviewModel(t *testing.T, kind workspaceinventory.Kind) *Model {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Hello from preview\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, recorder := previewModel(t)
	ws := m.catalog["a"]
	ws.Kind = kind
	ws.Path = root
	ws.ProjectRoot = root
	if kind == workspaceinventory.KindShell {
		ws.Name = "shell-one"
		ws.IsMain = false
	}
	m.catalog["a"] = ws
	m.results["sidecar"] = workspaceinventory.ProjectResult{
		ProjectKey: "sidecar", ProjectName: "sidecar", ProjectRoot: root,
		Workspaces: []workspaceinventory.Workspace{ws},
	}
	m.syncBoard()
	m.workspaces.SelectID("a")
	recorder.output["%1"] = "see README.md then https://example.com/docs and review td-196c42\n"
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	return m
}

func previewNeedleAction(t *testing.T, m *Model, needle string) mouse.MouseAction {
	t.Helper()
	m.WorkspacesView(previewWide, previewTall)
	geometry, ok := m.previewGeometry()
	if !ok {
		t.Fatal("no preview geometry")
	}
	buffer := m.previewBuffer()
	if buffer == nil {
		t.Fatal("no preview buffer")
	}
	count := buffer.LineCount()
	for line := 0; line < count; line++ {
		text, ok := tty.LineTextAt(buffer, line)
		if !ok {
			continue
		}
		plain := ansi.Strip(uiExpand(text))
		col := strings.Index(plain, needle)
		if col < 0 {
			continue
		}
		return mouse.MouseAction{
			Type: mouse.ActionClick,
			X:    geometry.Content.X + col + 1,
			Y:    geometry.Content.Y,
			Region: &mouse.Region{
				ID:   previewRegionKind,
				Data: previewRegionKind,
				Rect: geometry.Content,
			},
		}
	}
	t.Fatalf("needle %q not found", needle)
	return mouse.MouseAction{}
}

func uiExpand(line string) string {
	return strings.ReplaceAll(line, "\t", "        ")
}

func TestGlobalPreviewUnderlinesAndOpensFileLinks(t *testing.T) {
	for _, kind := range []workspaceinventory.Kind{workspaceinventory.KindWorktree, workspaceinventory.KindShell} {
		t.Run(string(kind), func(t *testing.T) {
			m := linkPreviewModel(t, kind)
			view := m.WorkspacesView(previewWide, previewTall)
			if !strings.Contains(view, "\x1b[4m") || !strings.Contains(ansi.Strip(view), "README.md") {
				t.Fatalf("file link was not underlined: %q", ansi.Strip(view))
			}
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
				X:      previewNeedleAction(t, m, "README.md").X,
				Y:      previewNeedleAction(t, m, "README.md").Y,
				Button: tea.MouseLeft,
			}))
			if m.PreviewInteractive() {
				t.Fatal("a file link click started typing")
			}
			if m.preview.doc == nil || m.preview.doc.view().Title() != "README.md" {
				t.Fatalf("doc = %#v", m.preview.doc)
			}
			if !strings.Contains(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "Hello from preview") {
				t.Fatal("doc pane did not render the file")
			}
		})
	}
}

func TestGlobalPreviewURLAndIssueActivationStayDistinct(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	urlAction := previewNeedleAction(t, m, "https://")
	cmd, claimed := m.activatePreviewLinkAt(urlAction, false)
	if !claimed || cmd == nil {
		t.Fatal("URL was not claimed")
	}
	if m.preview.doc != nil {
		t.Fatal("URL opened a doc pane")
	}

	issueAction := previewNeedleAction(t, m, "td-196c42")
	cmd, claimed = m.activatePreviewLinkAt(issueAction, false)
	if !claimed || cmd == nil {
		t.Fatal("issue id was not activated")
	}
	run(t, m, cmd)
	if m.preview.doc != nil {
		t.Fatal("issue id opened the document slot")
	}
	if m.preview.issue == nil || m.preview.issue.view() == nil || m.preview.issue.view().Data() == nil || m.preview.issue.view().Data().ID != "td-196c42" {
		t.Fatalf("issue preview = %#v", m.preview.issue)
	}

	spans := terminallink.Scan("review td-196c42", nil, nil)
	if len(spans) != 1 || spans[0].Kind != terminallink.KindIssue {
		t.Fatalf("scanner issue span = %#v", spans)
	}
}

func TestGlobalIssuePreviewRawChildClickUsesRenderedCoordinates(t *testing.T) {
	// 90 is the narrow case now: every leaf pays for its own border, so a split
	// needs the terminal's floor plus the issue's floor plus two panel frames.
	// The project workspace budgets a split the same way and refuses at the same
	// kind of width — see paneframe.ChromeFloors.
	for _, width := range []int{90, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			stubPreviewTd(t)
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			m.WorkspacesView(width, previewTall)
			run(t, m, m.openPreviewIssue("td-196c42"))
			m.WorkspacesView(width, previewTall)
			issue := m.preview.issue
			if issue == nil || issue.view() == nil || issue.view().Data() == nil {
				t.Fatalf("issue did not load: %#v", issue)
			}

			var child issueview.Hit
			found := false
			for _, hit := range issue.view().Hits() {
				if hit.Kind == issueview.HitChild && hit.ID == "td-acde12" {
					child, found = hit, true
					break
				}
			}
			if !found {
				t.Fatalf("rendered issue has no child hit: %+v", issue.view().Hits())
			}
			var region *mouse.Region
			for _, candidate := range m.workspacesMouse.HitMap.Regions() {
				if kind, ok := candidate.Data.(string); ok && kind == previewIssueRegionKind {
					copy := candidate
					region = &copy
					break
				}
			}
			if region == nil {
				t.Fatal("rendered issue has no mouse region")
			}
			x := region.Rect.X + child.X
			y := region.Rect.Y + termpreview.HeaderRows + child.Y
			resolved := m.workspacesMouse.HitMap.Test(x, y)
			if resolved == nil || resolved.ID != previewIssueRegionKind {
				t.Fatalf("raw coordinate (%d,%d) resolves to %#v", x, y, resolved)
			}

			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
			if issue.view() == nil || issue.view().Data() == nil || issue.view().Data().ID != "td-acde12" {
				t.Fatalf("raw child click loaded %#v, want td-acde12", issue.view())
			}
			if got := previewIssueTabIDs(issue); len(got) != 2 || got[0] != "td-196c42" || got[1] != "td-acde12" {
				t.Fatalf("child click tabs = %v, want parent kept and child appended", got)
			}

			m.WorkspacesView(width, previewTall)
			parentAtSameCell := false
			for _, hit := range issue.view().Hits() {
				if hit.Kind == issueview.HitParent && hit.Y == child.Y && x == region.Rect.X+hit.X {
					parentAtSameCell = true
					break
				}
			}
			if !parentAtSameCell {
				t.Fatalf("loaded child did not render its parent at the original raw cell: %+v", issue.view().Hits())
			}
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
			if issue.view() == nil || issue.view().Data() == nil || issue.view().Data().ID != "td-acde12" || issue.view().IssueID() != "td-acde12" {
				t.Fatalf("double-click replay navigated to %#v / %q", issue.view().Data(), issue.view().IssueID())
			}
		})
	}
}

func TestGlobalIssueAndDocumentShareTheProjectPanePlacement(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	issue := m.preview.issue
	if issue == nil {
		t.Fatal("issue did not open")
	}
	rendered := m.renderPreviewIssue(issue, termpreview.Box{W: 32, H: 10})
	lines := strings.Split(ansi.Strip(rendered), "\n")
	for row, line := range lines[1:] {
		if ansi.StringWidth(line) != 32 || line[0] != ' ' || line[len(line)-1] != ' ' {
			t.Fatalf("issue body row %d does not keep the shared inset/width: %q", row, line)
		}
	}

	action := previewNeedleAction(t, m, "README.md")
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, action)))
	if m.preview.doc == nil || m.preview.issue == nil {
		t.Fatalf("opening doc did not preserve issue: doc=%#v issue=%#v", m.preview.doc, m.preview.issue)
	}
	root := m.preview.paneRoot
	if root == nil || root.Split == nil || root.Split.Axis != panelayout.Columns || root.Split.A.Kind != panelayout.Terminal ||
		root.Split.B.Split == nil || root.Split.B.Split.Axis != panelayout.Rows ||
		root.Split.B.Split.A.Kind != panelayout.Issue || root.Split.B.Split.B.Kind != panelayout.Document {
		t.Fatalf("global pane tree = %#v, want terminal beside stacked issue/document", root)
	}
}

func TestGlobalPaneStackResizesAndRestoresPerWorkspaceScroll(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	root := m.catalog["a"].Path
	longDoc := strings.Repeat("line\n", 80)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(longDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewIssue("td-196c42"))
	if m.preview.doc == nil || m.preview.issue == nil {
		t.Fatalf("stack did not open: doc=%#v issue=%#v", m.preview.doc, m.preview.issue)
	}
	m.preview.doc.view().SetSize(30, 4)
	m.preview.doc.view().Scroll(7)
	data := *m.preview.issue.view().Data()
	data.Description = strings.Repeat("issue body\n\n", 40)
	m.preview.issue.view().SetData(&data)
	m.preview.issue.view().SetSize(30, 4)
	m.preview.issue.view().Scroll(5)
	m.WorkspacesView(previewWide, previewTall)
	peer, hasBox := m.previewPeerBox()
	if !hasBox {
		t.Fatal("preview box missing")
	}
	layout, ok := m.layoutPreviewPanes(peer)
	if !ok {
		t.Fatal("stacked layout did not fit")
	}
	var divider panelayout.Divider
	for _, candidate := range layout.Dividers {
		if candidate.Axis == panelayout.Rows {
			divider = candidate
			break
		}
	}
	if divider.SplitID == 0 {
		t.Fatalf("no row divider in %+v", layout.Dividers)
	}
	before := panelayout.Find(m.preview.paneRoot, divider.SplitID).Split.Ratio
	x, y := divider.Box.X+divider.Box.W/2, divider.Box.Y
	m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.WorkspacesMouse(tea.MouseMotionMsg{X: x, Y: y + 3, Button: tea.MouseLeft})
	after := panelayout.Find(m.preview.paneRoot, divider.SplitID).Split.Ratio
	if after == before {
		t.Fatalf("row divider ratio stayed %d", before)
	}
	m.WorkspacesMouse(tea.MouseReleaseMsg{X: x, Y: y + 3, Button: tea.MouseLeft})
	m.WorkspacesView(previewWide, previewTall)
	docScroll, issueScroll := m.preview.doc.view().ScrollOffset(), m.preview.issue.view().ScrollOffset()

	result := m.results["sidecar"]
	other := result.Workspaces[0]
	other.ID, other.Name = "b", "beta"
	result.Workspaces = append(result.Workspaces, other)
	m.results["sidecar"] = result
	m.syncBoard()
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	if m.preview.doc != nil || m.preview.issue != nil {
		t.Fatalf("workspace b inherited a's panes: doc=%#v issue=%#v", m.preview.doc, m.preview.issue)
	}
	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	m.WorkspacesView(previewWide, previewTall)
	if m.preview.doc == nil || m.preview.issue == nil ||
		m.preview.doc.view().ScrollOffset() != docScroll || m.preview.issue.view().ScrollOffset() != issueScroll {
		t.Fatalf("restored panes/scroll = doc %#v offset %d, issue %#v offset %d; want %d/%d",
			m.preview.doc, scrollOfPreviewDoc(m.preview.doc), m.preview.issue, scrollOfPreviewIssue(m.preview.issue), docScroll, issueScroll)
	}
	restored := panelayout.Find(m.preview.paneRoot, divider.SplitID)
	if restored == nil || restored.Split == nil || restored.Split.Ratio != after {
		t.Fatalf("restored divider = %#v, want ratio %d", restored, after)
	}
}

func TestGlobalStackClicksKeepInputAndTreeFocusTogether(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewIssue("td-196c42"))
	m.WorkspacesView(previewWide, previewTall)

	x, y, ok := visualPreviewDocTabPoint(t, m, 0)
	if !ok {
		t.Fatal("document tab is not drawn")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
	docLeaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	if docLeaf == nil || m.preview.paneFocus != docLeaf.ID || !m.preview.doc.focused || m.preview.issue.focused {
		t.Fatalf("doc click focus = tree %d doc %v issue %v", m.preview.paneFocus, m.preview.doc.focused, m.preview.issue.focused)
	}

	m.WorkspacesView(previewWide, previewTall)
	var issueBody mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, _ := region.Data.(string); kind == previewIssueRegionKind {
			issueBody = region
			break
		}
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: issueBody.Rect.X + 1, Y: issueBody.Rect.Y + 2, Button: tea.MouseLeft}))
	issueLeaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Issue)
	if issueLeaf == nil || m.preview.paneFocus != issueLeaf.ID || m.preview.doc.focused || !m.preview.issue.focused {
		t.Fatalf("issue click focus = tree %d doc %v issue %v", m.preview.paneFocus, m.preview.doc.focused, m.preview.issue.focused)
	}
}

func scrollOfPreviewDoc(doc *previewDoc) int {
	if doc == nil || doc.view() == nil {
		return -1
	}
	return doc.view().ScrollOffset()
}

func scrollOfPreviewIssue(issue *previewIssue) int {
	if issue == nil || issue.view() == nil {
		return -1
	}
	return issue.view().ScrollOffset()
}

func TestGlobalIssuePreviewWheelKeyboardAndQClose(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	issue := m.preview.issue
	data := *issue.view().Data()
	data.Description = strings.Repeat("scrollable issue body\n\n", 30)
	issue.view().SetData(&data)
	view := m.WorkspacesView(previewWide, previewTall)
	if strings.Contains(ansi.Strip(view), "q close") {
		t.Fatalf("issue header still has q close: %q", ansi.Strip(view))
	}
	if !strings.Contains(ansi.Strip(view), ui.CloseButtonLabel) {
		t.Fatalf("issue header has no close button: %q", ansi.Strip(view))
	}
	if previewCloseRegion(m, panelayout.Issue) == nil {
		t.Fatal("issue pane has no close hit region")
	}

	var body mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, _ := region.Data.(string); kind == previewIssueRegionKind {
			body = region
			break
		}
	}
	if body.ID == "" {
		t.Fatalf("issue body region missing: %#v", m.workspacesMouse.HitMap.Regions())
	}
	before := issue.view().View()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: body.Rect.X + 2, Y: body.Rect.Y + 3, Button: tea.MouseWheelDown}))
	if issue.view().View() == before {
		t.Fatal("wheel over issue did not scroll the issue")
	}
	before = issue.view().View()
	handled, _ := m.WorkspacesKey(key("j"))
	if !handled || issue.view().View() == before {
		t.Fatalf("issue keyboard scroll handled=%v changed=%v", handled, issue.view().View() != before)
	}
	handled, _ = m.WorkspacesKey(key("q"))
	if !handled || m.preview.issue != nil {
		t.Fatalf("q handled=%v issue=%#v", handled, m.preview.issue)
	}

	// When the issue is the lower half of a file/issue stack, the widened
	// divider must not cover its tab ID cell.
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewIssue("td-196c42"))
	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := visualPreviewIssueIDPoint(t, m, "td-196c42")
	if !ok {
		t.Fatal("stacked issue tab ID is not drawn")
	}
	resolved := m.workspacesMouse.HitMap.Test(x, y)
	if hit, isTab := resolved.Data.(previewIssueTabHit); !isTab || int(hit) != 0 {
		t.Fatalf("lower issue tab at (%d,%d) resolves to %#v", x, y, resolved)
	}
}

func TestGlobalIssuePreviewDoesNotStealOverlayKeys(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	m.openViewFlyout()
	if !m.viewFlyoutOpen {
		t.Fatal("sort flyout did not open")
	}
	handled, _ := m.WorkspacesKey(key("esc"))
	if !handled || m.viewFlyoutOpen {
		t.Fatalf("overlay esc handled=%v open=%v", handled, m.viewFlyoutOpen)
	}
	if m.preview.issue == nil {
		t.Fatal("issue stole esc from the overlay and closed")
	}
}

func TestGlobalIssuePreviewRejectsStaleSelectionAndVisibilityResults(t *testing.T) {
	for _, change := range []string{"selection", "visibility"} {
		t.Run(change, func(t *testing.T) {
			stubPreviewTd(t)
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			result := m.results["sidecar"]
			other := result.Workspaces[0]
			other.ID, other.Name = "b", "beta"
			result.Workspaces = append(result.Workspaces, other)
			m.results["sidecar"] = result
			m.syncBoard()
			m.workspaces.SelectID("a")
			run(t, m, m.previewSync())
			stale := previewIssueResult(t, m.openPreviewIssue("td-196c42"))

			switch change {
			case "selection":
				m.workspaces.SelectID("b")
				run(t, m, m.previewSync())
				if m.preview.workspaceID != "b" {
					t.Fatalf("selection did not rebind preview: %q", m.preview.workspaceID)
				}
			case "visibility":
				run(t, m, m.SetWorkspacesVisible(false))
			}

			m.Update(stale)
			if m.preview.issue != nil {
				t.Fatalf("stale %s result restored %#v", change, m.preview.issue)
			}
		})
	}
}

func previewIssueResult(t *testing.T, cmd tea.Cmd) previewIssueLoadedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("issue open returned no command")
	}
	msg := cmd()
	if loaded, ok := msg.(previewIssueLoadedMsg); ok {
		return loaded
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("issue open message = %T, want batch", msg)
	}
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if loaded, ok := sub().(previewIssueLoadedMsg); ok {
			return loaded
		}
	}
	t.Fatal("issue batch had no loaded result")
	return previewIssueLoadedMsg{}
}

func mustPreviewSpan(t *testing.T, m *Model, action mouse.MouseAction) terminallink.Span {
	t.Helper()
	span, ok := m.previewLinkAt(action)
	if !ok {
		t.Fatalf("no link at %#v", action)
	}
	return span
}

func TestGlobalPreviewShiftClickDoesNotOpenDoc(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	action := previewNeedleAction(t, m, "README.md")
	if cmd, claimed := m.activatePreviewLinkAt(action, true); claimed || cmd != nil {
		t.Fatal("modified click claimed a link")
	}
	if m.preview.doc != nil {
		t.Fatal("shift-click opened a document")
	}
}

func TestGlobalPreviewLiveBufferFileClickOpensDoc(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, terminal := interactiveModel(t)
	ws := m.catalog["a"]
	ws.Path = root
	m.catalog["a"] = ws
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: "opened README.md\n"})
	run(t, m, m.enterPreviewInteractive())
	if !m.PreviewInteractive() {
		t.Fatal("fixture is not typing")
	}
	m.WorkspacesView(previewWide, previewTall)
	action := previewNeedleAction(t, m, "README.md")
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: action.X, Y: action.Y, Button: tea.MouseLeft}))
	if m.PreviewInteractive() {
		t.Fatal("file click left the preview typing")
	}
	if m.preview.doc == nil || m.preview.doc.view().Title() != "README.md" {
		t.Fatalf("live buffer click doc = %#v", m.preview.doc)
	}
}

func TestGlobalPreviewDocTabStripAndMToggle(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	if m.preview.doc == nil || !m.preview.doc.view().Rendered() {
		t.Fatalf("opened doc = %#v", m.preview.doc)
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "README.md") || strings.Contains(view, "Rendered") || strings.Contains(view, "q close") {
		t.Fatalf("doc header is not a path-only tab strip: %q", view)
	}

	handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !handled || m.preview.doc.view().Rendered() {
		t.Fatalf("m did not toggle to raw: handled=%v rendered=%v", handled, m.preview.doc.view().Rendered())
	}
	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !handled || !m.preview.doc.view().Rendered() {
		t.Fatalf("m did not restore rendered: handled=%v rendered=%v", handled, m.preview.doc.view().Rendered())
	}
	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled || m.preview.doc == nil || !m.preview.doc.view().Rendered() {
		t.Fatalf("r should be absorbed without toggling or closing: handled=%v doc=%#v", handled, m.preview.doc)
	}
}

func TestGlobalPreviewDocTabClickSelectsFile(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewDoc(terminallink.Span{
		Kind: terminallink.KindFile, Value: "main.go", Extra: terminallink.Extra{Raw: "main.go"},
	}))
	if m.preview.doc == nil || m.preview.doc.view().Title() != "main.go" {
		t.Fatalf("second open = %#v", m.preview.doc)
	}
	if len(m.preview.doc.tabs.Items) != 2 {
		t.Fatalf("tabs = %d, want 2", len(m.preview.doc.tabs.Items))
	}

	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := visualPreviewDocTabPoint(t, m, 0)
	if !ok {
		t.Fatal("README tab is not drawn")
	}
	resolved := m.workspacesMouse.HitMap.Test(x, y)
	if hit, isTab := resolved.Data.(previewDocTabHit); !isTab || int(hit) != 0 {
		t.Fatalf("visual README tab at (%d,%d) resolves to %#v", x, y, resolved)
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
	if m.preview.doc.view().Title() != "README.md" {
		t.Fatalf("clicking README selected %q", m.preview.doc.view().Title())
	}
	if !m.preview.doc.focused || !m.PreviewFocused() {
		t.Fatal("tab click handed the keyboard back to the list")
	}

	handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if !handled || m.preview.doc.view().Title() != "main.go" {
		t.Fatalf("} did not select main.go: handled=%v title=%q", handled, m.preview.doc.view().Title())
	}
	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || m.preview.doc == nil || m.preview.doc.view().Title() != "README.md" {
		t.Fatalf("x did not leave README: handled=%v title=%q", handled, titleOrEmpty(m.preview.doc))
	}
	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || m.preview.doc != nil || m.PreviewFocused() {
		t.Fatalf("last x did not close the pane: handled=%v doc=%#v focused=%v", handled, m.preview.doc, m.PreviewFocused())
	}
}

func TestGlobalPreviewIssueQReturnsToTheList(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	if m.preview.issue == nil || !m.PreviewFocused() {
		t.Fatal("issue did not take preview focus")
	}
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	run(t, m, cmd)
	if !handled || m.preview.issue != nil || m.PreviewFocused() {
		t.Fatalf("q handled=%v issue=%#v focused=%v", handled, m.preview.issue, m.PreviewFocused())
	}
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspaces {
		t.Fatalf("after q context = %q, want the list", got)
	}
}

func TestGlobalPreviewDocQClosesAndDropsStaleLoad(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	first := m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md")))
	if m.preview.doc == nil {
		t.Fatal("doc did not open")
	}
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled || m.preview.doc != nil || m.PreviewFocused() {
		t.Fatalf("q handled=%v doc=%#v focused=%v", handled, m.preview.doc, m.PreviewFocused())
	}
	run(t, m, cmd)
	run(t, m, first)
	if m.preview.doc != nil {
		t.Fatal("stale load after q restored the document")
	}
}

func TestGlobalPreviewReopenRejectsClosedModelResults(t *testing.T) {
	t.Run("document", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		span := mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))
		oldLoad := m.openPreviewDoc(span)
		oldEpoch := m.preview.doc.epoch
		m.closePreviewDoc()
		newLoad := m.openPreviewDoc(span)
		if m.preview.doc.epoch == oldEpoch {
			t.Fatalf("reopened document reused epoch %d", oldEpoch)
		}
		m.preview.doc.view().SetSize(40, 8)
		run(t, m, oldLoad)
		if !strings.Contains(ansi.Strip(m.preview.doc.view().View()), "Loading document") {
			t.Fatal("closed document result completed the reopened model")
		}
		run(t, m, newLoad)
		if strings.Contains(ansi.Strip(m.preview.doc.view().View()), "Loading document") {
			t.Fatal("reopened document's own result was rejected")
		}
	})

	t.Run("issue", func(t *testing.T) {
		stubPreviewTd(t)
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		oldLoad := m.openPreviewIssue("td-196c42")
		oldEpoch := m.preview.issue.epoch
		m.closePreviewIssue()
		newLoad := m.openPreviewIssue("td-196c42")
		if m.preview.issue.epoch == oldEpoch {
			t.Fatalf("reopened issue reused epoch %d", oldEpoch)
		}
		run(t, m, oldLoad)
		if !m.preview.issue.view().Loading() {
			t.Fatal("closed issue result completed the reopened model")
		}
		run(t, m, newLoad)
		if m.preview.issue.view().Loading() {
			t.Fatal("reopened issue's own result was rejected")
		}
	})
}

func TestGlobalPreviewLoadFinishesWhileItsWorkspaceIsCached(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	load := m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md")))
	if load == nil || m.preview.doc == nil {
		t.Fatal("document did not begin loading")
	}
	result := m.results["sidecar"]
	other := result.Workspaces[0]
	other.ID, other.Name = "b", "beta"
	result.Workspaces = append(result.Workspaces, other)
	m.results["sidecar"] = result
	m.syncBoard()
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	run(t, m, load)
	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if m.preview.doc == nil || m.preview.doc.view() == nil ||
		!strings.Contains(view, "Hello from preview") {
		t.Fatalf("cached document load did not finish: %#v\n%s", m.preview.doc, view)
	}
}

func visualPreviewDocTabPoint(t *testing.T, m *Model, index int) (x, y int, ok bool) {
	t.Helper()
	peer, hasBox := m.previewPeerBox()
	if !hasBox || m.preview.doc == nil {
		return 0, 0, false
	}
	docBox, split := m.previewPaneBox(panelayout.Document, peer)
	if !split {
		return 0, 0, false
	}
	for _, tab := range docview.LayoutTabStrip(m.preview.doc.tabs, docBox.W, m.preview.doc.focused).Tabs {
		if tab.Index != index {
			continue
		}
		return docBox.X + tab.Col + tab.Width/2, docBox.Y, true
	}
	return 0, 0, false
}

func titleOrEmpty(doc *previewDoc) string {
	if doc == nil || doc.view() == nil {
		return ""
	}
	return doc.view().Title()
}

func TestGlobalPreviewDiffLeafKeepsDoc(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	action := previewNeedleAction(t, m, "README.md")
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: action.X, Y: action.Y, Button: tea.MouseLeft}))
	if m.preview.doc == nil {
		t.Fatal("expected an open doc")
	}
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil {
		t.Fatal("expected a Diff leaf")
	}
	if m.preview.doc == nil {
		t.Fatal("opening Diff hid the document leaf")
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Hello from preview") {
		t.Fatalf("Diff leaf hid the doc body:\n%s", view)
	}
}

func TestGlobalPreviewDecoratesEveryActivatedLinkKind(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.previewSpecResolver = func(_, raw string) (string, bool) { return raw, raw == "abc1234" }
	decorated := m.decoratePreviewLine("see README.md then review td-196c42 and abc1234", 0)
	if !strings.Contains(decorated, "\x1b[4mREADME.md\x1b[24m") {
		t.Fatalf("the file this surface does open was not underlined: %q", decorated)
	}
	if !strings.Contains(decorated, "\x1b[4mtd-196c42\x1b[24m") {
		t.Fatalf("the issue this surface opens was not underlined: %q", decorated)
	}
	if !strings.Contains(decorated, "\x1b[4mabc1234\x1b[24m") {
		t.Fatalf("the git spec this surface opens was not underlined: %q", decorated)
	}
}

func TestPreviewClickThenCLISharesResolvedIdentity(t *testing.T) {
	root := initPreviewTwoCommitRepo(t)
	headShort := strings.TrimSpace(runPreviewGit(t, root, "rev-parse", "--short=7", "HEAD"))
	headFull := strings.TrimSpace(runPreviewGit(t, root, "rev-parse", "HEAD"))
	parentShort := strings.TrimSpace(runPreviewGit(t, root, "rev-parse", "--short=7", "HEAD~1"))
	parentFull := strings.TrimSpace(runPreviewGit(t, root, "rev-parse", "HEAD~1"))

	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"commit", headShort, "c:" + headFull},
		{"two-dot", parentShort + ".." + headShort, "r:" + parentFull + ".." + headFull},
		{"three-dot", parentShort + "..." + headShort, "r:" + parentFull + "..." + headFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			ws := m.catalog["a"]
			ws.Path = root
			ws.ProjectRoot = root
			m.catalog["a"] = ws

			run(t, m, m.activatePreviewDiff(terminallink.Span{
				Kind:  terminallink.KindDiff,
				Value: tc.token,
				Extra: terminallink.Extra{Raw: tc.token},
			}))
			if m.preview.diff == nil || m.preview.diff.view() == nil {
				t.Fatal("click opened no Diff view")
			}
			if got := m.preview.diff.view().Target.Identity(); got != tc.want {
				t.Fatalf("click identity = %q, want %q", got, tc.want)
			}

			cli := uirequest.DiffTarget(previewDiffPath(ws), tc.token)
			if cli.Identity() != tc.want {
				t.Fatalf("DiffTarget identity = %q, want %q", cli.Identity(), tc.want)
			}
			run(t, m, m.openPreviewDiff(cli))
			if keys := previewDiffKeys(m.preview.diff); !reflect.DeepEqual(keys, []string{tc.want}) {
				t.Fatalf("tabs after click+CLI = %v, want [%s]", keys, tc.want)
			}
		})
	}
}

func TestGlobalPreviewGitSpecClickOpensDiffLeaf(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.previewSpecResolver = func(_, raw string) (string, bool) {
		return raw, raw == "abc1234" || raw == "abc1234..def5678"
	}
	if buf := m.previewBuffer(); buf == nil {
		t.Fatal("no preview buffer")
	} else {
		buf.Update("landed abc1234 then abc1234..def5678\n")
	}
	m.WorkspacesView(previewWide, previewTall)

	cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "abc1234.."), false)
	if !claimed || cmd == nil {
		t.Fatal("range was not activated")
	}
	run(t, m, cmd)
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("range click opened no Diff leaf")
	}
	if got := m.preview.diff.view().Target.Identity(); got != "r:abc1234..def5678" {
		t.Fatalf("range identity = %q", got)
	}

	run(t, m, m.activatePreviewDiff(terminallink.Span{
		Kind:  terminallink.KindDiff,
		Value: "abc1234",
		Extra: terminallink.Extra{Raw: "abc1234"},
	}))
	if idx := m.preview.diff.tabs.Find("c:abc1234"); idx < 0 {
		t.Fatalf("commit tab missing after click: keys=%v", previewDiffKeys(m.preview.diff))
	}

	m.preview.paneRoot = nil
	if spans := m.previewLinkSpans("landed abc1234"); diffSpanCount(spans) != 0 {
		t.Fatalf("nil pane tree still emitted git spans: %#v", spans)
	}
}

func TestGlobalPreviewGitSpecCapAndRejects(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	calls := 0
	m.previewSpecResolver = func(_, raw string) (string, bool) {
		calls++
		return raw, true
	}
	var tokens []string
	for i := 0; i < 20; i++ {
		tokens = append(tokens, fmt.Sprintf("aaaaaa%02x", i))
	}
	line := strings.Join(tokens, " ")
	spans := m.previewLinkSpans(line)
	if calls != terminallink.MaxNewDiffResolves {
		t.Fatalf("resolver calls = %d, want cap %d", calls, terminallink.MaxNewDiffResolves)
	}
	if diffSpanCount(spans) != terminallink.MaxNewDiffResolves {
		t.Fatalf("spans = %d, want %d", diffSpanCount(spans), terminallink.MaxNewDiffResolves)
	}
	_ = m.previewLinkSpans(line)
	if calls != terminallink.MaxNewDiffResolves {
		t.Fatalf("memo reused: calls = %d", calls)
	}

	m.previewSpecResolver = func(_, raw string) (string, bool) { return raw, true }
	m.preview.linkMemo = previewLinkMemo{}
	if n := diffSpanCount(m.previewLinkSpans("Abc1234")); n != 0 {
		t.Fatalf("mixed-case produced %d spans", n)
	}
	if n := diffSpanCount(m.previewLinkSpans("abc1234.go")); n != 0 {
		t.Fatalf("filename produced %d spans", n)
	}
	if n := diffSpanCount(m.previewLinkSpans("abc1234..def5678")); n != 1 {
		t.Fatalf("range produced %d spans, want 1", n)
	}
}

func initPreviewTwoCommitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runPreviewGit(t, root, "init", "-b", "main")
	runPreviewGit(t, root, "config", "user.email", "sidecar@example.test")
	runPreviewGit(t, root, "config", "user.name", "Sidecar Test")
	runPreviewGit(t, root, "commit", "--allow-empty", "-m", "one")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runPreviewGit(t, root, "add", "a.go")
	runPreviewGit(t, root, "commit", "-m", "two")
	return root
}

func runPreviewGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func previewDiffKeys(diff *previewDiff) []string {
	if diff == nil {
		return nil
	}
	keys := make([]string, 0, len(diff.tabs.Items))
	for _, item := range diff.tabs.Items {
		keys = append(keys, item.Key)
	}
	return keys
}

func diffSpanCount(spans []terminallink.Span) int {
	n := 0
	for _, span := range spans {
		if span.Kind == terminallink.KindDiff {
			n++
		}
	}
	return n
}
