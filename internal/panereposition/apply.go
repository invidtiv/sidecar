package panereposition

import "github.com/marcus/sidecar/internal/panelayout"

// ApplyLive installs one accepted plan on the LIVE tree and guarantees what the
// modal's replay guarantees for each of its steps: the moved leaf is the same
// *Node it was before, carrying the same ID, so host-owned terminal, deck, and
// tab state keyed by either identity travels with the pane instead of being
// rebuilt.
//
// It exists so the agent verb and the modal cannot answer this differently. The
// modal replays a sequence and needs its own rollback; a single-call move needs
// exactly this. Both go through panelayout.ApplyMove, and neither host writes a
// structural edit of its own.
//
// A non-empty reason means the live tree was not touched.
func ApplyLive(live *panelayout.Node, plan panelayout.MovePlan) (*panelayout.Node, int, string) {
	if live == nil {
		return live, 0, LayoutChangedReason
	}
	source := panelayout.Find(live, plan.LeafID)
	if source == nil || source.Split != nil {
		return live, plan.LeafID, LayoutChangedReason
	}
	root, focus := panelayout.ApplyMove(live, plan)
	if focus != plan.LeafID || panelayout.Find(root, plan.LeafID) != source {
		return live, plan.LeafID, LayoutChangedReason
	}
	return root, focus, ""
}

// TrialMove answers what a plan would produce without touching the live tree.
// Hosts use it to ask a content deck whether it can adopt the result before the
// move is real, which is the same order the modal commits in.
func TrialMove(live *panelayout.Node, plan panelayout.MovePlan) *panelayout.Node {
	if live == nil {
		return nil
	}
	trial, _ := panelayout.ApplyMove(panelayout.Clone(live), plan)
	return trial
}
