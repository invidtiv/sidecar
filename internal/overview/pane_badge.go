package overview

import (
	"github.com/marcus/sidecar/internal/panebadge"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The list badges a split workspace rather than listing its panes, exactly as
// the project sidebar does, and from the same helper: the two lists are two
// projections of one model, and a workspace showing two panes is showing two
// panes in both of them.

// paneRowBadge is the layout glyph one row wears.
//
// The selected row is answered from the LIVE preview tree when this surface is
// the one that placed it, for the reason the project sidebar answers it that
// way: a split is persisted on save, not on creation, and a badge read only
// from disk would lag the pane the user just opened.
func (m *Model) paneRowBadge(workspace workspaceinventory.Workspace) string {
	surface := workspaceSurfaceIdentity(workspace)
	if surface == "" {
		return ""
	}
	if selected, ok := m.SelectedWorkspace(); ok && selected.ID == workspace.ID && m.preview.paneRoot != nil {
		return panebadge.Glyph(panelayout.LeafCount(m.preview.paneRoot))
	}
	return RowLayoutBadge(state.GetWorkspaceState(workspace.ProjectRoot), surface)
}

// RowLayoutBadge is the layout glyph one workspace's row wears, given the
// project state that workspace was saved in. It is a state-free rule on
// purpose: it is what the project sidebar's own badge is measured against, and
// a rule that could only be reached through a running Model could not be.
func RowLayoutBadge(wtState state.WorkspaceState, surface string) string {
	return panebadge.For(wtState, surface)
}

// WorkspaceSurface is the key one workspace's pane tree is persisted under.
// Exported for the same reason RowLayoutBadge is: the two surfaces have to
// agree on it, and a test that cannot ask both of them proves nothing.
func WorkspaceSurface(workspace workspaceinventory.Workspace) string {
	return workspaceSurfaceIdentity(workspace)
}

// workspaceSurfaceIdentity is the key one workspace's pane tree is persisted
// under. It is the project surface's rule, reached through the shared helper so
// the two cannot resolve differently for the same workspace.
func workspaceSurfaceIdentity(workspace workspaceinventory.Workspace) string {
	if workspace.Kind == workspaceinventory.KindShell {
		return panebadge.ShellSurface(workspace.TmuxName)
	}
	return panebadge.WorktreeSurfaceFor(workspace.Path)
}
