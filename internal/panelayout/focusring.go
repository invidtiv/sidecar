package panelayout

// TargetKind names the sort of window a focus target refers to.
type TargetKind int

const (
	// TargetSidebar is the workspace list beside the preview.
	TargetSidebar TargetKind = iota
	// TargetLeaf is a pane-tree leaf, identified by Target.Leaf.
	TargetLeaf
	// TargetTermPanel is the transitional terminal panel entry. It is deleted
	// when windowing M1 absorbs the panel as a tree leaf; this case and the
	// termPanelVisible argument to Ring are the only ring-side sites to remove.
	TargetTermPanel
)

// Target is a single focusable window. Leaf is meaningful only for TargetLeaf.
type Target struct {
	Kind TargetKind
	Leaf int
}

// Ring lists the focusable windows in cycle order: the sidebar (when visible),
// then every tree leaf in placement order, then the terminal panel (when
// visible). Leaf order matches the A-then-B walk LayoutPanes performs, so Tab
// follows the same sequence the panes are drawn in.
func Ring(root *Node, sidebarVisible, termPanelVisible bool) []Target {
	var ring []Target
	if sidebarVisible {
		ring = append(ring, Target{Kind: TargetSidebar})
	}
	ring = appendLeafTargets(root, ring)
	if termPanelVisible {
		ring = append(ring, Target{Kind: TargetTermPanel})
	}
	return ring
}

func appendLeafTargets(node *Node, ring []Target) []Target {
	if node == nil {
		return ring
	}
	if node.Split == nil {
		return append(ring, Target{Kind: TargetLeaf, Leaf: node.ID})
	}
	ring = appendLeafTargets(node.Split.A, ring)
	return appendLeafTargets(node.Split.B, ring)
}

// CycleTarget returns the next target after current, wrapping at both ends. A
// current target that is not in the ring starts the cycle at the first entry
// (the last when reverse); an empty ring leaves focus where it is.
func CycleTarget(ring []Target, current Target, reverse bool) Target {
	if len(ring) == 0 {
		return current
	}
	index := -1
	for i, target := range ring {
		if target == current {
			index = i
			break
		}
	}
	if index < 0 {
		if reverse {
			return ring[len(ring)-1]
		}
		return ring[0]
	}
	if reverse {
		return ring[(index-1+len(ring))%len(ring)]
	}
	return ring[(index+1)%len(ring)]
}
