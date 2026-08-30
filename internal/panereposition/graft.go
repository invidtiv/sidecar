package panereposition

import (
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/panelayout"
)

// LeafGraft records the exact split that attached a host-owned leaf to a
// passive tree. A content deck can replace the passive projection without
// knowing about that leaf; the host replays this record afterward.
type LeafGraft struct {
	LeafID    int
	AnchorID  int
	SplitID   int
	Axis      panelayout.Axis
	Ratio     int
	LeafFirst bool
}

// CaptureLeafGrafts finds direct split attachments for leaves of kind.
func CaptureLeafGrafts(root *panelayout.Node, kind panelayout.Kind) []LeafGraft {
	var grafts []LeafGraft
	var walk func(*panelayout.Node)
	walk = func(node *panelayout.Node) {
		if node == nil || node.Split == nil {
			return
		}
		a, b := node.Split.A, node.Split.B
		if a != nil && b != nil && a.Split == nil && a.Kind == kind {
			grafts = append(grafts, LeafGraft{LeafID: a.ID, AnchorID: b.ID, SplitID: node.ID, Axis: node.Split.Axis, Ratio: node.Split.Ratio, LeafFirst: true})
		}
		if a != nil && b != nil && b.Split == nil && b.Kind == kind {
			grafts = append(grafts, LeafGraft{LeafID: b.ID, AnchorID: a.ID, SplitID: node.ID, Axis: node.Split.Axis, Ratio: node.Split.Ratio})
		}
		walk(a)
		walk(b)
	}
	walk(root)
	return grafts
}

// ApplyLeafGraft attaches the exact leaf object to fresh at its captured
// anchor/order/axis/ratio. If the passive projection removed the anchor, the
// whole fresh tree is the stable fallback, matching the existing Sessions
// behavior. Split identity is retained when it does not collide.
func ApplyLeafGraft(fresh *panelayout.Node, graft LeafGraft, leaf *panelayout.Node) *panelayout.Node {
	if fresh == nil || leaf == nil || leaf.Split != nil || leaf.ID != graft.LeafID || panelayout.Find(fresh, leaf.ID) != nil {
		return fresh
	}
	splitID := graft.SplitID
	if splitID <= 0 || panelayout.Find(fresh, splitID) != nil || splitID == leaf.ID {
		splitID = max(panelayout.MaxID(fresh), leaf.ID) + 1
	}
	build := func(anchor *panelayout.Node) *panelayout.Node {
		a, b := anchor, leaf
		if graft.LeafFirst {
			a, b = leaf, anchor
		}
		return &panelayout.Node{ID: splitID, Split: &panelayout.Split{Axis: graft.Axis, Ratio: graft.Ratio, A: a, B: b}}
	}
	if fresh.ID == graft.AnchorID {
		return build(fresh)
	}
	if spliceNode(fresh, graft.AnchorID, build) {
		return fresh
	}
	return build(fresh)
}

func spliceNode(root *panelayout.Node, anchorID int, build func(*panelayout.Node) *panelayout.Node) bool {
	if root == nil || root.Split == nil {
		return false
	}
	for _, side := range []**panelayout.Node{&root.Split.A, &root.Split.B} {
		child := *side
		if child == nil {
			continue
		}
		if child.ID == anchorID {
			*side = build(child)
			return true
		}
		if spliceNode(child, anchorID, build) {
			return true
		}
	}
	return false
}

// Fingerprint is a deterministic structural generation for a pane tree. It
// changes for in-place rewrites as well as root replacement, while remaining
// stable across reads of the same tree.
func Fingerprint(root *panelayout.Node) string {
	var out strings.Builder
	var walk func(*panelayout.Node)
	walk = func(node *panelayout.Node) {
		if node == nil {
			out.WriteString("nil;")
			return
		}
		out.WriteString(strconv.Itoa(node.ID))
		out.WriteByte(':')
		if node.Split == nil {
			out.WriteString(strconv.Itoa(int(node.Kind)))
			out.WriteByte(':')
			out.WriteString(strconv.Itoa(node.ContentID))
			out.WriteByte(';')
			return
		}
		out.WriteByte('s')
		out.WriteByte(':')
		out.WriteString(strconv.Itoa(int(node.Split.Axis)))
		out.WriteByte(':')
		out.WriteString(strconv.Itoa(node.Split.Ratio))
		out.WriteByte('[')
		walk(node.Split.A)
		walk(node.Split.B)
		out.WriteString("];")
	}
	walk(root)
	return out.String()
}
