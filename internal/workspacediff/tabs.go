package workspacediff

import "github.com/marcus/sidecar/internal/styles"

// TabsVisible reports whether Output / Diff / Task chips belong on a preview:
// not a shell, and not the main worktree.
func TabsVisible(isShell, isMain bool) bool {
	return !isShell && !isMain
}

// CycleTab walks Output → Diff → Task (or the reverse).
func CycleTab(current Tab, delta int) Tab {
	return Tab((int(current) + delta + 3) % 3)
}

// TabChips renders the Output / Diff / Task pills. The same chips the project
// plugin draws, so both surfaces drop whole chips rather than clip one.
func TabChips(active Tab) []string {
	tabs := []string{"Output", "Diff", "Task"}
	rendered := make([]string, 0, len(tabs))
	for i, tab := range tabs {
		style := styles.BarChip
		if Tab(i) == active {
			style = styles.BarChipActive
		}
		rendered = append(rendered, styles.RenderPillWithStyle(tab, style, nil))
	}
	return rendered
}
