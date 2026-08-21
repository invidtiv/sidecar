package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/panebadge"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The project sidebar and the global Sessions list are two projections of one
// model, so a workspace showing two panes has to be badged the same way in
// both. This is the test that says so: one persisted tree, both surfaces' own
// row helpers, one glyph.

func twoPaneLayout(surface string) *state.PaneLayoutJSON {
	return &state.PaneLayoutJSON{
		Surface: surface,
		Open:    true,
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
			B: &state.PaneLayoutJSON{Kind: contentKindShell},
		},
	}
}

func TestBothSurfacesBadgeTheSameTreeTheSameWay(t *testing.T) {
	root := t.TempDir()
	surface := panebadge.ShellSurface("test-shell")
	saved := state.WorkspaceState{
		PaneLayouts: map[string]*state.PaneLayoutJSON{surface: twoPaneLayout(surface)},
	}

	p := docPaneTestPlugin(t, root, true)
	// The badge for a row that is not the live selection comes off what was
	// saved, which is the case both surfaces share.
	p.paneLayoutSurface = ""
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}
	project := p.shellRowBadge(p.shells[0])

	ws := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindShell, TmuxName: "test-shell", ProjectRoot: root,
	}
	global := overview.RowLayoutBadge(saved, overview.WorkspaceSurface(ws))

	if project != panebadge.SplitGlyph {
		t.Fatalf("project sidebar badged a two-pane workspace %q, want %q", project, panebadge.SplitGlyph)
	}
	if project != global {
		t.Fatalf("project sidebar badge %q, global list badge %q", project, global)
	}
}

// A workspace with one pane wears no badge on either surface: every workspace
// has a pane, so a glyph on all of them says nothing.
func TestNeitherSurfaceBadgesAnUnsplitWorkspace(t *testing.T) {
	root := t.TempDir()
	surface := panebadge.ShellSurface("test-shell")
	saved := state.WorkspaceState{
		PaneLayouts: map[string]*state.PaneLayoutJSON{
			surface: {Surface: surface, Open: true, Kind: contentKindTerminal},
		},
	}

	p := docPaneTestPlugin(t, root, true)
	p.paneLayoutSurface = ""
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}

	project := p.shellRowBadge(p.shells[0])
	ws := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindShell, TmuxName: "test-shell", ProjectRoot: root,
	}
	global := overview.RowLayoutBadge(saved, overview.WorkspaceSurface(ws))
	if project != "" || global != "" {
		t.Fatalf("unsplit workspace badged project=%q global=%q, want neither", project, global)
	}
}
