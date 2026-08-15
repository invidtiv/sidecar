package workspacelist

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Baseline capture for the workspace sidebar redesign
// (docs/plans/implemented/workspace-sidebar-redesign-baseline.md).
//
// The redesign's central claim is that project and global Workspaces should
// share one header grammar and one row grammar. Today they share a renderer but
// not a grammar: they hand RenderSidebar different chrome, different section
// structure, and differently prioritised row fields. That difference is easy to
// argue about and hard to see, because the two surfaces are composed in two
// packages and never rendered side by side.
//
// This test renders both shapes from one set of semantic records at the four
// responsive tiers the plan names, and pins the result in a golden file. It is a
// characterization of what ships today, not an assertion that today is correct:
// every later slice is expected to change it, and the diff is the point.
//
// Colour is deliberately stripped. The golden captures structure — field order,
// section wording, control placement, and width degradation — which is what the
// redesign moves. Theme tokens are covered by the styles package and would make
// this file unreadable in review.
//
// Regenerate with:
//
//	go test ./internal/workspacelist -run TestSidebarBaselineFixture -update

var updateBaseline = flag.Bool("update", false, "rewrite the sidebar baseline golden file")

// baselineNow is a fixed clock so relative ages ("2m", "4h") are stable.
var baselineNow = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return baselineNow.Add(-d) }

// baselineItems is one catalog of records spanning the states the row renderer
// distinguishes: an agent needing attention, a working agent, a plain live
// shell, an idle worktree, and a worktree with no session at all. Two projects
// are present so the global shape has something to prefix.
func baselineItems() []Item {
	return []Item{
		{
			ID: "sidecar:release-fix", Name: "release fix", Kind: KindWorktree,
			Project: "sidecar", ProjectKey: "sidecar", ProjectOrder: 0,
			Branch: "fix/release", Task: "td-a12bc3", Provider: "claude",
			Status: "blocked", Detail: "td-a12bc3 · +41 -3",
			Marker: RowMarker{Icon: "◆", Lane: "blocked"}, Group: GroupNeedsAttention,
			ChangedAt: ago(2 * time.Minute),
		},
		{
			ID: "braid:model-benchmark", Name: "model benchmark", Kind: KindShell,
			Project: "braid", ProjectKey: "braid", ProjectOrder: 1,
			Provider: "grok", Status: "working", Detail: "working",
			Marker: RowMarker{Icon: "●", Lane: "working"}, Group: GroupWorking,
			ChangedAt: ago(4 * time.Minute),
		},
		{
			ID: "sidecar:fix-terminal-resize", Name: "fix terminal resize", Kind: KindShell,
			Project: "sidecar", ProjectKey: "sidecar", ProjectOrder: 0,
			Provider: "codex", Status: "working", Detail: "working",
			Marker: RowMarker{Icon: "●", Lane: "working"}, Group: GroupWorking,
			ChangedAt: ago(9 * time.Minute),
		},
		{
			ID: "sidecar:scratch", Name: "scratch", Kind: KindShell,
			Project: "sidecar", ProjectKey: "sidecar", ProjectOrder: 0,
			Status: "live", Marker: RowMarker{Icon: "◎", Tone: MarkerLive}, Group: GroupLive,
			ChangedAt: ago(50 * time.Minute),
		},
		{
			ID: "braid:global-workspace-shortcut", Name: "global workspace shortcut", Kind: KindWorktree,
			Project: "braid", ProjectKey: "braid", ProjectOrder: 1,
			Branch: "feature/global", Provider: "grok", Status: "idle", Detail: "feature/global",
			Marker: RowMarker{Icon: "○", Tone: MarkerMuted}, Group: GroupIdle,
			ChangedAt: ago(3 * time.Hour),
		},
		{
			ID: "sidecar:main", Name: "main", Kind: KindWorktree,
			Project: "sidecar", ProjectKey: "sidecar", ProjectOrder: 0,
			Branch: "main", Status: "no session", Detail: "main",
			Marker: RowMarker{Icon: "◉", Tone: MarkerMain}, Group: GroupNoSession,
			ChangedAt: ago(26 * time.Hour),
		},
	}
}

// renderGlobalShape is exactly how internal/overview draws the global browser:
// a Model with a sort label in the header and activity-grouped sections.
func renderGlobalShape(width, height int) string {
	var m Model
	m.SetItems(baselineItems())
	m.SetSort(SortActivity)
	return m.Render(RenderOptions{Width: width, Height: height, Title: "Workspaces", Focused: true, Now: baselineNow}).View
}

