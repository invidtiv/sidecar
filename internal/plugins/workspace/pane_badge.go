package workspace

import (
	"github.com/marcus/sidecar/internal/panebadge"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/projectdir"
)

// A split workspace is one row with a badge, not a row per pane: rows stay 1:1
// with workspaces and the ⤷ indent stays reserved for worktree shells. The
// glyph comes from internal/panebadge, which the global Sessions list reads
// too, so the same workspace cannot be badged two ways in two lists.

// paneRowBadge is the layout glyph one sidebar row wears.
//
// The selected row is answered from the LIVE tree rather than from what is on
// disk. A split is persisted when it is saved, not when it is made, and a badge
// that appeared a save later would be telling the user about the pane they just
// closed rather than the one they just opened.
func (p *Plugin) paneRowBadge(surface string) string {
	if surface == "" {
		return ""
	}
	if surface == p.paneLayoutSurface && p.paneRoot != nil {
		return panebadge.Glyph(panelayout.LeafCount(p.paneRoot))
	}
	return panebadge.For(p.readWorkspaceState(), surface)
}

// shellRowBadge is the badge for one shell's row.
func (p *Plugin) shellRowBadge(shell *ShellSession) string {
	if shell == nil {
		return ""
	}
	return p.paneRowBadge(panebadge.ShellSurface(shell.TmuxName))
}

// worktreeRowBadge is the badge for one worktree's row. It resolves the same
// identity workspaceSurfaceIdentity persists under, so a row reads the layout
// its own workspace saved.
func (p *Plugin) worktreeRowBadge(wt *Worktree) string {
	if wt == nil {
		return ""
	}
	return p.paneRowBadge(workspaceSurfaceIdentity(wt))
}

// worktreeSurfaceKey is the key half of workspaceSurfaceIdentity, kept beside
// it so the badge and the persisted layout cannot resolve differently.
func worktreeSurfaceKey(wt *Worktree) string {
	if wt == nil {
		return ""
	}
	key := wt.IdentityKey()
	if wt.Key == "" {
		if canonical, err := projectdir.WorktreeKey(wt.Path); err == nil {
			key = canonical
		}
	}
	if key == "" {
		key = stablePathKey(wt.Path)
	}
	return key
}
