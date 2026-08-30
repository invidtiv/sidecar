package overview

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func sessionsMovePayload(t *testing.T, move uirequest.LayoutMove) uirequest.Request {
	t.Helper()
	raw, err := json.Marshal(uirequest.LayoutPayload{Mode: uirequest.LayoutModeMove, Move: &move})
	if err != nil {
		t.Fatal(err)
	}
	return uirequest.Request{
		ID: "layout-" + uirequest.NewRequestID(), Action: uirequest.ActionLayout,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{Sessions: true},
		Payload: raw,
	}
}

func sessionsGridIDs(root *panelayout.Node) [][]int {
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

// The Sessions surface moves a pane the same way the project surface does, and
// its acknowledgement names the row it changed rather than "the layout".
func TestSessionsLayoutMove_ChangesTheViewerTreeAndNamesTheRow(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	m.WorkspacesView(previewWide, previewTall)
	before := sessionsGridIDs(m.preview.paneRoot)
	if len(before) != 2 {
		t.Fatalf("fixture grid = %v, want a primary beside a document", before)
	}
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	if doc == nil {
		t.Fatal("fixture opened no document pane")
	}

	req := sessionsMovePayload(t, uirequest.LayoutMove{From: "2.1", To: "left"})
	_ = m.handleUIRequest(req)
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusMoved {
		t.Fatalf("move = %s %q", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 1 || ack.Items[0].Verdict != uirequest.ItemVerdictMoved || ack.Items[0].Surface != "a" {
		t.Fatalf("ack items = %+v, want one moved item naming row a", ack.Items)
	}
	if ack.Items[0].Cell != "1.2" {
		t.Fatalf("landed cell = %q, want 1.2", ack.Items[0].Cell)
	}
	after := sessionsGridIDs(m.preview.paneRoot)
	if len(after) != 1 || len(after[0]) != 2 {
		t.Fatalf("grid after the move = %v, want one column of two", after)
	}
	if panelayout.Find(m.preview.paneRoot, doc.ID) != doc {
		t.Fatal("the Sessions move rebuilt the document leaf instead of grafting it")
	}
}

// The keyboard and the verb resolve the same pane: `layout move --focused`
// targets the leaf M would open the modal on.
func TestSessionsLayoutMoveFocusedMatchesTheKeyboardTarget(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	m.WorkspacesView(previewWide, previewTall)
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	m.preview.paneFocus = doc.ID
	m.preview.focus = focusPreview
	if got, want := m.layoutMoveFocusedLeaf(), m.paneLayoutShortcutLeaf(); want != 0 && got != want {
		t.Fatalf("--focused resolves leaf %d, M resolves %d", got, want)
	}
}

// remoteSessionsRow adds a workspace that lives on another machine, so row
// resolution can be held to the host-scoped rule.
func remoteSessionsRow(t *testing.T, m *Model) workspaceinventory.Workspace {
	t.Helper()
	result := m.results["sidecar"]
	remote := workspaceinventory.Workspace{
		ID: "mac-mini\x1fapi:worktree:remote", HostID: "mac-mini", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindWorktree, Name: "ghost", Branch: "ghost-branch",
		TmuxName: "sc-ghost", Path: "/home/me/api",
	}
	result.Workspaces = append(result.Workspaces, remote)
	m.results["sidecar"] = result
	m.syncBoard()
	return remote
}

// An explicit remote row ID IS host-scoped, so it resolves — and what it moves
// is this machine's viewer tree for that row. No layout mutation is sent to the
// machine the workspace lives on: the whole commit path is the local preview's.
func TestSessionsLayoutMove_ExplicitRemoteRowMovesOnlyTheLocalViewer(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	remote := remoteSessionsRow(t, m)
	if !remote.Remote() {
		t.Fatal("fixture row is not remote")
	}
	if !m.workspaces.SelectID(remote.ID) {
		t.Fatalf("fixture could not select the remote row %q", remote.ID)
	}
	run(t, m, m.bindPreview(false))
	m.WorkspacesView(previewWide, previewTall)
	// A remote row's content panes are deliberately not opened from this
	// machine's filesystem, so the second pane here is a terminal leaf: the
	// live kind whose geometry a move must not assert on the other machine.
	shell := &panelayout.Node{ID: 2, Kind: panelayout.Shell}
	m.preview.paneRoot = &panelayout.Node{ID: 3, Split: &panelayout.Split{
		Axis: panelayout.Columns, Ratio: 50,
		A: &panelayout.Node{ID: 1, Kind: panelayout.Primary},
		B: shell,
	}}
	m.preview.paneNextID = 4
	m.WorkspacesView(previewWide, previewTall)
	if got := sessionsGridIDs(m.preview.paneRoot); len(got) != 2 {
		t.Fatalf("remote fixture grid = %v, want two columns", got)
	}

	req := sessionsMovePayload(t, uirequest.LayoutMove{From: "2.1", To: "left"})
	req.Origin.SessionsRow = remote.ID
	cmd := m.handleUIRequest(req)
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusMoved {
		t.Fatalf("explicit remote row move = %s %q", ack.Status, ack.Reason)
	}
	if ack.Items[0].Surface != remote.ID {
		t.Fatalf("ack surface = %q, want the host-scoped remote row ID", ack.Items[0].Surface)
	}
	if got := sessionsGridIDs(m.preview.paneRoot); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("the local viewer tree = %v, want the moved single column", got)
	}
	if panelayout.Find(m.preview.paneRoot, shell.ID) != shell {
		t.Fatal("the remote row's live leaf was rebuilt rather than grafted")
	}
	// Nothing was scheduled: the commit path is the local preview's, so there
	// is no remote layout mutation and no remote resize to run.
	if cmd != nil {
		t.Fatal("a browse-state remote move scheduled work")
	}
}

// The local-only name fallback must not bind a row on another machine: a
// display or session name is unique per machine, and an unordered catalog walk
// would otherwise pick a remote row at random for an ambiguous local name.
func TestSessionsLayoutMove_AmbiguousLocalNameCannotBindARemoteRow(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	remote := remoteSessionsRow(t, m)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	m.WorkspacesView(previewWide, previewTall)
	before := sessionsGridIDs(m.preview.paneRoot)

	for _, name := range []string{remote.Name, remote.TmuxName} {
		req := sessionsMovePayload(t, uirequest.LayoutMove{From: "2.1", To: "left"})
		req.Origin.SessionsRow = name
		_ = m.handleUIRequest(req)
		ack := readSessionsLayoutAck(t, req)
		if ack.Status != uirequest.StatusDeclined {
			t.Fatalf("%q bound a remote row: %s %q", name, ack.Status, ack.Reason)
		}
		if got := sessionsGridIDs(m.preview.paneRoot); len(got) != len(before) {
			t.Fatalf("%q changed the layout before declining: %v -> %v", name, before, got)
		}
	}
}

// The human's draft owns the surface while it is open. An agent move landing
// underneath it would invalidate that draft silently, so it is declined with a
// reason that says what to do.
func TestSessionsLayoutMove_DeclinesWhileTheModalHasADraft(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	m.WorkspacesView(previewWide, previewTall)
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	before := sessionsGridIDs(m.preview.paneRoot)
	run(t, m, m.openPaneLayoutModal(doc.ID))
	if m.paneLayoutModal == nil {
		t.Fatal("fixture did not open the reposition modal")
	}

	req := sessionsMovePayload(t, uirequest.LayoutMove{From: "2.1", To: "left"})
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("a declined move emitted a command")
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || ack.Reason != LayoutMoveModalOpenReason {
		t.Fatalf("ack = %s %q, want the modal-open decline", ack.Status, ack.Reason)
	}
	if got := sessionsGridIDs(m.preview.paneRoot); len(got) != len(before) {
		t.Fatalf("the declined move changed the layout: %v -> %v", before, got)
	}
}

// Off screen, a move declines like get and apply. Layout never queues.
func TestSessionsLayoutMove_OffScreenDeclines(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.SetWorkspacesVisible(false))
	req := sessionsMovePayload(t, uirequest.LayoutMove{Focused: true, To: "left"})
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("off-screen move emitted a command")
	}
	if ack := readSessionsLayoutAck(t, req); ack.Status != uirequest.StatusDeclined {
		t.Fatalf("ack = %s %q, want the off-screen decline", ack.Status, ack.Reason)
	}
}
