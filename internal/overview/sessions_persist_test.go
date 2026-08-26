package overview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestSessionsSelectedDebounceSavesLastIDOnce(t *testing.T) {
	var saved []string
	saveSessionsSelected = func(id string) error {
		saved = append(saved, id)
		return nil
	}
	t.Cleanup(func() {
		saveSessionsSelected = func(string) error { return nil }
		sessionsSelectedDebounce = 0
	})
	sessionsSelectedDebounce = 300 * time.Millisecond // ticks are fired by hand

	m, _ := previewModel(t)
	m.preview.visible = true
	m.pendingRestoreSelected = ""
	for _, id := range []string{"a", "b", "c"} {
		m.workspaces.SelectID(id)
		m.bindPreview(false)
	}
	if m.sessionsSelectedPending != "c" {
		t.Fatalf("pending = %q, want c", m.sessionsSelectedPending)
	}
	m.Update(sessionsSelectedTickMsg{generation: m.sessionsSelectedGen - 1, id: "b"})
	if len(saved) != 0 {
		t.Fatalf("stale tick saved %v", saved)
	}
	m.Update(sessionsSelectedTickMsg{generation: m.sessionsSelectedGen, id: "c"})
	if len(saved) != 1 || saved[0] != "c" {
		t.Fatalf("saved = %v, want [c]", saved)
	}
}

func TestSessionsRestoreCatalogSelectsRowAndWarmsComposedTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# catalog restore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := termpanes.SessionPrefix + "bravo"
	loadSessionsSelected = func() string { return "b" }
	loadSessionsPaneLayout = func(id string) *state.PaneLayoutJSON {
		if id != "b" {
			return nil
		}
		return &state.PaneLayoutJSON{
			Root: root, Surface: "b", Open: true, FocusKind: "doc",
			Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: "terminal"},
				B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
					Axis: "rows", Ratio: 40,
					A: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "raw"}}},
					B: &state.PaneLayoutJSON{Kind: "shell", Session: session, Name: "build logs"},
				}},
			},
		}
	}
	var ensured []string
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(gotSession, workDir string) (string, error) {
		ensured = append(ensured, gotSession+"|"+workDir)
		if gotSession != session || workDir != root {
			t.Fatalf("EnsureSession(%q, %q), want (%q, %q)", gotSession, workDir, session, root)
		}
		return "%restored", nil
	}
	t.Cleanup(func() {
		loadSessionsSelected = func() string { return "" }
		loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil }
		ensurePreviewTerminalSession = original
	})

	m, _ := previewModel(t)
	result := m.results["sidecar"]
	workspaces := append([]workspaceinventory.Workspace(nil), result.Workspaces...)
	for i := range workspaces {
		if workspaces[i].ID != "b" {
			continue
		}
		workspaces[i].Path = root
		workspaces[i].ProjectRoot = root
	}
	result.Workspaces = workspaces
	m.results["sidecar"] = result

	// Production catalog path: projectMsg → syncBoard → SelectID(persisted) → previewSync.
	m.pendingRestoreSelected = "b"
	m.preview.visible = true
	m.preview.workspaceID = ""
	m.preview.paneCache = nil
	m.resetActivePreviewPanes()
	m.syncBoard()
	run(t, m, m.previewSync())

	if m.workspaces.SelectedID() != "b" {
		t.Fatalf("selected = %q, want persisted row b", m.workspaces.SelectedID())
	}
	visible := m.workspaces.Visible()
	if len(visible) == 0 || visible[0].ID == "b" {
		first := ""
		if len(visible) > 0 {
			first = visible[0].ID
		}
		t.Fatalf("b must not be the first visible row, first=%q n=%d", first, len(visible))
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil {
		t.Fatalf("restored tree missing document: %+v", m.preview.paneRoot)
	}
	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		t.Fatalf("restored tree missing shell: %+v", m.preview.paneRoot)
	}
	leaf := m.preview.terminalPanes.Leaf(shell.ID)
	if leaf == nil || leaf.Session != session {
		t.Fatalf("restored shell leaf = %+v", leaf)
	}
	if len(ensured) != 1 || ensured[0] != session+"|"+root {
		t.Fatalf("EnsureSession calls = %v, want [%s|%s]", ensured, session, root)
	}
}

