package overview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

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
			if m.preview.doc == nil || m.preview.doc.view.Title() != "README.md" {
				t.Fatalf("doc = %#v", m.preview.doc)
			}
			if !strings.Contains(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "Hello from preview") {
				t.Fatal("doc pane did not render the file")
			}
		})
	}
}

func TestGlobalPreviewURLAndIssueAreNotFileActivation(t *testing.T) {
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
	if cmd, claimed := m.activatePreviewLinkAt(issueAction, false); claimed || cmd != nil {
		t.Fatal("issue id was activated")
	}
	if m.preview.doc != nil {
		t.Fatal("issue id opened a doc pane")
	}

	spans := terminallink.Scan("review td-196c42", nil)
	if len(spans) != 1 || spans[0].Kind != terminallink.KindIssue {
		t.Fatalf("scanner issue span = %#v", spans)
	}
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
	if m.preview.doc == nil || m.preview.doc.view.Title() != "README.md" {
		t.Fatalf("live buffer click doc = %#v", m.preview.doc)
	}
}

func TestGlobalPreviewDocModeChipAndMToggle(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	if m.preview.doc == nil || !m.preview.doc.view.Rendered() {
		t.Fatalf("opened doc = %#v", m.preview.doc)
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Rendered") || strings.Contains(view, "r raw") || strings.Contains(view, "r render") {
		t.Fatalf("doc header/hint = %q", view)
	}

	var mode mouse.Region
	found := false
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, ok := region.Data.(string); ok && kind == previewDocModeKind {
			mode = region
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rendered mode chip has no hit region")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X: mode.Rect.X, Y: mode.Rect.Y, Button: tea.MouseLeft,
	}))
	if m.preview.doc.view.Rendered() {
		t.Fatal("mode chip click did not toggle to raw")
	}

	handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !handled || !m.preview.doc.view.Rendered() {
		t.Fatalf("m did not restore rendered: handled=%v rendered=%v", handled, m.preview.doc.view.Rendered())
	}
	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled || m.preview.doc == nil || !m.preview.doc.view.Rendered() {
		t.Fatalf("r should be absorbed without toggling or closing: handled=%v doc=%#v", handled, m.preview.doc)
	}
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

// Decorate underlines every kind it is handed; the host chooses what to hand
// it. This surface opens no td pane, so an underlined td id here would be a
// dead link — and nothing but decoratedPreviewSpans stands between the two.
func TestGlobalPreviewLeavesTdIdsUndecorated(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	decorated := m.decoratePreviewLine("see README.md then review td-196c42", 0)
	if !strings.Contains(decorated, "\x1b[4mREADME.md\x1b[24m") {
		t.Fatalf("the file this surface does open was not underlined: %q", decorated)
	}
	if !strings.Contains(decorated, "review td-196c42") {
		t.Fatalf("the td id this surface opens nowhere was decorated: %q", decorated)
	}
}
