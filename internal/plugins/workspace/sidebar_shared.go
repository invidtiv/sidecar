package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspacelist"
)

type sidebarNavKind int

const (
	navKindShell sidebarNavKind = iota
	navKindWorktree
	navKindNestedShell
)

type sidebarNavItem struct {
	kind        sidebarNavKind
	shellIdx    int
	worktreeIdx int
	shell       *ShellSession
}

// nestedShellHit is the hit-map payload for a sibling shell nested under a
// worktree. Top-section shells keep the existing negative-int encoding.
type nestedShellHit struct {
	TmuxName string
}

func (p *Plugin) visibleNestedShells(wt *Worktree) []*ShellSession {
	if wt == nil || p.isCurrentWorkDir(wt.Path) {
		return nil
	}
	shells := p.nestedByWorkDir[filepath.Clean(wt.Path)]
	query := p.listFilter.Query()
	if query == "" {
		return shells
	}
	out := make([]*ShellSession, 0, len(shells))
	for _, shell := range shells {
		if workspacelist.MatchFields(query, p.shellFilterFields(shell)...) {
			out = append(out, shell)
		}
	}
	return out
}

func (p *Plugin) visibleSidebarItems() []sidebarNavItem {
	items := make([]sidebarNavItem, 0, len(p.shells)+len(p.worktrees)+p.nestedShellTotal())
	for _, index := range p.visibleShellIndices() {
		items = append(items, sidebarNavItem{kind: navKindShell, shellIdx: index})
	}
	for _, index := range p.visibleWorktreeIndices() {
		items = append(items, sidebarNavItem{kind: navKindWorktree, worktreeIdx: index})
		wt := p.worktrees[index]
		for _, shell := range p.visibleNestedShells(wt) {
			items = append(items, sidebarNavItem{kind: navKindNestedShell, worktreeIdx: index, shell: shell})
		}
	}
	return items
}

func (p *Plugin) selectSidebarItem(item sidebarNavItem) {
	switch item.kind {
	case navKindShell:
		p.selectTopShellAt(item.shellIdx)
	case navKindWorktree:
		p.selectWorktreeAt(item.worktreeIdx)
	case navKindNestedShell:
		tmuxName := ""
		if item.shell != nil {
			tmuxName = item.shell.TmuxName
		}
		p.selectNestedShell(item.worktreeIdx, tmuxName)
	}
}

func (p *Plugin) sidebarItemSelected(item sidebarNavItem) bool {
	switch item.kind {
	case navKindShell:
		return p.shellSelected && p.selectedShellIdx == item.shellIdx
	case navKindWorktree:
		return !p.shellSelected && p.selectedNestedTmux == "" && p.selectedIdx == item.worktreeIdx
	case navKindNestedShell:
		return !p.shellSelected && item.shell != nil && p.selectedNestedTmux == item.shell.TmuxName
	default:
		return false
	}
}

// toastDuration is how long a toast stays up. It is longer than the attach
// flash because a toast is the only place a refused action explains itself: the
// window it appears in is the narrow one that caused the refusal, so a reader
// needs long enough to find it as well as to read it.
const toastDuration = 4 * time.Second

// fitToast picks the longest form of a toast message that survives the sidebar
// it is drawn in. The sidebar of the window narrow enough to refuse a split is
// itself narrow — seventeen columns at 60x24 — and a refusal truncated to
// "⚠ Document pan…" is a message that never reaches the user it is for.
func fitToast(msg string, width int) string {
	candidates := []string{msg}
	if head, _, ok := strings.Cut(msg, ";"); ok {
		candidates = append(candidates, strings.TrimSpace(head))
	}
	switch {
	case strings.Contains(msg, "wider"):
		candidates = append(candidates, "Needs a wider window", "Too narrow")
	case strings.Contains(msg, "taller"):
		candidates = append(candidates, "Needs a taller window", "Too short")
	}
	for _, candidate := range candidates {
		if ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	return ansi.Truncate(candidates[len(candidates)-1], width, "…")
}

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
	if p.toastMessage != "" && !p.toastTime.IsZero() && time.Since(p.toastTime) < toastDuration {
		warnings = append(warnings, warningStyle.Bold(true).Render("⚠ "+fitToast(p.toastMessage, max(1, width-2))))
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
				return []string{p.renderWorktreeSidebarItem(wt, selected, rowWidth)}
			}})
			for _, shell := range p.visibleNestedShells(wt) {
				shell := shell
				nestedID := "nested:" + shell.TmuxName
				if shell.TmuxName == "" {
					nestedID = fmt.Sprintf("nested:%s:%s", wt.IdentityKey(), shell.Name)
				}
				section.Rows = append(section.Rows, workspacelist.SidebarRow{ID: nestedID, Data: nestedShellHit{TmuxName: shell.TmuxName}, Render: func(rowWidth int, selected, _ bool) []string {
					return []string{p.renderNestedShellEntry(shell, selected, rowWidth)}
				}})
			}
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
	} else if p.selectedNestedTmux != "" {
		selectedID = "nested:" + p.selectedNestedTmux
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
	return len(p.visibleSidebarItems())
}

func (p *Plugin) sharedSidebarSelectionIndex() int {
	items := p.visibleSidebarItems()
	for i, item := range items {
		if p.sidebarItemSelected(item) {
			return i
		}
	}
	return -1
}
