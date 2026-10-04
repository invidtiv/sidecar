package workspacelist

// Projected applies the shared query, pin order and sort to a catalog. Pins
// obey the query and retain their configured order; every other matching row
// follows the selected stable sort. The caller's slices are never changed.
// Selection and viewport state belong to the consumer, not this projection.
func Projected(items []Item, query string, mode Sort, pinnedIDs []string) []Item {
	matched := Filtered(items, query)
	pinned, rest := splitPinned(matched, pinnedIDs)
	return append(pinned, Sorted(rest, mode)...)
}

// NormalizePins removes empty and duplicate identities without consulting a
// catalog. Missing rows retain their pin while inventory is still arriving.
func NormalizePins(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// IsPinned reports membership independently of whether the row is visible.
func IsPinned(ids []string, id string) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

// TogglePinned returns a new pin order. New pins are appended, preserving
// first-pinned-first order, and an empty target leaves the order unchanged.
func TogglePinned(ids []string, id string) []string {
	out := append([]string(nil), ids...)
	if id == "" {
		return out
	}
	for i, existing := range out {
		if existing == id {
			return append(out[:i], out[i+1:]...)
		}
	}
	return append(out, id)
}

// RetainPins prunes identities absent from the complete known catalog. Use
// the unfiltered catalog, including hidden hosts and idle rows, so a view
// preference or query never deletes a durable pin. Callers decide when their
// inventory is complete enough to prune.
func RetainPins(ids, knownIDs []string) []string {
	known := make(map[string]bool, len(knownIDs))
	for _, id := range knownIDs {
		known[id] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if known[id] {
			out = append(out, id)
		}
	}
	return out
}

// Visibility is source-resolved input to the list's visibility rule. The
// consumer knows whether a remote worktree is a main checkout with no agent;
// the rule never reads inventory, tmux or the filesystem to decide that fact.
type Visibility struct {
	ShowIdleWorktrees      bool
	Revealed               bool
	RemoteMainWithoutAgent bool
}

// Hidden reports whether the view preference hides a row. An explicitly
// revealed worktree wins over both idle filtering and the remote project-home
// exclusion. Managed shells and agent states are not inferred to be idle.
func Hidden(group Group, visibility Visibility) bool {
	if visibility.Revealed {
		return false
	}
	return visibility.RemoteMainWithoutAgent || (!visibility.ShowIdleWorktrees && group == GroupNoSession)
}