func TestSessionsRestoreSelectsPersistedRowWhenCatalogArrives(t *testing.T) {
	loadSessionsSelected = func() string { return "b" }
	t.Cleanup(func() { loadSessionsSelected = func() string { return "" } })

	m, _ := previewModel(t)
	if m.pendingRestoreSelected != "b" && m.workspaces.SelectedID() != "b" {
		t.Fatalf("pending=%q selected=%q, want b restored", m.pendingRestoreSelected, m.workspaces.SelectedID())
	}
	m.pendingRestoreSelected = "b"
	m.loading = false
	m.syncWorkspaces()
	if m.workspaces.SelectedID() != "b" {
		t.Fatalf("selected = %q, want persisted row b", m.workspaces.SelectedID())
	}
}

func TestSessionsRestoreAbsentRowStartsNormally(t *testing.T) {
	loadSessionsSelected = func() string { return "gone-row" }
	t.Cleanup(func() { loadSessionsSelected = func() string { return "" } })

	m, _ := previewModel(t)
	m.pendingRestoreSelected = "gone-row"
	m.loading = false
	m.syncWorkspaces()
	if m.pendingRestoreSelected != "" {
		t.Fatal("absent persisted row should not fail restore")
	}
	if id := m.workspaces.SelectedID(); id == "gone-row" {
		t.Fatal("list selected a row that is not in the catalog")
	}
	if m.workspaces.SelectedID() == "" {
		t.Fatal("list should start on an available row")
	}
}

func TestNewDoesNotDecodeSessionsLayouts(t *testing.T) {
	loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON {
		t.Fatal("layouts must not decode before a row is first shown")
		return nil
	}
	t.Cleanup(func() { loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil } })
	previewModel(t)
}

func TestBarePrimaryWritesNothing(t *testing.T) {
	var wrote bool
	saveSessionsPaneLayout = func(_ string, layout *state.PaneLayoutJSON) error {
		if layout != nil {
			wrote = true
		}
		return nil
	}
	t.Cleanup(func() { saveSessionsPaneLayout = func(string, *state.PaneLayoutJSON) error { return nil } })

	m, _ := previewModel(t)
	m.preview.visible = true
	m.bindPreview(false)
	m.persistSessionsLayout()
	if wrote {
		t.Fatal("bare primary preview wrote a sessionsPaneLayouts entry")
	}
}

func TestComposedTreePersistsAndBareDeleteRemovesEntry(t *testing.T) {
	layouts := map[string]*state.PaneLayoutJSON{}
	saveSessionsPaneLayout = func(id string, layout *state.PaneLayoutJSON) error {
		if layout == nil {
			delete(layouts, id)
			return nil
		}
		layouts[id] = layout
		return nil
	}
	t.Cleanup(func() { saveSessionsPaneLayout = func(string, *state.PaneLayoutJSON) error { return nil } })

	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	if layouts["a"] == nil || layouts["a"].Split == nil {
		t.Fatalf("opening a doc did not persist a composed tree: %#v", layouts["a"])
	}
	if layouts["a"].FocusKind == "" {
		t.Fatal("persisted layout omitted focusKind")
	}

	run(t, m, m.closePreviewDoc())
	if layouts["a"] != nil {
		t.Fatalf("closing the last extra pane left an entry: %#v", layouts["a"])
	}
}

