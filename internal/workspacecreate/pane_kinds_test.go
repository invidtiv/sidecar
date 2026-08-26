package workspacecreate

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func kindsOf(rows []kindRow) []Kind {
	kinds := make([]Kind, 0, len(rows))
	for _, row := range rows {
		kinds = append(kinds, row.Kind)
	}
	return kinds
}

func hasKind(rows []kindRow, kind Kind) bool {
	for _, row := range rows {
		if row.Kind == kind {
			return true
		}
	}
	return false
}

// A host with no workspace list drops exactly the two rows that create one.
// Everything else the catalog offers is a pane, and a pane host can place it.
func TestPaneKindsOnlyDropsShellAndWorktreeAlone(t *testing.T) {
	providers := []ProviderItem{{ID: "jira"}, {ID: "linear"}}
	full := kindRowsForOpts(rowOpts{allowTerminalSplit: true, showNotes: true, providers: providers})
	panes := kindRowsForOpts(rowOpts{allowTerminalSplit: true, showNotes: true, paneKindsOnly: true, providers: providers})

	if hasKind(panes, KindShell) || hasKind(panes, KindWorktree) {
		t.Fatalf("PaneKindsOnly kept a workspace row: %v", kindsOf(panes))
	}
	for _, kind := range []Kind{KindFile, KindDiff, KindIssue, KindNote, KindResource} {
		if !hasKind(panes, kind) {
			t.Errorf("PaneKindsOnly dropped pane kind %v", kind)
		}
	}
	if got, want := len(panes), len(full)-2; got != want {
		t.Fatalf("PaneKindsOnly kept %d rows, want %d (full %v, panes %v)", got, want, kindsOf(full), kindsOf(panes))
	}
	// Both provider instances survive as their own rows: they share a Kind, so
	// a count that only checked kinds would miss losing one.
	instances := 0
	for _, row := range panes {
		if row.Kind == KindResource {
			instances++
		}
	}
	if instances != len(providers) {
		t.Fatalf("PaneKindsOnly kept %d provider rows, want %d", instances, len(providers))
	}
}

// The plugin host is PaneKindsOnly plus AllowTerminalSplit:false — its deck has
// no live-leaf host, so a terminal row would promise a pane it cannot place.
func TestPaneKindsOnlyWithoutTerminalSplitLeavesNoTerminalRow(t *testing.T) {
	rows := kindRowsForOpts(rowOpts{paneKindsOnly: true, showNotes: true})
	if hasKind(rows, KindTerminalSplit) {
		t.Fatalf("terminal split offered without AllowTerminalSplit: %v", kindsOf(rows))
	}
	f := Open(OpenOpts{Kind: KindFile, PaneKindsOnly: true, ShowNotes: true})
	if f.Kind() != KindFile {
		t.Fatalf("kind = %v, want File", f.Kind())
	}
	f.SetKind(KindTerminalSplit)
	if f.Kind() == KindTerminalSplit {
		t.Fatal("a host with no terminal row selected one anyway")
	}
}

// The heading names the act the host can actually perform. Found in M2's proof
// run: the plugin switcher opened over the td board titled "Create Workspace",
// on a list with no Shell and no Worktree row in it.
func TestPaneKindsOnlyTitlesTheModalForAPaneHost(t *testing.T) {
	// Rendered rather than asserted off the helper: what matters is that the
	// heading a user reads changed, and the kind step is built in two places.
	rendered := func(f *Form) string {
		f.Build(52)
		return ansi.Strip(f.Modal().Render(120, 40, mouse.NewHandler()))
	}

	panes := rendered(Open(OpenOpts{Kind: KindFile, PaneKindsOnly: true, ShowNotes: true}))
	if !strings.Contains(panes, "Open Pane") || strings.Contains(panes, "Create Workspace") {
		t.Errorf("pane host modal is not titled Open Pane:\n%s", panes)
	}

	// The Workspaces surfaces are unchanged: their list still starts with the
	// rows that create a workspace, and the modal still says so.
	workspaces := rendered(Open(OpenOpts{Kind: KindShell, ShowNotes: true}))
	if !strings.Contains(workspaces, "Create Workspace") {
		t.Errorf("workspace host modal lost its title:\n%s", workspaces)
	}
}

// A remembered row this host cannot offer falls back to one it can — and must
// not overwrite the shared memory while doing so, or opening the plugin
// switcher would silently move the Workspaces list off Shell.
func TestPaneKindsOnlyFallsBackWithoutClobberingLastKind(t *testing.T) {
	lastKind = KindShell
	t.Cleanup(func() { lastKind = KindShell })

	f := Open(OpenOpts{Kind: KindShell, UseLastKind: true, PaneKindsOnly: true, ShowNotes: true})
	if f.Kind() == KindShell || f.Kind() == KindWorktree {
		t.Fatalf("kind = %v, want a pane row", f.Kind())
	}
	if f.Kind() != f.rows[0].Kind {
		t.Fatalf("kind = %v, want the first offered row %v", f.Kind(), f.rows[0].Kind)
	}
	if lastKind != KindShell {
		t.Fatalf("lastKind = %v after a fallback open, want the untouched %v", lastKind, KindShell)
	}

	// A row the user actually picks is remembered, exactly as on the other hosts.
	f.SetKind(KindIssue)
	if lastKind != KindIssue {
		t.Fatalf("lastKind = %v after selecting Issue, want it recorded", lastKind)
	}
	// And a host that offers the remembered row still opens on it.
	reopened := Open(OpenOpts{Kind: KindFile, UseLastKind: true, PaneKindsOnly: true, ShowNotes: true})
	if reopened.Kind() != KindIssue {
		t.Fatalf("reopened on %v, want the remembered row", reopened.Kind())
	}
}
