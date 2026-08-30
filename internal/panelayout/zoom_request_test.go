package panelayout

import "testing"

func TestLayoutTreeRequestedZoomUsesTheNamedLeafAndFallsBackWhenItIsGone(t *testing.T) {
	root := split(3, Columns, 50, leaf(1, Primary), leaf(2, Document))
	box := Box{W: 80, H: 24}
	floors := Floors{Primary: Floor{Width: 4, Height: 2}, Doc: Floor{Width: 4, Height: 2}}
	layout, ok := LayoutTreeWithZoom(root, box, floors, 1, 2)
	if !ok || !layout.Zoomed || len(layout.Leaves) != 1 || layout.Leaves[0].Node != Find(root, 2) || layout.Leaves[0].Box != box {
		t.Fatalf("requested zoom = %#v ok=%v", layout, ok)
	}
	layout, ok = LayoutTreeWithZoom(root, box, floors, 1, 99)
	if !ok || layout.Zoomed || len(layout.Leaves) != 2 {
		t.Fatalf("missing zoom target did not fall back to the tree: %#v ok=%v", layout, ok)
	}
}