// renderProjectShape is how internal/plugins/workspace composes the same
// records: the shared [sort] [+] header, fixed Shells/Worktrees sections each
// with their own "+", and no project prefix.
//
// It reproduces the plugin's SidebarOptions rather than importing it, because
// the plugin owns a live tmux/Git model this package must never depend on. The
// details below track view_list.go and sidebar_shared.go:
//
//   - both kinds carry their glyph and both carry an age, so the two sections
//     share one gutter width and one right-hand column;
//   - shell rows always carry a status word on line two ("live", "no session"),
//     so unlike global they never render an empty second line;
//   - worktree rows lead line two with a state label ("branch X");
//   - the main checkout is not offered as a row at all.
func renderProjectShape(width, height int) string {
	shells := SidebarSection{Action: &SidebarAction{ID: "shells-plus", Label: "+"}}
	worktrees := SidebarSection{Action: &SidebarAction{ID: "workspaces-plus", Label: "+"}}
	for _, item := range baselineItems() {
		if item.Project != "sidecar" {
			continue // project scope shows one project's records only
		}
		if item.Name == "main" {
			continue // the main checkout is not a workspace row
		}
		item := item
		isShell := item.Kind == KindShell
		row := SidebarRow{ID: item.ID, Data: item.ID, Render: func(w int, selected, focused bool) []string {
			presentation := RowPresentation{
				Marker: item.Marker, Kind: item.Kind, Name: item.Name,
				Provider: item.Provider, Age: RelativeAge(item.ChangedAt, baselineNow),
			}
			if isShell {
				if item.Provider == "" {
					presentation.BeforeProvider = []RowField{PlainField("shell")}
				}
				presentation.AfterProvider = []RowField{PlainField(item.Status)}
			} else {
				presentation.BeforeProvider = []RowField{PlainField("branch " + item.Branch)}
				if item.Task != "" {
					presentation.AfterProvider = []RowField{PlainField(item.Task)}
				}
			}
			return RenderRow(presentation, w, selected, focused)
		}}
		if isShell {
			shells.Rows = append(shells.Rows, row)
		} else {
			worktrees.Rows = append(worktrees.Rows, row)
		}
	}
	shells.Title, shells.Count = "Shells", len(shells.Rows)
	worktrees.Title, worktrees.Count = "Worktrees", len(worktrees.Rows)
	return RenderSidebar(SidebarOptions{
		Width: width, Height: height, Title: "Workspaces", Focused: true,
		SelectedID:   "sidecar:fix-terminal-resize",
		HeaderMeta:   &SidebarAction{ID: "sort", Label: SortPillLabel(SortManual)},
		HeaderAction: &SidebarAction{ID: "new", Label: "+"},
		Sections:     []SidebarSection{shells, worktrees},
	}).View
}

// baselineWidths are the four responsive tiers the plan names. 56 and 40 are
// two-line renderings; 26 and 18 fall below twoLineWidth once the scrollbar
// column is reserved.
var baselineWidths = []int{56, 40, 26, 18}

func TestSidebarBaselineFixture(t *testing.T) {
	const height = 18
	var out strings.Builder
	out.WriteString("Workspace sidebar baseline — captured before the redesign.\n")
	out.WriteString("Generated by TestSidebarBaselineFixture; ANSI stripped, trailing space trimmed.\n")

	for _, width := range baselineWidths {
		for _, shape := range []struct {
			name   string
			render func(int, int) string
		}{
			{"project", renderProjectShape},
			{"global", renderGlobalShape},
		} {
			out.WriteString("\n=== " + shape.name + " @ " + itoa(width) + " cols ===\n")
			for _, line := range strings.Split(ansi.Strip(shape.render(width, height)), "\n") {
				out.WriteString(strings.TrimRight(line, " ") + "\n")
			}
		}
	}
	// Keep the fixture newline-terminated without manufacturing a blank line at
	// EOF when the rendered view already ends in a newline.
	got := strings.TrimRight(out.String(), "\n") + "\n"

	golden := filepath.Join("testdata", "baseline-sidebar.txt")
	if *updateBaseline {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote " + golden)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		actual := golden + ".actual"
		_ = os.WriteFile(actual, []byte(got), 0o644)
		t.Fatalf("sidebar rendering changed; compare %s with %s and re-run with -update if the change is intended", golden, actual)
	}
}

// TestSidebarBaselineWidthInvariants pins the invariants the redesign must not
// break while it moves everything else: every line fits its allocated width,
// and the panel never renders more rows than it was given.
func TestSidebarBaselineWidthInvariants(t *testing.T) {
	const height = 18
	for _, width := range baselineWidths {
		for name, view := range map[string]string{
			"project": renderProjectShape(width, height),
			"global":  renderGlobalShape(width, height),
		} {
			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Errorf("%s @ %d: rendered %d lines, allocated %d", name, width, len(lines), height)
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("%s @ %d: line %d is %d cells wide", name, width, i, got)
				}
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
