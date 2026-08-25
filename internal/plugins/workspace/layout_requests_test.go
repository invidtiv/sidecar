package workspace

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
)

func layoutRequestFixture(t *testing.T) (*Plugin, string) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	t.Cleanup(config.ResetTestStateDir)
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# hello\n")
	writeDocPaneFixture(t, root, "docs/notes.md", "- note\n")
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.View(p.width, p.height)
	return p, root
}

func layoutPayload(t *testing.T, mode string, panes ...uirequest.LayoutPane) uirequest.Request {
	t.Helper()
	raw, err := json.Marshal(uirequest.LayoutPayload{Mode: mode, Panes: panes})
	if err != nil {
		t.Fatal(err)
	}
	return uirequest.Request{
		ID: "layout-" + uirequest.NewRequestID(), Action: uirequest.ActionLayout,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{TmuxSession: "test-shell"},
		Payload: raw,
	}
}

func readLayoutAck(t *testing.T, req uirequest.Request) uirequest.Ack {
	t.Helper()
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
	return acks[0]
}

func encodedTree(t *testing.T, p *Plugin) string {
	t.Helper()
	raw, err := json.Marshal(p.encodePaneNode(p.paneRoot))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var threePanes = []uirequest.LayoutPane{
	{Kind: "file", Targets: []string{"README.md", "docs/notes.md"}},
	{Kind: "issue", Targets: []string{"td-1a2b3c"}},
	{Kind: "shell", Run: "echo hi", Name: "dev server"},
}

// The M3 proof's ack half: one apply of three panes answers with a versioned
// items array carrying EVERY requested pane's verdict, its landed cell, and
// the surface — beside the single overall status.
func TestLayoutApply_ThreePanesAckItemsContract(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	req := layoutPayload(t, uirequest.LayoutModeApply, threePanes...)

	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("three-pane apply emitted no command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened || ack.Reason != "" {
		t.Fatalf("status = %s reason %q", ack.Status, ack.Reason)
	}
	if ack.ItemsVersion != 1 {
		t.Fatalf("itemsVersion = %d, want 1", ack.ItemsVersion)
	}
	if len(ack.Items) != 3 {
		t.Fatalf("items = %+v, want one per requested pane", ack.Items)
	}
	wantVerdicts := []string{
		uirequest.ItemVerdictOpened,
		uirequest.ItemVerdictOpened,
		uirequest.ItemVerdictOpened,
	}
	wantCells := []string{"2.1", "2.2", "1.2"}
	for i, item := range ack.Items {
		if item.Index != i {
			t.Errorf("item %d carries index %d", i, item.Index)
		}
		if item.Verdict != wantVerdicts[i] {
			t.Errorf("item %d verdict = %q, want %q", i, item.Verdict, wantVerdicts[i])
		}
		if item.Cell != wantCells[i] {
			t.Errorf("item %d cell = %q, want %q", i, item.Cell, wantCells[i])
		}
		if item.Surface != "shell:test-shell" {
			t.Errorf("item %d surface = %q", i, item.Surface)
		}
		if item.Pane == 0 {
			t.Errorf("item %d landed nowhere", i)
		}
	}

	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil || grid.ColumnCount() != 2 || grid.RowCount(1) != 2 || grid.RowCount(2) != 2 {
		t.Fatalf("committed tree projected to %+v, want a 2x2 grid", grid)
	}
	if grid.Cell(1, 1).Kind != panelayout.Primary || grid.Cell(1, 2).Kind != panelayout.Shell ||
		grid.Cell(2, 1).Kind != panelayout.Document || grid.Cell(2, 2).Kind != panelayout.Issue {
		t.Fatalf("cell kinds wrong: %+v", grid)
	}
	doc := grid.Cell(2, 1)
	if got := p.docs[doc.ContentID]; got == nil || len(got.tabs.Items) != 2 {
		t.Fatalf("file pane did not take both targets as tabs: %+v", got)
	}
	if name := p.shellLeafTitle(); name != "dev server" {
		t.Fatalf("shell title = %q", name)
	}
}

// The other half of the ack contract: a decline still lists EVERY requested
// pane with its individual verdict as evaluated during validation, names the
// first violation at the top level, and leaves the tree byte-for-byte alone.
func TestLayoutApply_DeclineListsEveryPaneAndLeavesTreeByteIdentical(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}},
		uirequest.LayoutPane{Kind: "shell", Run: "one"},
		uirequest.LayoutPane{Kind: "shell", Run: "two"}, // second live terminal: over the cap
	)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("declined apply emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined {
		t.Fatalf("status = %s, want declined", ack.Status)
	}
	if ack.Reason != shellCapMessage {
		t.Fatalf("reason = %q, want the live-cap rule %q", ack.Reason, shellCapMessage)
	}
	if len(ack.Items) != 3 {
		t.Fatalf("items = %+v, want all three panes accounted for", ack.Items)
	}
	wantVerdicts := []string{uirequest.ItemVerdictOpened, uirequest.ItemVerdictOpened, uirequest.ItemVerdictDeclined}
	for i, item := range ack.Items {
		if item.Verdict != wantVerdicts[i] {
			t.Errorf("item %d verdict = %q, want %q", i, item.Verdict, wantVerdicts[i])
		}
		if item.Cell != "" {
			t.Errorf("item %d claims cell %q but nothing landed", i, item.Cell)
		}
	}
	if last := ack.Items[2]; last.Index != 2 || last.Reason == "" {
		t.Errorf("declined item = %+v, want its own reason", last)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("decline mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
	if p.shellLeaf() != nil {
		t.Fatal("a shell leaf appeared despite the decline")
	}
}

// Deliberate divergence from open: layout requests never queue. An off-screen
// origin shell is a decline with that reason (exit 4), not a pendingView.
func TestLayoutApply_NeverQueuesOffScreenShell(t *testing.T) {
	p, root := layoutRequestFixture(t)
	p.shells = append(p.shells, &ShellSession{
		Name: "Shell 2", TmuxName: "sidecar-sh-sidecar-2",
		Agent: &Agent{TmuxPane: "%902", OutputBuf: tty.NewOutputBuffer(20)},
	})
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeGet)
	req.Origin.TmuxSession = "sidecar-sh-sidecar-2"
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("off-screen get emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "never queued") {
		t.Fatalf("get ack = %s %q, want the never-queued decline", ack.Status, ack.Reason)
	}

	applyReq := layoutPayload(t, uirequest.LayoutModeApply, threePanes...)
	applyReq.Origin.TmuxSession = "sidecar-sh-sidecar-2"
	_ = p.handleUIRequest(applyReq)
	if len(p.pendingViews) != 0 {
		t.Fatalf("apply queued %d pending views; layout must never queue", len(p.pendingViews))
	}
	if _, ok := p.pendingViewBadge("sidecar-sh-sidecar-2"); ok {
		t.Fatal("off-screen apply left a queued badge")
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("off-screen apply changed the tree")
	}
	_ = root
}

