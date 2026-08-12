package workspace

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// renderSidebarContent projects project-owned shells, worktrees and optional
// lifecycle actions into the same presentation component used by global
// Workspaces. Nothing in workspacelist can create, attach, delete or load a
// preview; those remain callbacks reached through the typed regions below.
func (p *Plugin) renderSidebarContent(width, height int) string {
	warnings := make([]string, 0, len(p.deleteWarnings)+1)
	warningStyle := lipgloss.NewStyle().Foreground(styles.Warning)
	for _, warning := range p.deleteWarnings {
		warnings = append(warnings, warningStyle.Render("⚠ "+ansi.Truncate(warning, max(1, width-2), "…")))
	}
	if p.toastMessage != "" && !p.toastTime.IsZero() && time.Since(p.toastTime) < flashDuration {
		warnings = append(warnings, warningStyle.Bold(true).Render("⚠ "+ansi.Truncate(p.toastMessage, max(1, width-2), "…")))
	}

	matched, total := p.filterCounts()
	sections := make([]workspacelist.SidebarSection, 0, 2)
	visibleShells := p.visibleShellIndices()
	if len(visibleShells) > 0 {
		section := workspacelist.SidebarSection{Title: workspacelist.SectionTitle("Shells", len(visibleShells)), Action: &workspacelist.SidebarAction{ID: regionShellsPlusButton, Label: "+", Hovered: p.hoverShellsPlusButton}}
		for _, index := range visibleShells {
			index := index
			shell := p.shells[index]
			id := shell.TmuxName
			if id == "" {
				id = fmt.Sprintf("shell:%s:%d", shell.Name, index)
			} else {
				id = "shell:" + id
			}
			section.Rows = append(section.Rows, workspacelist.SidebarRow{ID: id, Data: -(index + 1), Render: func(rowWidth int, selected, _ bool) []string {
				return []string{p.renderShellEntryForSession(shell, selected, rowWidth)}
			}})
		}
		sections = append(sections, section)
	}
	visibleWorktrees := p.visibleWorktreeIndices()
	if len(visibleWorktrees) > 0 {
		section := workspacelist.SidebarSection{Title: workspacelist.SectionTitle("Workspaces", len(visibleWorktrees))}
		// With no Shells section above it this heading lands one row under the
		// panel header, whose "New" creates the same thing the "+" would.
		if len(visibleShells) > 0 {
			section.Action = &workspacelist.SidebarAction{ID: regionWorkspacesPlusButton, Label: "+", Hovered: p.hoverWorkspacesPlusButton}
		}
		for _, index := range visibleWorktrees {
			index := index
			wt := p.worktrees[index]
			id := "worktree:" + wt.IdentityKey()
			section.Rows = append(section.Rows, workspacelist.SidebarRow{ID: id, Data: index, Render: func(rowWidth int, selected, _ bool) []string {
				return []string{p.renderWorktreeItem(wt, selected, rowWidth)}
			}})
		}
		sections = append(sections, section)
	}

	selectedID := ""
	if p.shellSelected && p.selectedShellIdx >= 0 && p.selectedShellIdx < len(p.shells) {
		shell := p.shells[p.selectedShellIdx]
		if shell.TmuxName == "" {
			selectedID = fmt.Sprintf("shell:%s:%d", shell.Name, p.selectedShellIdx)
		} else {
			selectedID = "shell:" + shell.TmuxName
		}
	} else if p.selectedIdx >= 0 && p.selectedIdx < len(p.worktrees) {
		selectedID = "worktree:" + p.worktrees[p.selectedIdx].IdentityKey()
	}

	empty := []string(nil)
	if len(visibleShells)+len(visibleWorktrees) == 0 {
		if p.filterActive() {
			empty = []string{workspacelist.NoMatchRow(max(1, width-1), p.listFilter.Query())}
		} else if len(p.shells)+len(p.worktrees) == 0 {
			empty = []string{styles.Muted.Render("No workspaces"), styles.Muted.Render("Press 'n' to create one")}
		}
	}

	rendered := workspacelist.RenderSidebar(workspacelist.SidebarOptions{
		Width: width, Height: height, Title: "Workspaces", Focused: p.activePane == PaneSidebar,
		SelectedID: selectedID, ScrollOffset: p.scrollOffset,
		HeaderAction: &workspacelist.SidebarAction{ID: regionCreateWorktreeButton, Label: "New", Hovered: p.hoverNewButton},
		PrefixLines:  warnings, FilterActive: p.filterActive(), FilterLine: p.listFilter.RenderRow(width, matched, total),
		Sections: sections, EmptyLines: empty,
	})
	p.scrollOffset, p.visibleCount = rendered.ScrollOffset, rendered.VisibleRows
	for _, region := range rendered.Regions {
		id, data := string(region.Kind), region.Data
		switch region.Kind {
		case workspacelist.RegionHeaderAction, workspacelist.RegionSectionAction:
			id = region.ID
		case workspacelist.RegionRow:
			id = regionWorktreeItem
		case workspacelist.RegionFilter:
			id = regionListFilter
		}
		// Panel content begins one row and two columns inside RenderPanel.
		p.mouseHandler.HitMap.AddRect(id, 2+region.X, 1+region.Y, region.W, region.H, data)
	}
	return rendered.View
}

func (p *Plugin) sharedSidebarRowCount() int {
	return len(p.visibleShellIndices()) + len(p.visibleWorktreeIndices())
}

func (p *Plugin) sharedSidebarSelectionIndex() int {
	shells, worktrees := p.visibleShellIndices(), p.visibleWorktreeIndices()
	if p.shellSelected {
		return indexOfValue(shells, p.selectedShellIdx)
	}
	position := indexOfValue(worktrees, p.selectedIdx)
	if position < 0 {
		return -1
	}
	return len(shells) + position
}
