// Package panebadge derives a workspace row's compact layout badge from the
// pane tree that workspace has persisted.
//
// It exists as one helper rather than two because the project workspace sidebar
// and the global Workspaces browser ("Sessions") are two projections of one
// model: a workspace showing two panes on one surface is showing two panes on
// the other, and a badge that disagreed between the lists would be saying the
// workspace itself was two different things. The rows keep their own two-line
// design — this only supplies the glyph they put in the metadata they already
// render.
package panebadge

import (
	"strconv"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/state"
)

const (
	// SplitGlyph is a two-pane layout: one workspace, split once.
	SplitGlyph = "◧◨"
	// GridPrefix heads a layout with more panes than a single split, followed
	// by the count, because past two the arrangement stops being something a
	// glyph can draw at this size and the number is the useful part.
	GridPrefix = "⊞"
)

// ShellSurface is the persisted surface identity of one shell's pane tree.
func ShellSurface(tmuxName string) string {
	if tmuxName == "" {
		return ""
	}
	return "shell:" + tmuxName
}

// WorktreeSurface is the persisted surface identity of one worktree's pane
// tree, given the key the caller already resolved for it.
func WorktreeSurface(key string) string {
	if key == "" {
		return ""
	}
	return "workspace:" + key
}

// WorktreeSurfaceFor is WorktreeSurface for a worktree named only by its path,
// which is how the global list holds one. It resolves the same key the project
// surface persists under, so the two lists reach one saved layout rather than
// two near-misses.
func WorktreeSurfaceFor(path string) string {
	key, err := projectdir.WorktreeKey(path)
	if err != nil {
		return ""
	}
	return WorktreeSurface(key)
}

// ContentLeaves counts the content panes a persisted tree restores to. A
// layout that is stored but hidden restores no panes, so it counts as none:
// the badge says what is on screen, not what is remembered.
func ContentLeaves(layout *state.PaneLayoutJSON) int {
	if !state.PaneLayoutOpen(layout) {
		return 0
	}
	return countLeaves(layout)
}

func countLeaves(layout *state.PaneLayoutJSON) int {
	if layout == nil {
		return 0
	}
	if layout.Split == nil {
		return 1
	}
	return countLeaves(layout.Split.A) + countLeaves(layout.Split.B)
}

// GlyphFor is the badge one persisted tree earns: nothing for the single pane
// every workspace has, the split pair for two, the grid mark and a count beyond.
func GlyphFor(layout *state.PaneLayoutJSON) string {
	return Glyph(ContentLeaves(layout))
}

// Glyph is GlyphFor's rule stated over a bare pane count, so a caller holding a
// live tree rather than a persisted one badges it the same way.
func Glyph(panes int) string {
	switch {
	case panes < 2:
		return ""
	case panes == 2:
		return SplitGlyph
	default:
		return GridPrefix + strconv.Itoa(panes)
	}
}

// For is the badge for one surface of one project's saved workspace state. It
// is the call both sidebars make, so neither can reach a different layout for
// the same row.
func For(wtState state.WorkspaceState, surface string) string {
	if surface == "" {
		return ""
	}
	return GlyphFor(wtState.PaneLayoutFor(surface))
}
