package overview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Slice 4 items 3-4 of docs/plans/active/global-overview-workspaces.md: each
// global tab keeps its own in-memory view state for the process lifetime, and
// no global code path touches any project's pane layout.

// TestEachGlobalTabKeepsItsViewStateAcrossSpaceToggles drives the exact
// lifecycle the app performs when the user leaves the global space and comes
// back: Stop on the way out, Ensure on the way in.
const shortTall = 8

func TestEachGlobalTabKeepsItsViewStateAcrossSpaceToggles(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	// Give the Workspaces tab a filter, a sort, a selection, and a scroll
	// position, and the Agents tab a card that is not the first one.
	press(t, m, "s")
	press(t, m, "s")
	press(t, m, "/")
	for _, r := range "sidecar" {
		m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.WorkspacesKey(key("enter"))
	m.workspaces.SelectID("d")
	// A short viewport is what makes the scroll position a real fact to keep.
	m.WorkspacesView(previewWide, shortTall)
	m.workspaces.Scroll(1)
	m.Update(key("j")) // Agents board selection
	board := m.board.Selection()

	sort, query := m.workspaces.Sort(), m.workspaces.Filter().Query()
	selected, scroll := m.workspaces.SelectedID(), m.workspaces.ScrollOffset()
	if query == "" || sort == workspacelist.SortName || selected != "d" || scroll == 0 {
		t.Fatalf("fixture did not establish view state: sort=%v query=%q selected=%q scroll=%d", sort, query, selected, scroll)
	}

	// Leaving the global space stops the cycle and releases the preview; coming
	// back starts a new one. Neither may reset what the user set up.
	for range 2 {
		m.Stop()
		if m.PreviewMetrics().Captures == 0 {
			t.Fatal("the fixture never captured anything")
		}
		m.Ensure(m.projects) // the command is the app's to run; the state is what matters here
		m.SetWorkspacesVisible(true)
		m.WorkspacesView(previewWide, shortTall)

		if m.workspaces.Sort() != sort || m.workspaces.Filter().Query() != query {
			t.Fatalf("re-entry reset sort/filter: sort=%v query=%q", m.workspaces.Sort(), m.workspaces.Filter().Query())
		}
		if m.workspaces.SelectedID() != selected || m.workspaces.ScrollOffset() != scroll {
			t.Fatalf("re-entry reset selection/scroll: selected=%q scroll=%d", m.workspaces.SelectedID(), m.workspaces.ScrollOffset())
		}
		if m.board.Selection() != board {
			t.Fatalf("re-entry reset the Agents board selection: %#v", m.board.Selection())
		}
	}
}

// The global browser hands a project an identity and nothing else. Pane layouts
// — reading them, rewriting them, pruning them — belong to the project's own
// Workspaces plugin, and this keeps that boundary honest against the next
// convenient shortcut.
func TestNoGlobalPackageTouchesAProjectsPaneLayout(t *testing.T) {
	packages := []string{"../app", "../overview", "../workspacelist", "../workspaceinventory", "../termpreview"}
	needles := []string{"PaneLayout", "paneLayout", "PaneSplitJSON", "PaneDocTabJSON"}
	for _, pkg := range packages {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(pkg, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range needles {
				if strings.Contains(string(data), needle) {
					t.Fatalf("%s/%s reads or writes a project's pane layout (%q)", pkg, name, needle)
				}
			}
		}
	}
}