func TestSessionsRestoreDecodesTreeAndReattachesShellSession(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	root := m.catalog["a"].Path
	session := termpanes.SessionPrefix + "restored"
	loadSessionsPaneLayout = func(id string) *state.PaneLayoutJSON {
		if id != "a" {
			return nil
		}
		return &state.PaneLayoutJSON{
			Root: root, Surface: "a", Open: true, FocusKind: "doc",
			Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: "terminal"},
				B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
					Axis: "rows", Ratio: 40,
					A: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "raw"}}},
					B: &state.PaneLayoutJSON{Kind: "shell", Session: session, Name: "build logs"},
				}},
			},
		}
	}
	var ensured []string
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(gotSession, workDir string) (string, error) {
		ensured = append(ensured, gotSession+"|"+workDir)
		if gotSession != session || workDir != root {
			t.Fatalf("EnsureSession(%q, %q)", gotSession, workDir)
		}
		return "%restored", nil
	}
	t.Cleanup(func() {
		loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil }
		ensurePreviewTerminalSession = original
	})

	m.resetActivePreviewPanes()
	m.preview.workspaceID = ""
	m.preview.paneCache = nil
	run(t, m, m.bindPreview(false))
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil {
		t.Fatalf("restored tree missing document: %+v", m.preview.paneRoot)
	}
	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		t.Fatalf("restored tree missing shell: %+v", m.preview.paneRoot)
	}
	leaf := m.preview.terminalPanes.Leaf(shell.ID)
	if leaf == nil || leaf.Session != session || leaf.Name != "build logs" {
		t.Fatalf("restored shell leaf = %+v", leaf)
	}
	if len(ensured) != 1 {
		t.Fatalf("EnsureSession calls = %v", ensured)
	}
	if kind := previewFocusKind(m.preview.paneRoot, m.preview.paneFocus); kind != "document" {
		t.Fatalf("restored focus = %q, want document", kind)
	}
}

func assertPreviewCollapsedToPrimary(t *testing.T, m *Model) {
	t.Helper()
	root := m.preview.paneRoot
	if root == nil {
		t.Fatal("pane tree is nil")
	}
	if root.Split != nil {
		t.Fatalf("split did not collapse: %+v", root)
	}
	if root.Kind != panelayout.Primary {
		t.Fatalf("collapsed kind = %v, want primary", root.Kind)
	}
}

func TestSessionsRestoreDegradationTable(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	root := m.catalog["a"].Path
	originalEnsure := ensurePreviewTerminalSession
	t.Cleanup(func() { ensurePreviewTerminalSession = originalEnsure })

	tests := []struct {
		name      string
		layout    *state.PaneLayoutJSON
		ensureErr error
		check     func(*testing.T, *Model)
	}{
		{
			name: "missing document drops the leaf",
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true,
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "gone.md", Mode: "raw"}}},
				},
			},
			check: func(t *testing.T, m *Model) {
				if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) != nil {
					t.Fatalf("missing document stayed: %+v", m.preview.paneRoot)
				}
				assertPreviewCollapsedToPrimary(t, m)
			},
		},
		{
			name: "missing issue drops the leaf",
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true,
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "issue", IssueTabs: []state.PaneIssueTabJSON{{Issue: "gone"}}},
				},
			},
			check: func(t *testing.T, m *Model) {
				if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Issue) != nil {
					t.Fatalf("missing issue stayed: %+v", m.preview.paneRoot)
				}
				assertPreviewCollapsedToPrimary(t, m)
			},
		},
		{
			name: "unknown kind drops the leaf",
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true,
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "hologram"},
				},
			},
			check: func(t *testing.T, m *Model) {
				if m.preview.paneRoot == nil || m.preview.paneRoot.Split != nil {
					t.Fatalf("unknown kind did not collapse: %+v", m.preview.paneRoot)
				}
				assertPreviewCollapsedToPrimary(t, m)
			},
		},
		{
			name: "dead sidecar-tp session is recreated",
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true,
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "shell", Session: termpanes.SessionPrefix + "dead"},
				},
			},
			check: func(t *testing.T, m *Model) {
				shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
				if shell == nil {
					t.Fatal("dead session dropped the shell leaf")
				}
				leaf := m.preview.terminalPanes.Leaf(shell.ID)
				if leaf == nil || leaf.Session != termpanes.SessionPrefix+"dead" {
					t.Fatalf("reattach session = %+v", leaf)
				}
			},
		},
		{
			name:      "EnsureSession error closes the shell and collapses",
			ensureErr: errors.New("tmux gone"),
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true,
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "shell", Session: termpanes.SessionPrefix + "dead"},
				},
			},
			check: func(t *testing.T, m *Model) {
				if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell) != nil {
					t.Fatalf("failed EnsureSession left a shell leaf: %+v", m.preview.paneRoot)
				}
				assertPreviewCollapsedToPrimary(t, m)
			},
		},
		{
			name: "gone focus falls back to primary",
			layout: &state.PaneLayoutJSON{
				Root: root, Surface: "a", Open: true, FocusKind: "doc",
				Split: &state.PaneSplitJSON{
					Axis: "cols", Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: "terminal"},
					B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "gone.md", Mode: "raw"}}},
				},
			},
			check: func(t *testing.T, m *Model) {
				if kind := previewFocusKind(m.preview.paneRoot, m.preview.paneFocus); kind != "primary" {
					t.Fatalf("focus = %q, want primary fallback", kind)
				}
				assertPreviewCollapsedToPrimary(t, m)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loadSessionsPaneLayout = func(id string) *state.PaneLayoutJSON {
				if id != "a" {
					return nil
				}
				return test.layout
			}
			ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
				if test.ensureErr != nil {
					return "", test.ensureErr
				}
				return "%recreated", nil
			}
			t.Cleanup(func() { loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil } })
			m.resetActivePreviewPanes()
			m.preview.workspaceID = ""
			m.preview.paneCache = nil
			run(t, m, m.bindPreview(false))
			test.check(t, m)
		})
	}
}

