package workspacediff

import "github.com/marcus/sidecar/internal/styles"

// TabSet is which preview chips a surface may show.
type TabSet int

const (
	// TabSetNone is shells (project plugin) and the main worktree.
	TabSetNone TabSet = iota
	// TabSetOutputDiffTask is a topic worktree: Output / Diff / Task.
	TabSetOutputDiffTask
	// TabSetOutputDiff is a global shell: Output / Diff, no Task.
	TabSetOutputDiff
)

// TabsVisible reports whether Output / Diff / Task chips belong on a preview:
// not a shell, and not the main worktree. The project plugin keeps this rule.
func TabsVisible(isShell, isMain bool) bool {
	return TabsFor(isShell, isMain) != TabSetNone
}

// TabsFor is the project-plugin chip set: topic worktrees only.
func TabsFor(isShell, isMain bool) TabSet {
	if isShell || isMain {
		return TabSetNone
	}
	return TabSetOutputDiffTask
}

// GlobalTabsFor is the global Workspaces chip set: topic worktrees keep all
// three chips; shells get Output + Diff (no Task); the main worktree stays tabless.
func GlobalTabsFor(isShell, isMain bool) TabSet {
	if isMain {
		return TabSetNone
	}
	if isShell {
		return TabSetOutputDiff
	}
	return TabSetOutputDiffTask
}

// Visible reports that this set draws a tab row.
func (s TabSet) Visible() bool { return s != TabSetNone }

// Tabs is the allowed tabs in cycle order.
func (s TabSet) Tabs() []Tab {
	switch s {
	case TabSetOutputDiff:
		return []Tab{TabOutput, TabDiff}
	case TabSetOutputDiffTask:
		return []Tab{TabOutput, TabDiff, TabTask}
	default:
		return nil
	}
}

// Contains reports that tab is in this set.
func (s TabSet) Contains(tab Tab) bool {
	for _, allowed := range s.Tabs() {
		if allowed == tab {
			return true
		}
	}
	return false
}

// CycleTab walks Output → Diff → Task (or the reverse) for a topic worktree.
func CycleTab(current Tab, delta int) Tab {
	return CycleTabIn(current, delta, TabSetOutputDiffTask)
}

// CycleTabIn walks only the tabs this set allows.
func CycleTabIn(current Tab, delta int, set TabSet) Tab {
	tabs := set.Tabs()
	if len(tabs) == 0 {
		return TabOutput
	}
	idx := 0
	for i, tab := range tabs {
		if tab == current {
			idx = i
			break
		}
	}
	n := len(tabs)
	return tabs[(idx+delta%n+n)%n]
}

// TabChips renders the Output / Diff / Task pills. The same chips the project
// plugin draws, so both surfaces drop whole chips rather than clip one.
func TabChips(active Tab) []string {
	return TabChipsFor(active, TabSetOutputDiffTask)
}

// TabChipsFor renders only the chips this set allows.
func TabChipsFor(active Tab, set TabSet) []string {
	labels := map[Tab]string{TabOutput: "Output", TabDiff: "Diff", TabTask: "Task"}
	tabs := set.Tabs()
	rendered := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		style := styles.BarChip
		if tab == active {
			style = styles.BarChipActive
		}
		rendered = append(rendered, styles.RenderPillWithStyle(labels[tab], style, nil))
	}
	return rendered
}
