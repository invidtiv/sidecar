package panereposition

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
)

func repositionLeaf(id int, kind panelayout.Kind) *panelayout.Node {
	return &panelayout.Node{ID: id, Kind: kind}
}

func repositionSplit(id int, axis panelayout.Axis, a, b *panelayout.Node) *panelayout.Node {
	return &panelayout.Node{ID: id, Split: &panelayout.Split{Axis: axis, Ratio: 50, A: a, B: b}}
}

func repositionTree() *panelayout.Node {
	return repositionSplit(7, panelayout.Columns,
		repositionSplit(5, panelayout.Rows, repositionLeaf(1, panelayout.Primary), repositionLeaf(2, panelayout.Document)),
		repositionSplit(6, panelayout.Rows, repositionLeaf(3, panelayout.Issue), repositionLeaf(4, panelayout.Diff)),
	)
}

func repositionFloors() panelayout.Floors {
	f := panelayout.Floor{Width: 4, Height: 2}
	return panelayout.Floors{Primary: f, Doc: f, Issue: f, Diff: f}
}

func key(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(text[0]), Text: text} }

func TestControllerCancelKeepsTheLiveTreeAndCommitReusesTheLiveLeaf(t *testing.T) {
	live := repositionTree()
	before := panelayout.Clone(live)
	source := panelayout.Find(live, 1)
	c := NewController("workspace:a", live, 1, panelayout.Box{W: 80, H: 24}, repositionFloors(), false, "shell")
	if c == nil {
		t.Fatal("controller did not open")
	}
	if result, _ := c.HandleKey(key("l")); result.Action != ModalChanged {
		t.Fatalf("draft move = %+v", result)
	}
	if reflect.DeepEqual(c.Draft(), live) {
		t.Fatal("draft did not move independently")
	}
	if !reflect.DeepEqual(live, before) || panelayout.Find(live, 1) != source {
		t.Fatal("draft move mutated live tree")
	}
	if result, _ := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape}); result.Action != ModalCancel {
		t.Fatalf("escape = %+v", result)
	}

	committed := c.Commit("workspace:a", live, panelayout.Box{W: 80, H: 24}, repositionFloors())
	if committed.Reason != "" || !committed.Moved || committed.Focus != 1 || panelayout.Find(committed.Root, 1) != source {
		t.Fatalf("commit = %+v, source=%p got=%p", committed, source, panelayout.Find(committed.Root, 1))
	}
}

func TestControllerLateRefusalAndStaleTreeCommitNothing(t *testing.T) {
	live := repositionTree()
	before := panelayout.Clone(live)
	source := panelayout.Find(live, 1)
	c := NewController("workspace:a", live, 1, panelayout.Box{W: 80, H: 24}, repositionFloors(), false, "shell")
	for _, direction := range []string{"l", "l"} {
		if result, _ := c.HandleKey(key(direction)); result.Action != ModalChanged {
			t.Fatalf("record %q = %+v", direction, result)
		}
	}
	// Two columns still fit 13 columns wide; the late outer-edge step needs a
	// third and is refused. Validation must stop before touching the live tree.
	result := c.Commit("workspace:a", live, panelayout.Box{W: 13, H: 5}, repositionFloors())
	if result.Reason == "" || !reflect.DeepEqual(live, before) || panelayout.Find(live, 1) != source {
		t.Fatalf("late refusal = %+v live=%#v", result, live)
	}

	clone := panelayout.Clone(live)
	result = c.Commit("workspace:a", clone, panelayout.Box{W: 80, H: 24}, repositionFloors())
	if result.Reason != LayoutChangedReason || !reflect.DeepEqual(clone, before) {
		t.Fatalf("replacement tree commit = %+v", result)
	}
	live.Split.Ratio = 60
	result = c.Commit("workspace:a", live, panelayout.Box{W: 80, H: 24}, repositionFloors())
	if result.Reason != LayoutChangedReason || live.Split.Ratio != 60 {
		t.Fatalf("in-place stale commit = %+v ratio=%d", result, live.Split.Ratio)
	}
}

func TestControllerRollsBackIfLiveReplayInvariantDiverges(t *testing.T) {
	live := repositionTree()
	before := panelayout.Clone(live)
	pointers := make(map[int]*panelayout.Node)
	for id := 1; id <= 4; id++ {
		pointers[id] = panelayout.Find(live, id)
	}
	c := NewController("workspace:a", live, 1, panelayout.Box{W: 80, H: 24}, repositionFloors(), false, "shell")
	for _, direction := range []string{"l", "l"} {
		if result, _ := c.HandleKey(key(direction)); result.Action != ModalChanged {
			t.Fatalf("record %q = %+v", direction, result)
		}
	}
	calls := 0
	c.applyLive = func(root *panelayout.Node, plan panelayout.MovePlan) (*panelayout.Node, int) {
		calls++
		if calls == 2 {
			return root, plan.LeafID
		}
		return panelayout.ApplyMove(root, plan)
	}

	result := c.Commit("workspace:a", live, panelayout.Box{W: 80, H: 24}, repositionFloors())
	if result.Reason != LayoutChangedReason || calls != 2 || !reflect.DeepEqual(live, before) {
		t.Fatalf("divergent live replay was not rolled back: result=%+v calls=%d live=%+v", result, calls, live)
	}
	for id, pointer := range pointers {
		if panelayout.Find(live, id) != pointer {
			t.Fatalf("rollback replaced leaf %d: %p -> %p", id, pointer, panelayout.Find(live, id))
		}
	}
}

func TestControllerMouseTargetsCellsAndZoomIsTreeScoped(t *testing.T) {
	live := repositionTree()
	c := NewController("workspace:a", live, 1, panelayout.Box{W: 80, H: 24}, repositionFloors(), false, "shell")
	handler := mouse.NewHandler()
	c.Render(100, 40, handler)
	var target *mouse.Region
	for _, region := range handler.HitMap.Regions() {
		if region.ID == cellAction+"2.2" {
			copy := region
			target = &copy
			break
		}
	}
	if target == nil {
		t.Fatal("miniature registered no 2.2 cell")
	}
	result := c.HandleMouse(tea.MouseClickMsg{X: target.Rect.X + 1, Y: target.Rect.Y + 1, Button: tea.MouseLeft}, handler)
	if result.Action != ModalChanged {
		t.Fatalf("cell click = %+v", result)
	}
	if result, _ = c.HandleKey(key("z")); result.Action != ModalChanged || !c.Zoomed() {
		t.Fatalf("zoom key = %+v zoomed=%v", result, c.Zoomed())
	}
	commit := c.Commit("workspace:a", live, panelayout.Box{W: 80, H: 24}, repositionFloors())
	if commit.Reason != "" || commit.ZoomLeaf != 1 {
		t.Fatalf("zoom commit = %+v", commit)
	}

	var zoom Zoom
	zoom.Set("workspace:a", commit.Root, commit.ZoomLeaf)
	if zoom.Leaf("workspace:a", commit.Root) != 1 {
		t.Fatal("zoom did not bind to committed tree")
	}
	replacement := panelayout.Clone(commit.Root)
	if panelayout.Find(replacement, 1) == nil {
		t.Fatal("replacement premise lost reused leaf ID")
	}
	if zoom.Leaf("workspace:a", replacement) != 0 {
		t.Fatal("zoom leaked to a replacement tree with the same leaf ID")
	}
}
