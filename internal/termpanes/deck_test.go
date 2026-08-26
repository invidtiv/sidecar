package termpanes

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
)

func TestDeckKeysLeavesAndCountsTheTree(t *testing.T) {
	d := New()
	leaf := Decode(7, "session", "%1", nil)
	if !d.Attach(leaf) || d.Leaf(7) != leaf {
		t.Fatal("attached leaf was not addressable by tree ID")
	}
	root := &panelayout.Node{Split: &panelayout.Split{
		A: &panelayout.Node{ID: 1, Kind: panelayout.Primary},
		B: &panelayout.Node{ID: 7, Kind: panelayout.Shell},
	}}
	if got := d.LiveLeafCount(root); got != 2 {
		t.Fatalf("LiveLeafCount = %d, want 2", got)
	}
	if released := d.Release(7); released != leaf || d.Leaf(7) != nil {
		t.Fatal("release did not remove the keyed leaf")
	}
}
