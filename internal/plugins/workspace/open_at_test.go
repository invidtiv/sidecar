package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
)

// layoutOpenRequest builds an ActionOpen request carrying an explicit cell.
func layoutOpenRequest(t *testing.T, target uirequest.Target, cell string) uirequest.Request {
	t.Helper()
	return uirequest.Request{
		ID: "open-" + uirequest.NewRequestID(), Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{TmuxSession: "test-shell"},
		Target:  target,
		Options: uirequest.Options{At: cell},
	}
}

func readOpenAck(t *testing.T, req uirequest.Request) uirequest.Ack {
	t.Helper()
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
	return acks[0]
}

func fileTarget(value string) uirequest.Target {
	return uirequest.Target{Kind: uirequest.TargetKindFile, Value: value}
}

// THE decision-7 divergence: --split silently no-ops onto an existing pane of
// the kind, but --at is a requirement — an open that would retarget declines
// naming the rule, and nothing moves.
func TestOpenAt_RetargetConflictIsAnErrorNotANoOp(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	if cmd := p.openDocPaneForSurface(p.ctx.WorkDir, "shell:test-shell", "README.md", 0); cmd == nil {
		t.Fatal("setup open failed")
	}
	before := encodedTree(t, p)

	req := layoutOpenRequest(t, fileTarget("docs/notes.md"), "3.1")
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("retarget-conflict open emitted a command")
	}
	ack := readOpenAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "cannot retarget") {
		t.Fatalf("ack = %s %q, want the retarget refusal", ack.Status, ack.Reason)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("refusal mutated the tree")
	}

	// The same open as a --split preference still retargets: tabs join the pane.
	req.Options.At = ""
	req.Options.Split = "right"
	req.ID = "open-" + uirequest.NewRequestID()
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("--split open emitted no command")
	}
	ack = readOpenAck(t, req)
	if ack.Status != uirequest.StatusRetargeted {
		t.Fatalf("--split ack = %s %q, want the silent retarget", ack.Status, ack.Reason)
	}
}

// An occupied cell INSERTS: the new pane takes the addressed row and everything
// below it shifts down — including when that occupant is the primary terminal.
func TestOpenAt_OccupiedCellInsertsAbovePrimary(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	req := layoutOpenRequest(t, fileTarget("docs/notes.md"), "1.1")
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("inserting open emitted no command")
	}
	ack := readOpenAck(t, req)
	if ack.Status != uirequest.StatusOpened || ack.Pane == 0 {
		t.Fatalf("ack = %s %q pane %d", ack.Status, ack.Reason, ack.Pane)
	}
	kinds := gridKinds(t, p)
	if kinds["1.1"] != panelayout.Document || kinds["1.2"] != panelayout.Primary {
		t.Fatalf("grid = %v, want notes above primary", kinds)
	}
}

// One past the column's end appends; one past the last column appends a
// column; anything further out declines untouched.
func TestOpenAt_CellClasses(t *testing.T) {
	t.Run("one-past-end row appends below", func(t *testing.T) {
		p, _ := layoutRequestFixture(t)
		req := layoutOpenRequest(t, fileTarget("README.md"), "1.2")
		if cmd := p.handleUIRequest(req); cmd == nil {
			t.Fatal("appending open emitted no command")
		}
		kinds := gridKinds(t, p)
		if kinds["1.1"] != panelayout.Primary || kinds["1.2"] != panelayout.Document {
			t.Fatalf("grid = %v, want the doc below the primary", kinds)
		}
	})

	t.Run("one-past-end column appends beside", func(t *testing.T) {
		p, _ := layoutRequestFixture(t)
		req := layoutOpenRequest(t, fileTarget("README.md"), "2.1")
		if cmd := p.handleUIRequest(req); cmd == nil {
			t.Fatal("column-appending open emitted no command")
		}
		kinds := gridKinds(t, p)
		if kinds["1.1"] != panelayout.Primary || kinds["2.1"] != panelayout.Document {
			t.Fatalf("grid = %v, want the doc in its own column", kinds)
		}
	})

	t.Run("further out of range declines byte-identical", func(t *testing.T) {
		p, _ := layoutRequestFixture(t)
		before := encodedTree(t, p)

		req := layoutOpenRequest(t, fileTarget("README.md"), "3.1")
		if cmd := p.handleUIRequest(req); cmd != nil {
			t.Fatal("out-of-range open emitted a command")
		}
		ack := readOpenAck(t, req)
		if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "out of range") {
			t.Fatalf("ack = %s %q, want the out-of-range refusal", ack.Status, ack.Reason)
		}
		if after := encodedTree(t, p); after != before {
			t.Fatal("refusal mutated the tree")
		}

		gap := layoutOpenRequest(t, fileTarget("README.md"), "1.3")
		_ = p.handleUIRequest(gap)
		ack = readOpenAck(t, gap)
		if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "out of range") {
			t.Fatalf("gap-cell ack = %s %q, want the out-of-range refusal", ack.Status, ack.Reason)
		}
	})

	t.Run("malformed cell declines", func(t *testing.T) {
		p, _ := layoutRequestFixture(t)
		before := encodedTree(t, p)
		req := layoutOpenRequest(t, fileTarget("README.md"), "2.x")
		_ = p.handleUIRequest(req)
		ack := readOpenAck(t, req)
		if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "not a grid address") {
			t.Fatalf("ack = %s %q, want the malformed-cell refusal", ack.Status, ack.Reason)
		}
		if after := encodedTree(t, p); after != before {
			t.Fatal("refusal mutated the tree")
		}
	})
}

