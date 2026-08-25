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
		if item.Reason == "" {
			t.Errorf("item %d carries no reason; would-have-opened panes must say the batch declined", i)
		}
	}
	if !strings.Contains(ack.Items[0].Reason, "would have opened") {
		t.Errorf("item 0 reason = %q, want the not-applied explanation", ack.Items[0].Reason)
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

// THE repro from review: an at-cell is a requirement. On a bare primary tree,
// issue at 1.1 must INSERT above the primary (which shifts down to 1.2), not
// auto-place beside it at 2.1.
func TestLayoutApply_AtCellInsertsAbovePrimary(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-1a2b3c"}, At: "1.1"},
	)
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("at-cell apply emitted no command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("status = %s reason %q", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictOpened || ack.Items[0].Cell != "1.1" {
		t.Fatalf("items = %+v, want the issue opened AT 1.1", ack.Items)
	}

	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil || grid.ColumnCount() != 1 || grid.RowCount(1) != 2 {
		t.Fatalf("tree projected to %+v, want one column of two", grid)
	}
	if grid.Cell(1, 1).Kind != panelayout.Issue || grid.Cell(1, 2).Kind != panelayout.Primary {
		t.Fatalf("cells = %v/%v, want issue above primary", grid.Cell(1, 1).Kind, grid.Cell(1, 2).Kind)
	}
}

// The other repro: a cell past the layout's live range is a refused
// requirement — exit-4 decline with the planner's own reason and a tree that
// never changed.
func TestLayoutApply_OutOfRangeCellDeclines(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-1a2b3c"}, At: "3.1"},
	)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("out-of-range cell emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "out of range") {
		t.Fatalf("ack = %s %q, want the out-of-range refusal", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictDeclined || ack.Items[0].Reason == "" {
		t.Fatalf("items = %+v, want the pane's own declined verdict", ack.Items)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("refusal mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
}

// Composed trial == committed tree: every at-cell in one batch lands exactly
// where addressed, including a shell planned into the terminal column.
func TestLayoutApply_CellBatchComposesExactlyAsPlanned(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}, At: "2.1"},
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-1a2b3c"}, At: "2.2"},
		uirequest.LayoutPane{Kind: "shell", Run: "echo hi", Name: "dev server", At: "1.2"},
	)
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("cell batch emitted no command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("status = %s reason %q", ack.Status, ack.Reason)
	}
	wantCells := []string{"2.1", "2.2", "1.2"}
	for i, item := range ack.Items {
		if item.Verdict != uirequest.ItemVerdictOpened || item.Cell != wantCells[i] {
			t.Errorf("item %d = %s@%s, want opened@%s", i, item.Verdict, item.Cell, wantCells[i])
		}
	}

	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil || grid.ColumnCount() != 2 || grid.RowCount(1) != 2 || grid.RowCount(2) != 2 {
		t.Fatalf("committed tree projected to %+v, want the planned 2x2", grid)
	}
	kinds := map[[2]int]panelayout.Kind{
		{1, 1}: panelayout.Primary, {1, 2}: panelayout.Shell,
		{2, 1}: panelayout.Document, {2, 2}: panelayout.Issue,
	}
	for rc, want := range kinds {
		if got := grid.Cell(rc[0], rc[1]); got == nil || got.Kind != want {
			t.Errorf("cell %d.%d = %+v, want %v", rc[0], rc[1], got, want)
		}
	}
}

