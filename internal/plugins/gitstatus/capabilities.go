package gitstatus

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

const (
	gitSidebarFocusID = "sidebar"
	gitDiffFocusID    = "diff"
)

var (
	_ plugin.PaneFocusProvider   = (*Plugin)(nil)
	_ plugin.ContentLinkProvider = (*Plugin)(nil)
)

// PaneFocusStops projects Git's existing sidebar/diff focus in visual order.
// Modal modes keep the same background projection, but retain their existing
// key ownership through BlocksGlobalKeys and ConsumesTextInput.
func (p *Plugin) PaneFocusStops() []plugin.PaneFocusStop {
	if p == nil || p.inNoRepoMode() || p.viewMode != ViewModeStatus {
		return nil
	}
	stops := make([]plugin.PaneFocusStop, 0, 2)
	if p.sidebarVisible {
		stops = append(stops, plugin.PaneFocusStop{ID: gitSidebarFocusID})
	}
	if p.diffPaneOnScreen() || !p.sidebarVisible {
		stops = append(stops, plugin.PaneFocusStop{ID: gitDiffFocusID})
	}
	return stops
}

func (p *Plugin) PaneFocus() string {
	if p.activePane == PaneDiff {
		return gitDiffFocusID
	}
	return gitSidebarFocusID
}

func (p *Plugin) SetPaneFocus(id string) tea.Cmd {
	switch id {
	case gitSidebarFocusID:
		if p.sidebarVisible {
			p.activePane = PaneSidebar
		}
	case gitDiffFocusID:
		if p.viewMode == ViewModeStatus && (p.diffPaneOnScreen() || !p.sidebarVisible) {
			p.activePane = PaneDiff
		}
	}
	return nil
}

func (p *Plugin) SetPaneFocusActive(active bool) {
	p.paneFocusManaged = true
	p.paneFocusActive = active
}

func (p *Plugin) innerPaneFocusActive() bool {
	return !p.paneFocusManaged || p.paneFocusActive
}

// ContentLinkSurfaces exposes only passive rendered diff text or a commit's
// subject/body. Sidebar rows, headers, gutters outside the diff renderer,
// minimaps, dividers, and every interactive overlay remain outside the zone.
func (p *Plugin) ContentLinkSurfaces() []contentlink.Surface {
	if !p.contentLinksSafe() {
		return nil
	}
	var rect mouse.Rect
	var id string
	if p.viewMode == ViewModeStatus && p.hasSelectedCommit() {
		rect = p.commitDescriptionRect()
		id = "git-commit-description"
	} else {
		rect = p.diffTextRect()
		id = "git-diff-content"
	}
	if rect.W <= 0 || rect.H <= 0 {
		return nil
	}
	return []contentlink.Surface{{
		ID:          id,
		Rect:        rect,
		WorkDir:     p.repoRoot,
		ProjectRoot: p.ctx.ProjectRoot,
		Kinds: contentlink.NewKindSet(
			contentlink.KindFile,
			contentlink.KindIssue,
			contentlink.KindDiff,
			contentlink.KindResource,
			contentlink.KindURL,
			contentlink.KindInternal,
		),
		ReadOnly: true,
	}}
}

func (p *Plugin) contentLinksSafe() bool {
	if p == nil || p.ctx == nil || !p.hasRepo || p.repoRoot == "" || p.width <= 0 || p.height <= 0 {
		return false
	}
	if p.historySearchMode || p.pathFilterMode || p.historyFilterActive {
		return false
	}
	if p.viewMode != ViewModeStatus && p.viewMode != ViewModeDiff {
		return false
	}
	if p.viewMode == ViewModeStatus && p.hasSelectedCommit() {
		return p.previewCommit != nil && p.previewCommitError == ""
	}
	if p.viewMode == ViewModeStatus && p.selectedDiffFile == "" {
		return false
	}
	if p.viewMode == ViewModeDiff && p.diffFile == "" {
		return false
	}
	return p.diffTextReady()
}

func (p *Plugin) diffTextReady() bool {
	var mode DiffViewMode
	var parsed *ParsedDiff
	var full *FullFileDiff
	var raw string
	if p.viewMode == ViewModeDiff {
		mode, parsed, full, raw = p.diffViewMode, p.parsedDiff, p.fullFileDiff, p.diffRaw
	} else {
		mode, parsed, full = p.diffPaneViewMode, p.diffPaneParsedDiff, p.diffPaneFullFileDiff
	}
	if mode == DiffViewFullFile {
		return full != nil && len(full.Lines) > 0
	}
	if parsed != nil {
		return !parsed.Binary && len(parsed.Hunks) > 0
	}
	return p.viewMode == ViewModeDiff && strings.TrimSpace(raw) != ""
}

func (p *Plugin) diffTextRect() mouse.Rect {
	panelX := 0
	panelWidth := p.width
	if p.sidebarVisible {
		panelX = p.sidebarWidth + dividerWidth
		panelWidth = p.diffPaneWidth
	}
	width := panelWidth - 4 // border plus horizontal panel padding
	if width <= 0 {
		return mouse.Rect{}
	}
	mode := p.diffPaneViewMode
	full := p.diffPaneFullFileDiff
	scroll := p.diffPaneScroll
	if p.viewMode == ViewModeDiff {
		mode, full, scroll = p.diffViewMode, p.fullFileDiff, p.diffScroll
	}
	height := p.height - 4 // border, header, and blank header separator
	if height < 1 {
		height = 1
	}
	if mode == DiffViewFullFile && full != nil && !p.diffWrapEnabled {
		// The minimap occupies the right edge whenever the renderer has enough
		// rows and width to draw it. Conservatively reserving it when short keeps
		// the declared zone a strict subset of source text.
		if width-MinimapWidth >= 30 && RenderMinimap(full, scroll, height, height) != "" {
			width -= MinimapWidth
		}
	}
	return mouse.Rect{X: panelX + 2, Y: 3, W: width, H: height}
}

func (p *Plugin) commitDescriptionRect() mouse.Rect {
	panelX := 0
	if p.sidebarVisible {
		panelX = p.sidebarWidth + dividerWidth
	}
	height := 1 // subject
	bodyLines := strings.Split(strings.TrimSpace(p.previewCommit.Body), "\n")
	if p.previewCommit.Body != "" {
		height++ // blank line before body
		if p.commitBodyExpanded {
			capacity := p.height - 9
			if capacity < 3 {
				capacity = 3
			}
			height += min(len(bodyLines), capacity)
		} else {
			shown := min(len(bodyLines), 3)
			height += shown
		}
	}
	return mouse.Rect{X: panelX + 2, Y: 6, W: p.diffPaneWidth - 4, H: height}
}