func TestForgetSessionsRowOnDelete(t *testing.T) {
	var selected string
	layouts := map[string]*state.PaneLayoutJSON{
		"a": {Kind: "terminal", Open: true},
	}
	loadSessionsSelected = func() string { return selected }
	saveSessionsSelected = func(id string) error { selected = id; return nil }
	loadSessionsPaneLayout = func(id string) *state.PaneLayoutJSON { return layouts[id] }
	saveSessionsPaneLayout = func(id string, layout *state.PaneLayoutJSON) error {
		if layout == nil {
			delete(layouts, id)
			return nil
		}
		layouts[id] = layout
		return nil
	}
	t.Cleanup(func() {
		loadSessionsSelected = func() string { return "" }
		saveSessionsSelected = func(string) error { return nil }
		loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil }
		saveSessionsPaneLayout = func(string, *state.PaneLayoutJSON) error { return nil }
	})

	m, _ := previewModel(t)
	selected = "a"
	m.preview.paneCache = map[string]previewPaneCache{"a": {root: &panelayout.Node{ID: 1, Kind: panelayout.Terminal}}}
	m.forgetSessionsRow("a")
	if _, ok := layouts["a"]; ok {
		t.Fatal("delete did not prune sessionsPaneLayouts")
	}
	if selected != "" {
		t.Fatalf("delete left sessionsSelected = %q", selected)
	}
	if _, ok := m.preview.paneCache["a"]; ok {
		t.Fatal("delete left the in-memory pane cache")
	}
}

func TestSessionsRestoreDoesNotTouchProjectWorkspaceState(t *testing.T) {
	saveSessionsPaneLayout = state.SetSessionsPaneLayout
	t.Cleanup(func() { saveSessionsPaneLayout = func(string, *state.PaneLayoutJSON) error { return nil } })

	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	root := m.catalog["a"].Path
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	m.persistSessionsLayout()
	wt := state.GetWorkspaceState(root)
	if len(wt.PaneLayouts) != 0 {
		t.Fatalf("global persist wrote WorkspaceState.PaneLayouts: %#v", wt.PaneLayouts)
	}
	if state.GetSessionsPaneLayout("a") == nil {
		t.Fatal("global persist did not write sessionsPaneLayouts")
	}
}

func TestSessionsLayoutJSONNamesThePersistedSession(t *testing.T) {
	m, _ := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	layout := m.sessionsPaneLayoutJSON()
	if layout == nil {
		t.Fatal("composed terminal split encoded to nil")
	}
	raw := mustLayoutSession(layout)
	if !strings.HasPrefix(raw, termpanes.SessionPrefix) {
		t.Fatalf("persisted session = %q, want sidecar-tp-*", raw)
	}
}

func mustLayoutSession(layout *state.PaneLayoutJSON) string {
	if layout == nil {
		return ""
	}
	if layout.Session != "" {
		return layout.Session
	}
	if layout.Split == nil {
		return ""
	}
	if s := mustLayoutSession(layout.Split.A); s != "" {
		return s
	}
	return mustLayoutSession(layout.Split.B)
}
