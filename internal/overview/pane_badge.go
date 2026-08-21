package overview

import (
	"github.com/marcus/sidecar/internal/panebadge"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The list badges a split workspace rather than listing its panes, exactly as
// the project sidebar does, and from the same helper: the two lists are two
// projections of one model, and a workspace showing two panes is showing two
// panes in both of them.

// paneRowBadge is the layout glyph one row wears — every row, selected or not,
// read from the workspace's persisted tree.
//
// The project sidebar answers its selected row from its live tree instead, and
// that is not a divergence: there, the live tree IS the workspace's layout, the
// one that gets saved, so reading it only spares the badge a save's worth of
// lag. Here the preview tree is this browser's own ephemeral projection — it is
// built as a lone Terminal node per selection, never restored from disk and
// never written back, and it has no Shell content to hold a split terminal at
// all. Answering the selected row from it made a split workspace lose its badge
// the moment it was selected, which is precisely the disagreement between the
// two lists that the badge exists to avoid.
func (m *Model) paneRowBadge(workspace workspaceinventory.Workspace) string {
	surface := workspaceSurfaceIdentity(workspace)
	if surface == "" {
		return ""
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
