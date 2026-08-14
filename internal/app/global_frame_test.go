package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
)

// Slice 4 item 5 of docs/plans/active/global-overview-workspaces.md: the
// integrated frame. Wide and narrow, mouse and keyboard, themed and with Nerd
// Font glyphs off, the global space keeps its header, its footer, and its exact
// terminal box — and startup still costs the global space nothing.
//
// The list's internal narrow behaviour and the preview's own layout are proved
// in internal/workspacelist and internal/overview; what is proved here is the
// app frame around them.

func globalFrameModel(t *testing.T) Model {
	t.Helper()
	m, _ := scopeBaselineModel(t, "git")
	keymap.RegisterDefaults(m.keymap)
	m.scope, m.globalTab = ScopeGlobal, GlobalWorkspaces
	m.updateContext()
	return m
}

func TestGlobalWorkspacesFrameFitsEverySupportedSize(t *testing.T) {
	// 60x24 is sidecar's documented minimum; below it the app draws its own
	// "terminal too small" card and no space owns the screen.
	sizes := []struct{ w, h int }{{200, 50}, {140, 40}, {100, 30}, {72, 30}, {60, 24}}
	for _, size := range sizes {
		m := globalFrameModel(t)
		m.width, m.height, m.ready = size.w, size.h, true
		view := m.viewContent()
		if got := lipgloss.Height(view); got != size.h {
			t.Fatalf("%dx%d frame height = %d\n%s", size.w, size.h, got, view)
		}
		lines := strings.Split(view, "\n")
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > size.w {
				t.Fatalf("%dx%d line %d is %d columns wide: %q", size.w, size.h, i, got, ansi.Strip(line))
			}
		}
		plain := ansi.Strip(view)
		header, footer := ansi.Strip(lines[0]), ansi.Strip(lines[len(lines)-1])
		if !strings.Contains(header, "Sidecar") || !strings.Contains(header, "Overview") {
			t.Fatalf("%dx%d header lost the global destination: %q", size.w, size.h, header)
		}
		raw := m.renderHeader()
		if strings.Contains(raw, styles.Subtitle.Render(" / Overview")) {
			t.Fatalf("%dx%d Overview is still muted subtitle text: %q", size.w, size.h, header)
		}
		if !strings.Contains(raw, styles.BarChipActive.Render("Overview")) {
			t.Fatalf("%dx%d Overview is not a filled breadcrumb pill: %q", size.w, size.h, header)
		}
		if !strings.Contains(header, "Workspaces") {
			t.Fatalf("%dx%d header dropped the active global tab: %q", size.w, size.h, header)
		}
		for _, projectTab := range []string{"files", "notes"} {
			if strings.Contains(header, projectTab) {
				t.Fatalf("%dx%d header showed a project tab: %q", size.w, size.h, header)
			}
		}
		if !strings.Contains(footer, "Type") {
			t.Fatalf("%dx%d footer does not offer the Type action: %q", size.w, size.h, footer)
		}
		if !strings.Contains(plain, "Workspaces") {
			t.Fatalf("%dx%d content is not the browser:\n%s", size.w, size.h, plain)
		}
	}
}

