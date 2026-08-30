package layoutapply

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
)

// moveFixture is one deliberately asymmetric layout: a Primary alone on the
// left and a stacked pair on the right. It has an inner column, both outer
// edges, a row boundary at each end, and an occupied destination cell, so one
// tree exercises every --to form.
//
//	+---------+---------+
//	|         |  doc 2  |
//	| prim 1  +---------+
//	|         |  diff 3 |
//	+---------+---------+
func moveFixture() *panelayout.Node {
	return &panelayout.Node{ID: 4, Split: &panelayout.Split{
		Axis:  panelayout.Columns,
		Ratio: 50,
		A:     &panelayout.Node{ID: 1, Kind: panelayout.Primary},
		B: &panelayout.Node{ID: 5, Split: &panelayout.Split{
			Axis:  panelayout.Rows,
			Ratio: 50,
			A:     &panelayout.Node{ID: 2, Kind: panelayout.Document},
			B:     &panelayout.Node{ID: 3, Kind: panelayout.Diff},
		}},
	}}
}

var moveTestBox = panelayout.Box{W: 200, H: 60}

func moveGridIDs(root *panelayout.Node) [][]int {
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

type moveAck struct {
	status uirequest.Status
	reason string
	items  []uirequest.AckItem
}

// moveHost is a Host that answers only what layout move asks of it. Every
// method the other two modes need is present and unused, so the fake cannot
// silently drift out of the interface.
type moveHost struct {
	root         *panelayout.Node
	focus        int
	box          panelayout.Box
	placed       bool
	floors       panelayout.Floors
	commitRefuse string
	commits      int
	acks         []moveAck
}

func newMoveHost(root *panelayout.Node) *moveHost {
	return &moveHost{root: root, box: moveTestBox, placed: true}
}

func (h *moveHost) PaneRoot() *panelayout.Node         { return h.root }
func (h *moveHost) LastBoxes() map[int]panelayout.Box  { return nil }
func (h *moveHost) PeerBox() (panelayout.Box, bool)    { return h.box, h.placed }
func (h *moveHost) Floors() panelayout.Floors          { return h.floors }
func (h *moveHost) EnsureDeck()                        {}
func (h *moveHost) DeckTree() *panelayout.Node         { return nil }
func (h *moveHost) TerminalEnabled() bool              { return true }
func (h *moveHost) TerminalOffReason() string          { return "" }
func (h *moveHost) ShellCapMessage() string            { return "" }
func (h *moveHost) ShellVisible() bool                 { return false }
func (h *moveHost) SplitOrigin() string                { return "" }
func (h *moveHost) TermPanelSessionName() string       { return "" }
func (h *moveHost) LiveShellSessions() map[string]bool { return nil }
func (h *moveHost) FocusedLeaf() int                   { return h.focus }
func (h *moveHost) AfterSpecCommit()                   {}
func (h *moveHost) LandedLeaf(panelayout.Kind) int     { return 0 }
func (h *moveHost) RestoreSpec(*state.PaneLayoutJSON) tea.Cmd {
	return nil
}
func (h *moveHost) AdoptSpecShell(uirequest.LayoutPane) (string, string, tea.Cmd) { return "", "", nil }
func (h *moveHost) ResolveTargets(panelayout.Kind, uirequest.LayoutPane) ([]uirequest.Target, string) {
	return nil, ""
}
func (h *moveHost) CommitPassive([]uirequest.Target, panelayout.OpenPlan) (string, string, tea.Cmd) {
	return "", "", nil
}
func (h *moveHost) CommitShell(uirequest.LayoutPane, panelayout.OpenPlan) (string, string, tea.Cmd) {
	return "", "", nil
}

// CommitMove is the real host contract in miniature: the shared
// identity-preserving apply, and nothing structural of its own.
func (h *moveHost) CommitMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	if h.commitRefuse != "" {
		return h.commitRefuse, nil
	}
	root, _, reason := panereposition.ApplyLive(h.root, plan)
	if reason != "" {
		return reason, nil
	}
	h.root = root
	h.commits++
	return "", nil
}

