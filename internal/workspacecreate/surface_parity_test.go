package workspacecreate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
)

// canonicalProviders is what a config with one enabled provider yields through
// both hosts' builders.
func canonicalProviders() []ProviderItem {
	return []ProviderItem{{ID: "jira-work"}}
}

// TestPaneSwitcherSurfacesStayInParity is the switcher's parity contract, the
// create action's rule carried to the grown modal: rows and pickers work
// identically from the project workspace and the global Sessions browser,
// minus exactly the HostScoped rows where no pane tree exists.
//
// Both surfaces build their form from this package, so parity lives or dies in
// two places — the catalogs their OpenOpts produce, and whether each host
// resolves targets through the core rather than growing its own path. This
// test holds both.
func TestPaneSwitcherSurfacesStayInParity(t *testing.T) {
	// The project workspace passes AllowTerminalSplit (it has a pane tree);
	// the global browser leaves it off (its preview has passive panes only).
	projectRows := kindRowsForOpts(rowOpts{hostScoped: true, showNotes: true, providers: canonicalProviders()})
	globalRows := kindRowsForOpts(rowOpts{hostScoped: false, showNotes: true, providers: canonicalProviders()})

	var placeable []kindRow
	for _, row := range projectRows {
		if !row.HostScoped {
			placeable = append(placeable, row)
		}
	}
	if len(placeable) != len(globalRows) {
		t.Fatalf("after dropping HostScoped rows the project surface offers %d rows, global %d", len(placeable), len(globalRows))
	}
	for i, want := range globalRows {
		got := placeable[i]
		if got.Kind != want.Kind || got.Label != want.Label || got.NeedsTarget != want.NeedsTarget {
			t.Fatalf("row %d differs: project %+v, global %+v", i, got, want)
		}
	}

	// Same picker data resolves to the same target on either surface's form.
	newForm := func(rows []kindRow) *Form {
		f := Open(OpenOpts{Kind: KindIssue})
		f.rows = rows
		return f
	}
	for _, rows := range [][]kindRow{projectRows, globalRows} {
		f := newForm(rows)
		f.SetIssues([]Suggestion{{Value: "td-756c34", Label: "td-756c34  fix(palette): scrollbar"}})
		f.AdvanceToTarget()
		target, err := f.TargetFor("")
		if err != nil {
			t.Fatalf("global-catalog form refused its top suggestion: %v", err)
		}
		if target.Kind != uirequest.TargetKindIssue || target.Value != "td-756c34" {
			t.Fatalf("target = %+v, want the suggested issue", target)
		}
	}

	// And neither host grew its own resolution path: each picker file must
	// resolve through Form.TargetFor and carry the placement through
	// PlacementSplit, the same vocabulary the CLI's --split uses.
	for _, host := range []struct{ name, file string }{
		{"the project workspace", "../plugins/workspace/create_picker.go"},
		{"the global Sessions browser", "../overview/create_picker.go"},
	} {
		calls := calledSelectors(t, host.file)
		for _, required := range []string{"TargetFor", "PlacementSplit"} {
			if !contains(calls, required) {
				t.Fatalf("%s (%s) never calls Form.%s — it grew its own target path",
					host.name, host.file, required)
			}
		}
	}
}

// calledSelectors lists the method names invoked in file, so a parity test can
// require that a host routes through shared code.
func calledSelectors(t *testing.T, file string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	seen := map[string]bool{}
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			seen[sel.Sel.Name] = true
		}
		return true
	})
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("%s names no calls — the parity scan read the wrong source", file)
	}
	return names
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestPickerFilesDoNotResolveTargetsThemselves guards the other direction: a
// host that starts parsing paths or ids locally has left the entry-point
// pattern, and the parity promise with it.
func TestPickerFilesDoNotResolveTargetsThemselves(t *testing.T) {
	for _, host := range []struct{ name, file string }{
		{"the project workspace", "../plugins/workspace/create_picker.go"},
		{"the global Sessions browser", "../overview/create_picker.go"},
	} {
		body := sourceOf(t, host.file)
		for _, forbidden := range []string{"filepath.Rel(", "terminallink.IssueID(", "contentlink.NoteID("} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s (%s) resolves targets itself with %s — resolve through workspacecreate instead",
					host.name, host.file, forbidden)
			}
		}
	}
}

func sourceOf(t *testing.T, file string) string {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return string(body)
}