// The footer follows the two-state model: the list advertises Type. Hiding the
// sidebar is layout only, so the table stays the list's.
func TestGlobalWorkspacesFooterFollowsFocus(t *testing.T) {
	m := globalFrameModel(t)
	m.width, m.height, m.ready = 160, 40, true

	list := footerLabels(m)
	if !containsHint(list, "Type") || !containsHint(list, "Filter") || containsHint(list, "Preview") {
		t.Fatalf("list footer = %v", list)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	m = asAppModel(t, updated)
	hidden := footerLabels(m)
	if !containsHint(hidden, "Type") || !containsHint(hidden, "Filter") || containsHint(hidden, "Scroll") {
		t.Fatalf("hidden-sidebar footer = %v, want the list table", hidden)
	}
	for _, forbidden := range []string{"Attach", "Interactive", "Delete", "New"} {
		if containsHint(list, forbidden) || containsHint(hidden, forbidden) {
			t.Fatalf("the read-only browser advertised %q: list=%v hidden=%v", forbidden, list, hidden)
		}
	}
}

func TestGlobalWorkspacesFooterFollowsIssueContext(t *testing.T) {
	m := globalFrameModel(t)
	hints := m.commandFooterHints([]plugin.Command{
		{ID: "open-item", Name: "Open", Context: "global-workspaces-issue", Priority: 1},
		{ID: "yank-issue", Name: "Yank", Context: "global-workspaces-issue", Priority: 2},
		{ID: "yank-issue-key", Name: "YankID", Context: "global-workspaces-issue", Priority: 3},
		{ID: "close", Name: "Close", Context: "global-workspaces-issue", Priority: 4},
		{ID: "pin", Name: "Pin", Context: "global-workspaces", Priority: 4},
	}, "global-workspaces-issue")
	labels := make([]string, 0, len(hints))
	for _, hint := range hints {
		labels = append(labels, hint.label)
	}
	if !containsHint(labels, "Yank") || !containsHint(labels, "YankID") || !containsHint(labels, "Close") {
		t.Fatalf("issue footer = %v, want Yank / YankID / Close", labels)
	}
	if containsHint(labels, "Pin") {
		t.Fatalf("issue footer leaked the list: %v", labels)
	}
}

// Pill tabs need a Nerd Font. With the glyphs off the header is plainer, but
// every tab is still labelled, still numbered, and still where the hit regions
// say it is.
func TestGlobalTabsAreReachableWithAndWithoutNerdFontGlyphs(t *testing.T) {
	original := styles.PillTabsEnabled
	t.Cleanup(func() { styles.PillTabsEnabled = original })

	for _, pills := range []bool{true, false} {
		styles.PillTabsEnabled = pills
		m := globalFrameModel(t)
		m.width, m.height, m.ready = 140, 40, true
		m.globalTab = GlobalAgents
		m.updateContext()

		header := m.renderHeader()
		if got := lipgloss.Width(header); got != m.width {
			t.Fatalf("pills=%v header width = %d, want %d", pills, got, m.width)
		}
		plain := ansi.Strip(header)
		if !strings.Contains(plain, "Agents") || !strings.Contains(plain, "Workspaces") {
			t.Fatalf("pills=%v header lost a tab label: %q", pills, plain)
		}

		var workspaces TabBounds
		found := false
		for _, bounds := range m.getTabBounds() {
			if bounds.Tab.scope == ScopeGlobal && bounds.Tab.global == GlobalWorkspaces {
				workspaces, found = bounds, true
			}
		}
		if !found {
			t.Fatalf("pills=%v registered no hit region for the Workspaces tab", pills)
		}
		clicked, _ := m.Update(tea.MouseClickMsg{X: (workspaces.Start + workspaces.End) / 2, Y: 0, Button: tea.MouseLeft})
		if got := asAppModel(t, clicked); got.globalTab != GlobalWorkspaces || !got.inGlobalScope() {
			t.Fatalf("pills=%v tab click landed on tab %v (global=%v)", pills, got.globalTab, got.inGlobalScope())
		}
		numbered, _ := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
		if got := asAppModel(t, numbered); got.globalTab != GlobalWorkspaces {
			t.Fatalf("pills=%v number row landed on tab %v", pills, got.globalTab)
		}
	}
}

// A theme changes colour, not content: the same frame renders the same text,
// and it really is styled rather than accidentally plain.
func TestGlobalWorkspacesRendersTheSameFrameUnderAnyTheme(t *testing.T) {
	t.Cleanup(func() { styles.ApplyTheme("default") })
	m := globalFrameModel(t)
	m.width, m.height, m.ready = 140, 40, true

	styles.ApplyTheme("default")
	first := m.viewContent()
	styles.ApplyTheme("dracula")
	second := m.viewContent()

	if ansi.Strip(first) != ansi.Strip(second) {
		t.Fatalf("theme changed the content, not just the colours\n--- default ---\n%s\n--- dracula ---\n%s",
			ansi.Strip(first), ansi.Strip(second))
	}
	if first == second {
		t.Fatal("the two themes produced byte-identical output; the browser is not themed")
	}
	if lipgloss.Height(second) != m.height {
		t.Fatalf("themed frame height = %d, want %d", lipgloss.Height(second), m.height)
	}
}

func TestGlobalHeaderKeepsOverviewAfterClockDrops(t *testing.T) {
	m := globalFrameModel(t)
	m.showClock = true
	m.ui.Clock = time.Date(2025, 1, 1, 14, 30, 0, 0, time.UTC)
	m.width, m.height, m.ready = 200, 40, true

	title, tabs, clock, _ := m.headerLayout()
	if clock == "" || !strings.Contains(ansi.Strip(title), "Overview") {
		t.Fatalf("wide header lost clock or Overview: title=%q clock=%q", ansi.Strip(title), ansi.Strip(clock))
	}
	tabsWidth := 0
	for i, tab := range tabs {
		tabsWidth += lipgloss.Width(tab.text)
		if i > 0 {
			tabsWidth++
		}
	}
	// One column below the full row: clock is the first thing headerLayout drops.
	m.width = lipgloss.Width(title) + tabsWidth + lipgloss.Width(clock) - 1
	header := m.renderHeader()
	plain := ansi.Strip(header)
	if strings.Contains(plain, "14:30") {
		t.Fatalf("clock should have dropped at width %d: %q", m.width, plain)
	}
	if !strings.Contains(plain, "Overview") {
		t.Fatalf("Overview vanished after the clock dropped: %q", plain)
	}
	if !strings.Contains(header, styles.BarChipActive.Render("Overview")) {
		t.Fatalf("Overview pill missing after the clock dropped: %q", plain)
	}
	if len(m.getTabBounds()) != len(tabs) {
		t.Fatalf("dropping the clock also dropped a tab: bounds=%d want %d", len(m.getTabBounds()), len(tabs))
	}

	// Keep shrinking until an inactive tab is gone. The destination pill
	// stays: headerLayout drops inactive tabs before the protected title.
	for m.width > 20 && len(m.getTabBounds()) == len(tabs) {
		m.width--
	}
	plain = ansi.Strip(m.renderHeader())
	if !strings.Contains(plain, "Overview") {
		t.Fatalf("Overview vanished before inactive tabs were gone: %q", plain)
	}
	if len(m.getTabBounds()) == 0 {
		t.Fatal("narrow header dropped every tab")
	}
}

// Startup owes the global space nothing: the first frame is the project, and no
// cross-project work has happened when it is drawn.
func TestFirstFrameIsTheProjectAndCostsTheGlobalSpaceNothing(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.width, m.height, m.ready = 160, 40, true
	if cmd := m.Init(); cmd != nil {
		cmd()
	}
	view := m.viewContent()
	plain := ansi.Strip(view)
	if strings.Contains(strings.Split(plain, "\n")[0], "Overview") {
		t.Fatalf("the first frame opened the global space: %q", strings.Split(plain, "\n")[0])
	}
	if m.inGlobalScope() {
		t.Fatal("startup entered the global space")
	}
	if lipgloss.Height(view) != m.height {
		t.Fatalf("first frame height = %d, want %d", lipgloss.Height(view), m.height)
	}
	if m.overview.WorkspacesPreviewActive() || m.overview.WorkspacesPreviewVisible() {
		t.Fatalf("startup started preview work: active=%v visible=%v", m.overview.WorkspacesPreviewActive(), m.overview.WorkspacesPreviewVisible())
	}
}

func footerLabels(m Model) []string {
	labels := make([]string, 0, 8)
	for _, hint := range m.footerHints() {
		labels = append(labels, hint.label)
	}
	return labels
}

func containsHint(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(label, want) {
			return true
		}
	}
	return false
}