// THE reviewer repro: [shell auto, file@2.2] on a bare primary tree. The
// shell's column has no deck-side existence, and committing the shell before
// an addressed pane lets the projection re-home the terminal split — so the
// positional promise at 2.2 cannot be kept. "at" is a requirement: the whole
// batch declines instead of silently opening a new column.
func TestLayoutApply_ShellBeforeAtCellDeclinesInsteadOfMislanding(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "shell", Run: "echo hi", Name: "dev server"},
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}, At: "2.2"},
	)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("mis-landing batch emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined {
		t.Fatalf("status = %s reason %q, want declined", ack.Status, ack.Reason)
	}
	if !strings.Contains(ack.Reason, "live terminal") {
		t.Errorf("reason = %q, want the live-terminal explanation", ack.Reason)
	}
	if len(ack.Items) != 2 {
		t.Fatalf("items = %+v", ack.Items)
	}
	if ack.Items[0].Verdict != uirequest.ItemVerdictOpened || !strings.Contains(ack.Items[0].Reason, "would have opened") {
		t.Errorf("shell item = %+v, want would-have-opened with a reason", ack.Items[0])
	}
	if ack.Items[1].Verdict != uirequest.ItemVerdictDeclined || !strings.Contains(ack.Items[1].Reason, "live terminal") {
		t.Errorf("file item = %+v, want declined with its own reason", ack.Items[1])
	}
	for _, item := range ack.Items {
		if item.Pane != 0 || item.Cell != "" {
			t.Errorf("declined batch reported a landing for item %d: %+v", item.Index, item)
		}
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("decline mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
}

// A shell left in its own column by a PRIOR session (no batch shell item)
// must not give at-cells below it a phantom deck address: they decline with
// the own-column refusal, tree untouched.
func TestLayoutApply_PriorSessionShellColumnDeclinesBelowCell(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	showTermPanel(t, p, SplitCols, 50)
	p.View(p.width, p.height)
	if p.shellLeaf() == nil {
		t.Fatal("fixture failed to open a shell leaf")
	}
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}, At: "2.2"},
	)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("phantom-column cell emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "live terminal") {
		t.Fatalf("ack = %s %q, want the own-column refusal", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictDeclined || ack.Items[0].Cell != "" {
		t.Fatalf("items = %+v, want one declined item with no landing", ack.Items)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("decline mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
}

// Ack cells and panes must agree with what `layout get` (GridOf over the final
// tree) reports for every terminal state — not with ids captured mid-batch
// before later reconciles recycled them.
func TestLayoutApply_AckCellsAgreeWithFinalGrid(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}},
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-1a2b3c"}, At: "2.2"},
		uirequest.LayoutPane{Kind: "shell", Run: "echo hi", Name: "dev server"},
	)
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("batch emitted no command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("status = %s reason %q", ack.Status, ack.Reason)
	}

	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil {
		t.Fatal("final tree escaped the grid vocabulary")
	}
	finalCell := func(leafID int) string {
		for c, column := range grid.Columns {
			for r, leaf := range column.Cells {
				if leaf.ID == leafID {
					return panelayout.Cell{Col: c + 1, Row: r + 1}.String()
				}
			}
		}
		return ""
	}
	kinds := []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Shell}
	for i, item := range ack.Items {
		if item.Verdict == uirequest.ItemVerdictDeclined {
			continue
		}
		var leafID int
		if kinds[i] == panelayout.Shell {
			if leaf := p.shellLeaf(); leaf != nil {
				leafID = leaf.ID
			}
		} else if leaf := panelayout.FirstOfKind(p.paneRoot, kinds[i]); leaf != nil {
			leafID = leaf.ID
		}
		if leafID == 0 {
			t.Fatalf("item %d (%v) has no leaf in the final tree", i, kinds[i])
		}
		if item.Pane != leafID || item.Cell != finalCell(leafID) {
			t.Errorf("item %d ack = pane %d @ %s, but final grid says pane %d @ %s",
				i, item.Pane, item.Cell, leafID, finalCell(leafID))
		}
	}

	// The composed shape itself: the auto shell takes the emptiest left
	// column, so the batch lands as a 2x2.
	if grid.ColumnCount() != 2 || grid.RowCount(1) != 2 || grid.RowCount(2) != 2 {
		t.Fatalf("final grid = %dx%d/%dx%d, want 2 columns of 2/2",
			grid.ColumnCount(), grid.RowCount(1), grid.ColumnCount(), grid.RowCount(2))
	}
	if grid.Cell(1, 1).Kind != panelayout.Primary || grid.Cell(1, 2).Kind != panelayout.Shell ||
		grid.Cell(2, 1).Kind != panelayout.Document || grid.Cell(2, 2).Kind != panelayout.Issue {
		t.Fatalf("cell kinds wrong: 1.1=%v 1.2=%v 2.1=%v 2.2=%v",
			grid.Cell(1, 1).Kind, grid.Cell(1, 2).Kind, grid.Cell(2, 1).Kind, grid.Cell(2, 2).Kind)
	}
}

// A cell addressed at the live terminal's own row cannot be honored by a
// content pane and is declined before anything opens.
func TestLayoutApply_CellOnShellRowDeclines(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	// Stack the split BELOW the primary on the explicit axis, so screen cell
	// 1.2 is the live terminal's own row.
	showTermPanel(t, p, SplitRows, 50)
	p.View(p.width, p.height)
	if p.shellLeaf() == nil {
		t.Fatal("fixture failed to open a shell leaf")
	}
	before := encodedTree(t, p)

	req := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}, At: "1.2"},
	)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("shell-row cell emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "live terminal") {
		t.Fatalf("ack = %s %q, want the shell-row refusal", ack.Status, ack.Reason)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("decline mutated the tree")
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

// The get report's resource pane must be speakable back as a spec pane. The
// spec grammar REQUIRES "provider" there, so a report that prints only the
// locator makes `layout get | edit | layout apply --spec` fail for exactly one
// kind — the round trip the contract promises without translation.
func TestLayoutReportResourcePaneRoundTripsAsASpecPane(t *testing.T) {
	reported := layoutPaneJSON{
		Cell:     "2.1",
		Kind:     panelayout.KindNameResource,
		Pane:     4,
		Provider: "jira-work",
		Tabs:     []string{"CASH-1245", "CASH-1300"},
	}
	raw, err := json.Marshal(reported)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"provider":"jira-work"`) {
		t.Fatalf("get would print %s, with no provider for a spec to carry", raw)
	}

	// What an agent does with that answer: kind + provider + tabs-as-targets.
	spec := uirequest.LayoutSpec{Columns: []uirequest.LayoutSpecColumn{
		{Panes: []uirequest.LayoutPane{{Kind: panelayout.KindNamePrimary}}},
		{Panes: []uirequest.LayoutPane{{
			Kind:     reported.Kind,
			Provider: reported.Provider,
			Targets:  reported.Tabs,
		}}},
	}}
	if err := uirequest.ValidateLayoutSpec(spec); err != nil {
		t.Fatalf("a resource pane as get prints it is not a valid spec pane: %v", err)
	}
}