// get answers from the focused surface's tree: grid projection with kinds,
// tabs, active tab, session, and geometry per cell — and the raw tree it
// reports decodes back into the same persisted shape the surface would save.
func TestLayoutGet_ReportsGridAndRoundTripsTree(t *testing.T) {
	p, root := layoutRequestFixture(t)
	apply := layoutPayload(t, uirequest.LayoutModeApply, threePanes...)
	if cmd := p.handleUIRequest(apply); cmd == nil {
		t.Fatal("setup apply failed")
	}

	get := layoutPayload(t, uirequest.LayoutModeGet)
	if cmd := p.handleUIRequest(get); cmd != nil {
		t.Fatal("get emitted a command")
	}
	ack := readLayoutAck(t, get)
	if ack.Status != uirequest.StatusOpened || len(ack.Layout) == 0 {
		t.Fatalf("get ack = %s layout %d bytes", ack.Status, len(ack.Layout))
	}

	var report struct {
		Version int    `json:"version"`
		Surface string `json:"surface"`
		Root    string `json:"root"`
		Grid    *struct {
			Columns []struct {
				Column int `json:"column"`
				Panes  []struct {
					Cell    string   `json:"cell"`
					Kind    string   `json:"kind"`
					Session string   `json:"session,omitempty"`
					Tabs    []string `json:"tabs,omitempty"`
					Active  int      `json:"active,omitempty"`
				} `json:"panes"`
			} `json:"columns"`
		} `json:"grid"`
		Caps struct {
			MaxColumns int `json:"maxColumns"`
			MaxRows    int `json:"maxRows"`
			LiveLeaves int `json:"liveLeaves"`
		} `json:"caps"`
		Floors map[string]struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"floors"`
		Tree json.RawMessage `json:"tree"`
	}
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatalf("report does not parse: %v\n%s", err, ack.Layout)
	}
	canonicalRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		canonicalRoot = resolved
	}
	if report.Version != 1 || report.Surface != "shell:test-shell" || report.Root != canonicalRoot {
		t.Fatalf("report header = %+v (want root %s)", report, canonicalRoot)
	}
	if report.Caps.MaxColumns != panelayout.MaxGridColumns || report.Caps.MaxRows != panelayout.MaxGridRows || report.Caps.LiveLeaves != panelayout.LiveLeafCap {
		t.Fatalf("caps = %+v", report.Caps)
	}
	if report.Floors[panelayout.KindNameFile].Height == 0 || report.Floors[panelayout.KindNamePrimary].Width == 0 {
		t.Fatalf("floors missing: %+v", report.Floors)
	}
	if report.Grid == nil || len(report.Grid.Columns) != 2 {
		t.Fatalf("grid = %+v, want two columns", report.Grid)
	}
	col1, col2 := report.Grid.Columns[0], report.Grid.Columns[1]
	if col1.Column != 1 || col2.Column != 2 || len(col1.Panes) != 2 || len(col2.Panes) != 2 {
		t.Fatalf("columns = %+v / %+v", col1, col2)
	}
	type cellView = struct {
		Cell    string   `json:"cell"`
		Kind    string   `json:"kind"`
		Session string   `json:"session,omitempty"`
		Tabs    []string `json:"tabs,omitempty"`
		Active  int      `json:"active,omitempty"`
	}
	cells := map[string]cellView{}
	for _, column := range report.Grid.Columns {
		for _, pane := range column.Panes {
			cells[pane.Cell] = pane
		}
	}
	if cells["1.1"].Kind != "primary" {
		t.Errorf("1.1 = %+v", cells["1.1"])
	}
	if cells["1.2"].Kind != "shell" || cells["1.2"].Session == "" {
		t.Errorf("1.2 = %+v, want a shell with its session", cells["1.2"])
	}
	fileCell := cells["2.1"]
	if fileCell.Kind != "file" || len(fileCell.Tabs) != 2 || fileCell.Active != 1 ||
		fileCell.Tabs[0] != "README.md" || fileCell.Tabs[1] != "docs/notes.md" {
		t.Errorf("2.1 = %+v, want both tabs with notes active", fileCell)
	}
	if cells["2.2"].Kind != "issue" || len(cells["2.2"].Tabs) != 1 || cells["2.2"].Tabs[0] != "td-1a2b3c" {
		t.Errorf("2.2 = %+v", cells["2.2"])
	}

	var saved state.PaneLayoutJSON
	if err := json.Unmarshal(report.Tree, &saved); err != nil {
		t.Fatalf("raw tree does not parse as PaneLayoutJSON: %v", err)
	}
	if saved.Root != canonicalRoot || saved.Surface != "shell:test-shell" || !saved.Open {
		t.Fatalf("raw tree header = %+v", saved)
	}
	layout := saved
	docLeaf := firstLayoutLeafOfKind(&layout, contentKindDoc)
	if docLeaf == nil || len(docLeaf.Tabs) != 2 {
		t.Fatalf("raw tree doc leaf = %+v", docLeaf)
	}
	shellLeaf := firstLayoutLeafOfKind(&layout, contentKindShell)
	if shellLeaf == nil || shellLeaf.Session == "" {
		t.Fatalf("raw tree shell leaf = %+v", shellLeaf)
	}
}

// Descriptor resolution is the CLI's own target classification, run host-side
// against the workspace root — and each resolved target must agree with the
// kind that asked for it.
func TestLayoutResolveTargets(t *testing.T) {
	p, root := layoutRequestFixture(t)
	p.SetResourceMatchers(nil)

	if targets, refusal := p.resolveLayoutTargets(panelayout.Document, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"README.md", "docs/notes.md:7"},
	}, root); refusal != "" || len(targets) != 2 ||
		targets[0].Value != "README.md" || targets[1].Value != "docs/notes.md" || targets[1].Line != 7 {
		t.Fatalf("file resolution = %+v refusal %q", targets, refusal)
	}

	wt, refusal := p.resolveLayoutTargets(panelayout.Diff, uirequest.LayoutPane{Kind: "diff"}, root)
	if refusal != "" || len(wt) != 1 || wt[0].Kind != uirequest.TargetKindDiff || wt[0].Value != "wt" {
		t.Fatalf("bare diff = %+v refusal %q, want the working tree", wt, refusal)
	}

	if _, refusal := p.resolveLayoutTargets(panelayout.Document, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"td-1a2b3c"},
	}, root); !strings.Contains(refusal, "resolves to a issue pane") {
		t.Fatalf("mismatched kind = %q, want an honest mismatch refusal", refusal)
	}

	_, refusal = p.resolveLayoutTargets(panelayout.Document, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"nope/missing.go"},
	}, root)
	if refusal == "" || !strings.Contains(refusal, "does not exist") {
		t.Fatalf("missing file = %q", refusal)
	}

	if _, refusal := p.resolveLayoutTargets(panelayout.Resource, uirequest.LayoutPane{
		Kind: "resource", Provider: "jira-work", Targets: []string{"CASH-1245"},
	}, root); refusal == "" {
		t.Fatal("resource with no configured provider was accepted")
	}
}

// Placement composition: the batch plans every pane against one shared trial
// tree, so auto placement follows the grid rule step by step and explicit
// cells resolve through PlanOpenAt's cell classes.
func TestLayoutPlanComposition_AutoAndAtCells(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	trial := &PaneNode{ID: 1, Kind: PaneTerminal}

	filePlan, refusal := p.planLayoutItem(trial, layoutItemPlan{kind: panelayout.Document}, nil)
	if refusal != "" || filePlan.Split != 1 || filePlan.Axis != panelayout.Columns {
		t.Fatalf("first open plan = %+v refusal %q, want beside the terminal", filePlan, refusal)
	}
	ApplyPanePlan(trial, filePlan, &PaneNode{Kind: panelayout.Document})
	docLeaf := panelayout.FirstOfKind(trial, panelayout.Document)

	issuePlan, refusal := p.planLayoutItem(trial, layoutItemPlan{kind: panelayout.Issue}, nil)
	if refusal != "" || issuePlan.Split != docLeaf.ID || issuePlan.Axis != panelayout.Rows {
		t.Fatalf("second open plan = %+v refusal %q, want stacked under the doc", issuePlan, refusal)
	}
	ApplyPanePlan(trial, issuePlan, &PaneNode{Kind: panelayout.Issue})

	shellPlan, refusal := p.planLayoutItem(trial, layoutItemPlan{kind: panelayout.Shell}, nil)
	grid := panelayout.GridOf(trial)
	if refusal != "" || grid == nil {
		t.Fatalf("shell plan refused %q", refusal)
	}
	if shellPlan.Split != grid.Columns[0].Node.ID || shellPlan.Axis != panelayout.Rows {
		t.Fatalf("third open plan = %+v, want the emptiest (left) column taking it", shellPlan)
	}

	fresh := &PaneNode{ID: 1, Kind: PaneTerminal}
	appendCol, refusal := p.planLayoutItem(fresh, layoutItemPlan{
		kind: panelayout.Document, cell: panelayout.Cell{Col: 2, Row: 1},
	}, nil)
	if refusal != "" || appendCol.Split != fresh.ID || appendCol.Axis != panelayout.Columns {
		t.Fatalf("one-past-end column = %+v refusal %q, want a new column off the root", appendCol, refusal)
	}

	insertAtOccupied, refusal := p.planLayoutItem(fresh, layoutItemPlan{
		kind: panelayout.Document, cell: panelayout.Cell{Col: 1, Row: 1},
	}, nil)
	if refusal != "" || insertAtOccupied.Split != 1 || insertAtOccupied.Axis != panelayout.Rows || !insertAtOccupied.NewFirst {
		t.Fatalf("occupied cell = %+v refusal %q, want insert-above on the occupant", insertAtOccupied, refusal)
	}

	if _, refusal := p.planLayoutItem(fresh, layoutItemPlan{
		kind: panelayout.Document, cell: panelayout.Cell{Col: panelayout.MaxGridColumns + 1},
	}, nil); !strings.Contains(refusal, "outside") {
		t.Fatalf("past-cap column = %q, want the cap refusal", refusal)
	}
	if _, refusal := p.planLayoutItem(fresh, layoutItemPlan{
		kind: panelayout.Document, cell: panelayout.Cell{Col: 1, Row: panelayout.MaxGridRows + 1},
	}, nil); refusal == "" {
		t.Fatal("far-out row accepted")
	}
}

// A tree whose shape escapes the columns-of-rows vocabulary still answers get:
// grid null plus the raw tree, never a fabricated projection.
func TestLayoutGet_EscapedTreeReportsNullGridWithRawTree(t *testing.T) {
	p, root := layoutRequestFixture(t)
	// A Columns split nested inside a column's row stack is exactly the shape
	// the vocabulary cannot name.
	p.paneRoot = &PaneNode{ID: 1, Split: &PaneSplit{
		Axis: SplitCols,
		A:    &PaneNode{ID: 2, Kind: PaneTerminal},
		B: &PaneNode{ID: 3, Split: &PaneSplit{
			Axis: SplitRows,
			A:    &PaneNode{ID: 4, Kind: PaneDoc, ContentID: 30},
			B: &PaneNode{ID: 5, Split: &PaneSplit{
				Axis: SplitCols,
				A:    &PaneNode{ID: 6, Kind: PaneIssue, ContentID: 31},
				B:    &PaneNode{ID: 7, Kind: PaneNote, ContentID: 32},
			}},
		}},
	}}
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	p.notes = make(map[int]*notePane)
	p.diffs = make(map[int]*diffPane)
	p.resources = make(map[int]*resourcePane)
	p.docs[30] = newDocPane(30, root, "shell:test-shell", nil)
	p.issues[31] = &issuePane{}
	p.notes[32] = &notePane{}

	get := layoutPayload(t, uirequest.LayoutModeGet)
	_ = p.handleUIRequest(get)
	ack := readLayoutAck(t, get)
	var report struct {
		Grid json.RawMessage `json:"grid"`
		Tree struct {
			Split *struct {
				Axis string `json:"axis"`
			} `json:"split"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatal(err)
	}
	if string(report.Grid) != "null" {
		t.Fatalf("grid = %s, want null for a non-grid tree", report.Grid)
	}
	if report.Tree.Split == nil || report.Tree.Split.Axis != "cols" {
		t.Fatalf("raw tree missing beside null grid: %s", ack.Layout)
	}
}