func (h *moveHost) Ack(_ uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, _ json.RawMessage) {
	h.acks = append(h.acks, moveAck{status: status, reason: reason, items: items})
}

func (h *moveHost) lastAck(t *testing.T) moveAck {
	t.Helper()
	if len(h.acks) != 1 {
		t.Fatalf("acks = %d, want exactly one", len(h.acks))
	}
	return h.acks[0]
}

func runMove(h *moveHost, move uirequest.LayoutMove) {
	Apply(h, uirequest.Request{Action: uirequest.ActionLayout}, uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeMove,
		Move: &move,
	}, "/tmp/project", "surface-under-test")
}

// modalMove is what the human does: open the shared controller on the same
// tree, press one movement key, and commit.
func modalMove(t *testing.T, root *panelayout.Node, leafID int, key string) *panelayout.Node {
	t.Helper()
	controller := panereposition.NewController("scope", root, leafID, moveTestBox, panelayout.Floors{}, false, "doc")
	if controller == nil {
		t.Fatalf("modal did not open on leaf %d", leafID)
	}
	result, _ := controller.HandleKey(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	if result.Reason != "" {
		t.Fatalf("modal refused %q: %s", key, result.Reason)
	}
	commit := controller.Commit("scope", root, moveTestBox, panelayout.Floors{})
	if commit.Reason != "" {
		t.Fatalf("modal commit of %q refused: %s", key, commit.Reason)
	}
	return commit.Root
}

// The whole point of routing the verb through panelayout.PlanMove: `layout
// move --to left` and the modal's `h` must produce the same layout from the
// same tree. Both outer edges are included because they are the placements a
// Cell address cannot name, and so the ones most likely to drift.
func TestLayoutMoveAndTheModalCompileTheSameMoveFromTheSameTree(t *testing.T) {
	for _, tc := range []struct {
		name      string
		leafID    int
		direction string
		key       string
	}{
		{name: "up within the column", leafID: 3, direction: "up", key: "k"},
		{name: "down within the column", leafID: 2, direction: "down", key: "j"},
		{name: "left into the column beside", leafID: 3, direction: "left", key: "h"},
		{name: "right into the column beside", leafID: 1, direction: "right", key: "l"},
		{name: "left off the outer edge opens a new first column", leafID: 2, direction: "left", key: "h"},
		{name: "right off the outer edge opens a new last column", leafID: 3, direction: "right", key: "l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// --focused proves the flag resolves the same pane the modal opens on.
			host := newMoveHost(moveFixture())
			host.focus = tc.leafID
			runMove(host, uirequest.LayoutMove{Focused: true, To: tc.direction})

			ack := host.lastAck(t)
			if ack.status != uirequest.StatusMoved {
				t.Fatalf("layout move --to %s = %s (%s)", tc.direction, ack.status, ack.reason)
			}
			want := moveGridIDs(modalMove(t, moveFixture(), tc.leafID, tc.key))
			if got := moveGridIDs(host.root); !reflect.DeepEqual(got, want) {
				t.Fatalf("--to %s produced %v; the modal's %q produced %v", tc.direction, got, tc.key, want)
			}
			if ack.items[0].Cell == "" || ack.items[0].Verdict != uirequest.ItemVerdictMoved {
				t.Fatalf("ack item = %+v, want a moved verdict with a landed cell", ack.items[0])
			}
			if ack.items[0].Surface != "surface-under-test" || ack.items[0].Pane != tc.leafID {
				t.Fatalf("ack item = %+v, want the moved pane and the surface it changed", ack.items[0])
			}
		})
	}
}

