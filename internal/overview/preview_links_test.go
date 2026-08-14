package overview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
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
	if m.preview.issue == nil || m.preview.issue.view.Data() == nil || m.preview.issue.view.Data().ID != "td-196c42" {
		t.Fatalf("issue preview = %#v", m.preview.issue)
	}

	spans := terminallink.Scan("review td-196c42", nil)
	if len(spans) != 1 || spans[0].Kind != terminallink.KindIssue {
		t.Fatalf("scanner issue span = %#v", spans)
	}
}

func TestGlobalIssuePreviewRawChildClickUsesRenderedCoordinates(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			stubPreviewTd(t)
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			m.WorkspacesView(width, previewTall)
			run(t, m, m.openPreviewIssue("td-196c42"))
			m.WorkspacesView(width, previewTall)
			issue := m.preview.issue
			if issue == nil || issue.view.Data() == nil {
				t.Fatalf("issue did not load: %#v", issue)
			}

			var child issueview.Hit
			found := false
			for _, hit := range issue.view.Hits() {
				if hit.Kind == issueview.HitChild && hit.ID == "td-acde12" {
					child, found = hit, true
					break
				}
			}
			if !found {
				t.Fatalf("rendered issue has no child hit: %+v", issue.view.Hits())
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
			if issue.view.Data() == nil || issue.view.Data().ID != "td-acde12" {
				t.Fatalf("raw child click loaded %#v, want td-acde12", issue.view.Data())
			}

			m.WorkspacesView(width, previewTall)
			parentAtSameCell := false
			for _, hit := range issue.view.Hits() {
				if hit.Kind == issueview.HitParent && hit.Y == child.Y && x == region.Rect.X+hit.X {
					parentAtSameCell = true
					break
				}
			}
			if !parentAtSameCell {
				t.Fatalf("loaded child did not render its parent at the original raw cell: %+v", issue.view.Hits())
			}
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
			if issue.view.Data() == nil || issue.view.Data().ID != "td-acde12" || issue.view.IssueID() != "td-acde12" {
				t.Fatalf("double-click replay navigated to %#v / %q", issue.view.Data(), issue.view.IssueID())
			}
		})
	}
}

func TestGlobalIssuePreviewHasSharedPaddingAndOneSecondarySlot(t *testing.T) {
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
	if m.preview.doc == nil || m.preview.issue != nil {
		t.Fatalf("opening doc did not replace issue: doc=%#v issue=%#v", m.preview.doc, m.preview.issue)
	}
	run(t, m, m.openPreviewIssue("td-196c42"))
	if m.preview.issue == nil || m.preview.doc != nil {
		t.Fatalf("opening issue did not replace doc: doc=%#v issue=%#v", m.preview.doc, m.preview.issue)
	}
}

func TestGlobalIssuePreviewWheelKeyboardAndCloseChip(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	issue := m.preview.issue
	data := *issue.view.Data()
	data.Description = strings.Repeat("scrollable issue body\n\n", 30)
	issue.view.SetData(&data)
	m.WorkspacesView(previewWide, previewTall)

	var body, close mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		kind, _ := region.Data.(string)
		switch kind {
		case previewIssueRegionKind:
			body = region
		case previewIssueCloseKind:
			close = region
		}
	}
	if body.ID == "" || close.ID == "" {
		t.Fatalf("issue regions missing: body=%#v close=%#v", body, close)
	}
	before := issue.view.View()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: body.Rect.X + 2, Y: body.Rect.Y + 3, Button: tea.MouseWheelDown}))
	if issue.view.View() == before {
		t.Fatal("wheel over issue did not scroll the issue")
	}
	before = issue.view.View()
	handled, _ := m.WorkspacesKey(key("j"))
	if !handled || issue.view.View() == before {
		t.Fatalf("issue keyboard scroll handled=%v changed=%v", handled, issue.view.View() != before)
	}
	handled, _ = m.WorkspacesKey(key("q"))
	if !handled || m.preview.issue != nil {
		t.Fatalf("q handled=%v issue=%#v", handled, m.preview.issue)
	}

	run(t, m, m.openPreviewIssue("td-196c42"))
	m.WorkspacesView(previewWide, previewTall)
	close = mouse.Region{}
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, _ := region.Data.(string); kind == previewIssueCloseKind {
			close = region
			break
		}
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: close.Rect.X, Y: close.Rect.Y, Button: tea.MouseLeft}))
	if m.preview.issue != nil {
		t.Fatal("close chip left issue open")
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

func TestGlobalPreviewDocQClosesAndDropsStaleLoad(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	first := m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md")))
	if m.preview.doc == nil {
		t.Fatal("doc did not open")
	}
	generation := m.preview.generation
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled || m.preview.doc != nil || m.PreviewFocused() || m.preview.generation == generation {
		t.Fatalf("q handled=%v doc=%#v focused=%v generation %d→%d", handled, m.preview.doc, m.PreviewFocused(), generation, m.preview.generation)
	}
	run(t, m, cmd)
	run(t, m, first)
	if m.preview.doc != nil {
		t.Fatal("stale load after q restored the document")
	}
}

func visualPreviewDocTabPoint(t *testing.T, m *Model, index int) (x, y int, ok bool) {
	t.Helper()
	box, hasBox := m.previewBox()
	if !hasBox || m.preview.doc == nil {
		return 0, 0, false
	}
	_, docBox, split := m.previewSecondaryLayout(box)
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

func TestGlobalPreviewDiffTabDoesNotShowDoc(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	action := previewNeedleAction(t, m, "README.md")
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: action.X, Y: action.Y, Button: tea.MouseLeft}))
	if m.preview.doc == nil {
		t.Fatal("expected an open doc")
	}
	m.previewTab = workspacediff.TabDiff
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if strings.Contains(view, "Hello from preview") {
		t.Fatalf("diff tab rendered the doc body: %q", view)
	}
}

func TestGlobalPreviewDecoratesEveryActivatedLinkKind(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	decorated := m.decoratePreviewLine("see README.md then review td-196c42", 0)
	if !strings.Contains(decorated, "\x1b[4mREADME.md\x1b[24m") {
		t.Fatalf("the file this surface does open was not underlined: %q", decorated)
	}
	if !strings.Contains(decorated, "\x1b[4mtd-196c42\x1b[24m") {
		t.Fatalf("the issue this surface opens was not underlined: %q", decorated)
	}
}
