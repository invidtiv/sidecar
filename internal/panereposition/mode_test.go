package panereposition

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
)

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