// The cell and column forms have no keypress, so they are held to the planner
// itself: the same PlanMove/ApplyMove pair the modal's clicked destination cell
// goes through.
func TestLayoutMoveCellAndColumnFormsMatchThePlannerDirectly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		from        string
		to          string
		destination panelayout.MoveDestination
	}{
		{name: "cell insert", from: "2.2", to: "1.1", destination: panelayout.MoveDestination{Cell: panelayout.Cell{Col: 1, Row: 1}}},
		{name: "cell append", from: "1.1", to: "2.3", destination: panelayout.MoveDestination{Cell: panelayout.Cell{Col: 2, Row: 3}}},
		{name: "column append lands at the bottom", from: "1.1", to: "2", destination: panelayout.MoveDestination{Cell: panelayout.Cell{Col: 2, Row: 3}}},
		{name: "column one past the end opens it", from: "2.2", to: "3", destination: panelayout.MoveDestination{Cell: panelayout.Cell{Col: 3, Row: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newMoveHost(moveFixture())
			runMove(host, uirequest.LayoutMove{From: tc.from, To: tc.to})
			if ack := host.lastAck(t); ack.status != uirequest.StatusMoved {
				t.Fatalf("%s = %s (%s)", tc.name, ack.status, ack.reason)
			}

			reference := moveFixture()
			source := panelayout.GridOf(reference)
			cell, _ := panelayout.ParseCell(tc.from)
			leafID := source.Cell(cell.Col, cell.Row).ID
			outcome := panelayout.PlanMove(reference, leafID, tc.destination, moveTestBox, panelayout.Floors{})
			if outcome.Status != panelayout.MoveMoved {
				t.Fatalf("planner refused the reference move: %+v", outcome)
			}
			reference, _ = panelayout.ApplyMove(reference, outcome.Plan)
			if got, want := moveGridIDs(host.root), moveGridIDs(reference); !reflect.DeepEqual(got, want) {
				t.Fatalf("--to %s produced %v, planner produced %v", tc.to, got, want)
			}
		})
	}
}

// The moved leaf is the same node, so everything the host keys by that identity
// travels with the pane instead of being rebuilt.
func TestLayoutMovePreservesTheMovedLeafIdentity(t *testing.T) {
	host := newMoveHost(moveFixture())
	before := panelayout.Find(host.root, 3)
	runMove(host, uirequest.LayoutMove{From: "2.2", To: "left"})
	if ack := host.lastAck(t); ack.status != uirequest.StatusMoved {
		t.Fatalf("move = %s (%s)", ack.status, ack.reason)
	}
	if after := panelayout.Find(host.root, 3); after != before || after.ID != 3 {
		t.Fatalf("moved leaf was rebuilt: %p -> %p", before, after)
	}
}

// A no-op is a success with its own word. It is not "moved" (nothing did) and
// not a decline (the pane is where the caller asked for it to be).
func TestLayoutMoveReportsAnAcceptedNoOpAsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		move uirequest.LayoutMove
		want string
	}{
		{name: "already at the top", move: uirequest.LayoutMove{From: "2.1", To: "up"}, want: "already at the top"},
		{name: "already at the bottom", move: uirequest.LayoutMove{From: "2.2", To: "down"}, want: "already at the bottom"},
		{name: "already in that cell", move: uirequest.LayoutMove{From: "2.1", To: "2.1"}, want: panelayout.MoveUnchangedMessage},
		{name: "alone in its column", move: uirequest.LayoutMove{From: "1.1", To: "left"}, want: panelayout.MoveUnchangedMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newMoveHost(moveFixture())
			want := moveGridIDs(host.root)
			runMove(host, tc.move)
			ack := host.lastAck(t)
			if ack.status != uirequest.StatusUnchanged || ack.reason != tc.want {
				t.Fatalf("ack = %s %q, want unchanged %q", ack.status, ack.reason, tc.want)
			}
			if ack.items[0].Verdict != uirequest.ItemVerdictUnchanged || ack.items[0].Cell == "" {
				t.Fatalf("ack item = %+v, want an unchanged verdict naming where the pane still is", ack.items[0])
			}
			if host.commits != 0 {
				t.Fatal("a no-op reached the host's commit path")
			}
			if got := moveGridIDs(host.root); !reflect.DeepEqual(got, want) {
				t.Fatalf("a no-op changed the layout: %v -> %v", want, got)
			}
		})
	}
}

