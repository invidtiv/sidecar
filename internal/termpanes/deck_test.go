package termpanes

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

func TestDeckKeysLeavesAndCountsTheTree(t *testing.T) {
	d := New()
	leaf := Decode(7, "session", "%1", nil)
	if leaf.RowAnalyzer == nil {
		t.Fatal("decoded leaf has no row analyzer")
	}
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

func TestRekeyCarriesLeafInteractionState(t *testing.T) {
	d := New()
	leaf := NewLeaf(2, nil)
	if leaf.RowAnalyzer == nil {
		t.Fatal("new leaf has no row analyzer")
	}
	analyzer := leaf.RowAnalyzer
	leaf.Interactive = true
	leaf.Selection.SelectRange(ui.SelectionPoint{Line: 4, Col: 1}, ui.SelectionPoint{Line: 4, Col: 5}, false)
	leaf.HostState = "host gesture"
	d.Attach(leaf)

	got := d.Rekey(2, 7)
	if got != leaf || d.Leaf(2) != nil || d.Leaf(7) != leaf {
		t.Fatal("rekey did not preserve the leaf identity")
	}
	if !got.Interactive || !got.Selection.HasSelection() || got.HostState != "host gesture" {
		t.Fatal("rekey dropped interaction state owned by the leaf")
	}
	if got.RowAnalyzer != analyzer {
		t.Fatal("rekey replaced the leaf's durable row analyzer")
	}
}