// A queued --at survives queueing: the cell is re-planned when the shell comes
// on screen, not dropped.
func TestOpenAt_QueuedCellStillPlacesAtTheCellWhenSelected(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	p.shells = append(p.shells, &ShellSession{
		Name: "Shell 2", TmuxName: "sidecar-sh-sidecar-2",
		Agent: &Agent{TmuxPane: "%902", OutputBuf: tty.NewOutputBuffer(20)},
	})
	req := layoutOpenRequest(t, fileTarget("README.md"), "1.1")
	req.Origin.TmuxSession = "sidecar-sh-sidecar-2"

	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("off-screen open emitted a command")
	}
	ack := readOpenAck(t, req)
	if ack.Status != uirequest.StatusQueued {
		t.Fatalf("ack = %s %q, want queued", ack.Status, ack.Reason)
	}

	p.selectTopShellAt(1)
	if cmd := p.consumePendingView("sidecar-sh-sidecar-2"); cmd == nil {
		t.Fatal("queued cell open did nothing on selection")
	}
	kinds := gridKinds(t, p)
	if kinds["1.1"] != panelayout.Document || kinds["1.2"] != panelayout.Primary {
		t.Fatalf("grid = %v, want the queued cell honored after selection", kinds)
	}
}

// The moderate repro: screen [primary|shell] with the shell owning column 2.
// An append past it is achievable, and the deck-side cell renumbers past the
// shell-only column instead of passing through untranslated.
func TestOpenAt_AppendsPastShellOnlyColumn(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	showTermPanel(t, p, SplitCols, 50)
	p.View(p.width, p.height)
	if p.shellLeaf() == nil {
		t.Fatal("fixture failed to open a shell leaf")
	}

	req := layoutOpenRequest(t, uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"}, "3.1")
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("append open emitted no command")
	}
	ack := readOpenAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("ack = %s %q", ack.Status, ack.Reason)
	}
	kinds := gridKinds(t, p)
	want := map[string]panelayout.Kind{
		"1.1": panelayout.Primary,
		"2.1": panelayout.Shell,
		"3.1": panelayout.Diff,
	}
	if len(kinds) != len(want) {
		t.Fatalf("grid = %+v, want %v", kinds, want)
	}
	for cell, kind := range want {
		if kinds[cell] != kind {
			t.Errorf("cell %s = %v, want %v", cell, kinds[cell], kind)
		}
	}
}

// When the deck side cannot honor a translated cell, its refusal reaches the
// ack verbatim — not re-guessed into a row-cap message.
func TestOpenAt_DeckRefusalSurfacesVerbatim(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	showTermPanel(t, p, SplitCols, 50)
	p.View(p.width, p.height)

	// A deck tree that escapes the grid vocabulary has no cells at all; the
	// planner's own "does not resolve to grid columns" answer must survive.
	escaped := &PaneNode{ID: 1, Split: &PaneSplit{Axis: SplitRows,
		A: &PaneNode{ID: 2, Kind: PaneTerminal},
		B: &PaneNode{ID: 3, Split: &PaneSplit{Axis: SplitCols,
			A: &PaneNode{ID: 4, Kind: PaneIssue},
			B: &PaneNode{ID: 5, Kind: PaneNote},
		}},
	}}
	item := layoutItemPlan{
		Spec: uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}},
		Kind: panelayout.Document, Cell: panelayout.Cell{Col: 3, Row: 1},
	}
	_, refusal := p.planPassiveItem(p.paneRoot, escaped, item, nil)
	if !strings.Contains(refusal, "does not resolve to grid columns") {
		t.Fatalf("refusal = %q, want the planner's own words", refusal)
	}
}