// Every decline names its reason and leaves the tree exactly as it was. The
// host's own refusal — a modal open on that surface, a deck that cannot adopt
// the result — is carried verbatim rather than being restated by this package.
func TestLayoutMoveDeclinesLeaveTheLayoutUntouched(t *testing.T) {
	for _, tc := range []struct {
		name    string
		move    uirequest.LayoutMove
		prepare func(*moveHost)
		want    string
	}{
		{name: "no source named", move: uirequest.LayoutMove{To: "left"}, want: "name the pane to move"},
		{name: "both source forms", move: uirequest.LayoutMove{From: "1.1", Focused: true, To: "left"}, want: "not both"},
		{name: "destination is not a form", move: uirequest.LayoutMove{From: "1.1", To: "sideways"}, want: "is not a cell"},
		{name: "source column out of range", move: uirequest.LayoutMove{From: "3.1", To: "left"}, want: "column 3 is out of range"},
		{name: "source cell empty", move: uirequest.LayoutMove{From: "1.2", To: "left"}, want: "holds no pane"},
		{name: "destination column out of range", move: uirequest.LayoutMove{From: "1.1", To: "4"}, want: "column 4 is out of range"},
		{
			name:    "nothing focused",
			move:    uirequest.LayoutMove{Focused: true, To: "left"},
			prepare: func(h *moveHost) { h.focus = 0 },
			want:    MoveNoFocusReason,
		},
		{
			name:    "no room on screen",
			move:    uirequest.LayoutMove{From: "2.2", To: "left"},
			prepare: func(h *moveHost) { h.placed = false },
			want:    tooSmall,
		},
		{
			name:    "the host refuses",
			move:    uirequest.LayoutMove{From: "2.2", To: "left"},
			prepare: func(h *moveHost) { h.commitRefuse = "the reposition modal is open on that surface" },
			want:    "the reposition modal is open on that surface",
		},
		{
			// The two columns on screen need 196 of the 200 available; the
			// third this move would open needs more than there is. The refusal
			// is caused by the move, not by a layout that never fitted.
			name: "the result does not fit",
			move: uirequest.LayoutMove{From: "2.2", To: "3"},
			prepare: func(h *moveHost) {
				h.floors = panelayout.Floors{
					Primary: panelayout.Floor{Width: 100, Height: 5},
					Doc:     panelayout.Floor{Width: 95, Height: 5},
					Diff:    panelayout.Floor{Width: 10, Height: 5},
				}
			},
			want: panelayout.MoveFitMessage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newMoveHost(moveFixture())
			host.focus = 2
			if tc.prepare != nil {
				tc.prepare(host)
			}
			want := moveGridIDs(host.root)
			runMove(host, tc.move)
			ack := host.lastAck(t)
			if ack.status != uirequest.StatusDeclined {
				t.Fatalf("ack = %s %q, want declined", ack.status, ack.reason)
			}
			if !strings.Contains(ack.reason, tc.want) {
				t.Fatalf("decline reason %q does not say %q", ack.reason, tc.want)
			}
			if len(ack.items) != 1 || ack.items[0].Verdict != uirequest.ItemVerdictDeclined || ack.items[0].Reason != ack.reason {
				t.Fatalf("ack items = %+v, want one declined item carrying the reason", ack.items)
			}
			if got := moveGridIDs(host.root); !reflect.DeepEqual(got, want) {
				t.Fatalf("a declined move changed the layout: %v -> %v", want, got)
			}
		})
	}
}
