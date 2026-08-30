package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
)

func layoutMoveRequest(t *testing.T, move uirequest.LayoutMove) uirequest.Request {
	t.Helper()
	raw, err := json.Marshal(uirequest.LayoutPayload{Mode: uirequest.LayoutModeMove, Move: &move})
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

func moveGrid(root *panelayout.Node) [][]int {
	grid := panelayout.GridOf(root)
	if grid == nil {
		return nil
	}
	out := make([][]int, grid.ColumnCount())
	for col := 1; col <= grid.ColumnCount(); col++ {
		for row := 1; row <= grid.RowCount(col); row++ {
			out[col-1] = append(out[col-1], grid.Cell(col, row).ID)
		}
	}
	return out
}

// layoutMoveFixture opens a document beside the project's primary terminal, so
// there are two panes and one of them can move.
func layoutMoveFixture(t *testing.T) (*Plugin, *panelayout.Node) {
	t.Helper()
	p, _ := layoutRequestFixture(t)
	req := layoutPayload(t, uirequest.LayoutModeApply, uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}})
	_ = p.applyLayoutRequest(req)
	if ack := readLayoutAck(t, req); ack.Status != uirequest.StatusOpened {
		t.Fatalf("fixture apply = %s %q", ack.Status, ack.Reason)
	}
	p.View(p.width, p.height)
	doc := panelayout.FirstOfKind(p.paneRoot, panelayout.Document)
	if doc == nil || len(moveGrid(p.paneRoot)) != 2 {
		t.Fatalf("fixture grid = %v, want a document beside the primary", moveGrid(p.paneRoot))
	}
	return p, doc
}

// The project surface answers layout move with the shared planner, keeps the
// moved leaf's identity, and names the pane and surface it changed.
func TestWorkspaceLayoutMove_MovesTheLeafAndKeepsItsIdentity(t *testing.T) {
	p, doc := layoutMoveFixture(t)
	req := layoutMoveRequest(t, uirequest.LayoutMove{From: "2.1", To: "left"})
	_ = p.applyLayoutRequest(req)

	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusMoved {
		t.Fatalf("move = %s %q", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictMoved ||
		ack.Items[0].Cell != "1.2" || ack.Items[0].Pane != doc.ID || ack.Items[0].Surface == "" {
		t.Fatalf("ack items = %+v, want the moved pane, its landed cell, and its surface", ack.Items)
	}
	if got := moveGrid(p.paneRoot); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("grid after the move = %v, want one column of two", got)
	}
	if panelayout.Find(p.paneRoot, doc.ID) != doc {
		t.Fatal("the move rebuilt the document leaf instead of grafting it")
	}
	if p.contentDeck != nil && len(moveGrid(p.contentDeck.Tree())) != 1 {
		t.Fatalf("the content deck did not adopt the move: %v", moveGrid(p.contentDeck.Tree()))
	}
}

// --focused resolves the pane M would open the modal on, so the key and the
// flag cannot mean two different panes.
func TestWorkspaceLayoutMoveFocusedMatchesTheKeyboardTarget(t *testing.T) {
	p, doc := layoutMoveFixture(t)
	p.activePane = PanePreview
	p.paneFocus = doc.ID
	if got := p.layoutMoveFocusedLeaf(); got != doc.ID {
		t.Fatalf("--focused resolved leaf %d, want the focused document %d", got, doc.ID)
	}
	p.activePane = PaneSidebar
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if got := p.layoutMoveFocusedLeaf(); primary != nil && got != primary.ID {
		t.Fatalf("--focused from the list resolved leaf %d, want the Primary terminal %d", got, primary.ID)
	}
}

// A human's open draft owns the surface: an agent move underneath it would
// invalidate the draft without saying so, and is declined instead.
func TestWorkspaceLayoutMove_DeclinesWhileTheModalHasADraft(t *testing.T) {
	p, doc := layoutMoveFixture(t)
	before := moveGrid(p.paneRoot)
	_ = p.openPaneLayoutModal(doc.ID)
	if p.paneLayoutModal == nil {
		t.Fatal("fixture did not open the reposition modal")
	}

	req := layoutMoveRequest(t, uirequest.LayoutMove{From: "2.1", To: "left"})
	if cmd := p.applyLayoutRequest(req); cmd != nil {
		t.Fatal("a declined move emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || ack.Reason != LayoutMoveModalOpenReason {
		t.Fatalf("ack = %s %q, want the modal-open decline", ack.Status, ack.Reason)
	}
	if got := moveGrid(p.paneRoot); len(got) != len(before) {
		t.Fatalf("the declined move changed the layout: %v -> %v", before, got)
	}
	if reason := panereposition.Reason(ack.Reason); reason != ack.Reason {
		t.Fatalf("the decline has no user-visible wording: %q", reason)
	}
}

// A no-op is a success with its own word, and it leaves the tree byte-identical.
func TestWorkspaceLayoutMove_UnchangedLeavesTheTreeByteIdentical(t *testing.T) {
	p, _ := layoutMoveFixture(t)
	before := encodedTree(t, p)
	req := layoutMoveRequest(t, uirequest.LayoutMove{From: "2.1", To: "2.1"})
	_ = p.applyLayoutRequest(req)
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusUnchanged || ack.Reason != panelayout.MoveUnchangedMessage {
		t.Fatalf("ack = %s %q, want the unchanged no-op", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictUnchanged || ack.Items[0].Cell != "2.1" {
		t.Fatalf("ack items = %+v, want an unchanged verdict naming where the pane still is", ack.Items)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("a no-op rewrote the tree:\n%s\n%s", before, after)
	}
}

// Off screen, a move declines exactly as get and apply do. Layout never queues:
// a stale answer is worse than a refusal.
func TestWorkspaceLayoutMove_NeverQueuesOffScreen(t *testing.T) {
	p, _ := layoutMoveFixture(t)
	p.shells = append(p.shells, &ShellSession{
		Name: "Shell 2", TmuxName: "sidecar-sh-sidecar-2",
		Agent: &Agent{TmuxPane: "%902", OutputBuf: tty.NewOutputBuffer(20)},
	})
	before := encodedTree(t, p)

	req := layoutMoveRequest(t, uirequest.LayoutMove{Focused: true, To: "left"})
	req.Origin.TmuxSession = "sidecar-sh-sidecar-2"
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("an off-screen move emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "never queued") {
		t.Fatalf("ack = %s %q, want the never-queued decline", ack.Status, ack.Reason)
	}
	if len(p.pendingViews) != 0 {
		t.Fatalf("an off-screen move queued %d pending views", len(p.pendingViews))
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("an off-screen move changed the tree")
	}
}
