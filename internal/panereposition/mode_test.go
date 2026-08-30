package panereposition

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
)

func TestModeIsScopedAndDiesWithItsLeaf(t *testing.T) {
	root := &panelayout.Node{ID: 1, Kind: panelayout.Primary}
	var mode Mode
	if !mode.Start("surface:1", 1) || !mode.Reconcile("surface:1", root) {
		t.Fatal("mode did not start on its scoped leaf")
	}
	if mode.Reconcile("surface:2", root) || mode.LeafID() != 0 {
		t.Fatal("mode crossed a tree scope")
	}
	mode.Start("surface:1", 1)
	if mode.Reconcile("surface:1", &panelayout.Node{ID: 2, Kind: panelayout.Primary}) || mode.LeafID() != 0 {
		t.Fatal("mode survived its leaf closing")
	}
}

func TestDecodeOwnsMoveAndExitKeys(t *testing.T) {
	for key, want := range map[string]panelayout.Direction{
		"h": panelayout.DirectionLeft, "left": panelayout.DirectionLeft,
		"j": panelayout.DirectionDown, "down": panelayout.DirectionDown,
		"k": panelayout.DirectionUp, "up": panelayout.DirectionUp,
		"l": panelayout.DirectionRight, "right": panelayout.DirectionRight,
	} {
		got := Decode(key)
		if !got.Move || got.Exit || got.Direction != want {
			t.Errorf("Decode(%q) = %+v", key, got)
		}
	}
	for _, key := range []string{"M", "enter", "esc"} {
		if got := Decode(key); !got.Exit || got.Move {
			t.Errorf("Decode(%q) = %+v", key, got)
		}
	}
	if got := Decode("q"); got != (Action{}) {
		t.Fatalf("unrecognized key = %+v", got)
	}
}

func TestLeafGraftPreservesExactLeafAndShape(t *testing.T) {
	primary := &panelayout.Node{ID: 1, Kind: panelayout.Primary}
	doc := &panelayout.Node{ID: 2, Kind: panelayout.Document}
	shell := &panelayout.Node{ID: 4, Kind: panelayout.Shell}
	old := &panelayout.Node{ID: 8, Split: &panelayout.Split{Axis: panelayout.Columns, Ratio: 61,
		A: doc, B: &panelayout.Node{ID: 7, Split: &panelayout.Split{Axis: panelayout.Rows, Ratio: 37, A: shell, B: primary}},
	}}
	grafts := CaptureLeafGrafts(old, panelayout.Shell)
	if len(grafts) != 1 {
		t.Fatalf("grafts = %+v", grafts)
	}
	fresh := &panelayout.Node{ID: 9, Split: &panelayout.Split{Axis: panelayout.Columns, Ratio: 61,
		A: &panelayout.Node{ID: 2, Kind: panelayout.Document}, B: &panelayout.Node{ID: 1, Kind: panelayout.Primary},
	}}
	fresh = ApplyLeafGraft(fresh, grafts[0], shell)
	if panelayout.Find(fresh, shell.ID) != shell {
		t.Fatal("graft replaced the exact host leaf")
	}
	parent := panelayout.Find(fresh, 7)
	if parent == nil || parent.Split == nil || parent.Split.Axis != panelayout.Rows || parent.Split.Ratio != 37 || parent.Split.A != shell || parent.Split.B.ID != primary.ID {
		t.Fatalf("graft shape = %+v", parent)
	}
}

func TestFingerprintDetectsSamePointerRewrite(t *testing.T) {
	root := &panelayout.Node{ID: 3, Split: &panelayout.Split{Axis: panelayout.Columns, Ratio: 50,
		A: &panelayout.Node{ID: 1, Kind: panelayout.Primary}, B: &panelayout.Node{ID: 2, Kind: panelayout.Document},
	}}
	before := Fingerprint(root)
	root.Split.A, root.Split.B = root.Split.B, root.Split.A
	if after := Fingerprint(root); after == before {
		t.Fatal("in-place child rewrite kept the same fingerprint")
	}
}
