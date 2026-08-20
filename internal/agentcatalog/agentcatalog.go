// Package agentcatalog is the single description of the agent families Sidecar
// can start: their order in a creation picker, what to call them, and the
// command each one launches by default.
//
// It is a leaf package on purpose. The workspace plugin builds its creation
// pickers from it, and Configuration lists the same families without importing
// the plugin (the plugin imports the app, and the app imports Configuration).
// One table, two readers, no drift.
package agentcatalog

import "strings"

// Family is one agent family Sidecar offers when work is created.
type Family struct {
	// ID is the stored identity — the value written to plugins.workspace.agents,
	// agentStart, and defaultAgentType.
	ID string
	// Name is the full display name.
	Name string
	// Short is the compact label a settings row or a selector uses.
	Short string
	// Command is the executable Sidecar launches when no override is configured.
	Command string
}

// families is the ordered list a creation picker offers. Order is the picker's
// order; adding a family here adds it everywhere.
var families = []Family{
	{ID: "claude", Name: "Claude Code", Short: "Claude", Command: "claude"},
	{ID: "codex", Name: "Codex CLI", Short: "Codex", Command: "codex"},
	{ID: "copilot", Name: "GitHub Copilot CLI", Short: "Copilot", Command: "copilot"},
	{ID: "antigravity", Name: "Antigravity", Short: "Antigravity", Command: "agy"},
	{ID: "cursor", Name: "Cursor Agent", Short: "Cursor", Command: "cursor-agent"},
	{ID: "opencode", Name: "OpenCode", Short: "OpenCode", Command: "opencode"},
	{ID: "pi", Name: "Pi Agent", Short: "Pi", Command: "pi"},
	{ID: "amp", Name: "Amp", Short: "Amp", Command: "amp"},
	{ID: "grok", Name: "Grok", Short: "Grok", Command: "grok"},
}

// Families returns every selectable family in picker order.
func Families() []Family {
	out := make([]Family, len(families))
	copy(out, families)
	return out
}

// Find returns the family with an ID, if it is one Sidecar knows.
func Find(id string) (Family, bool) {
	for _, family := range families {
		if family.ID == id {
			return family, true
		}
	}
	return Family{}, false
}

// Known reports whether an ID names a family Sidecar can start.
func Known(id string) bool {
	_, ok := Find(id)
	return ok
}

// Resolve is the allowlist rule creation uses, stated once.
//
// An empty allowlist means every family: a user who has never touched the
// setting is offered everything, and so is a user whose allowlist names nothing
// Sidecar recognizes. Otherwise the allowlist is honoured in its own order,
// with unknown and duplicate entries dropped.
func Resolve(allowlist []string) []Family {
	if len(allowlist) == 0 {
		return Families()
	}
	seen := make(map[string]bool, len(allowlist))
	var out []Family
	for _, raw := range allowlist {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		family, ok := Find(id)
		if !ok {
			continue
		}
		seen[id] = true
		out = append(out, family)
	}
	if len(out) == 0 {
		return Families()
	}
	return out
}

// ResolvePicker is the creation-picker list: Resolve's allowlist, then None
// (empty string) placed first for shells and last for worktrees.
//
// Empty and unrecognized allowlists follow Resolve: every catalog family.
func ResolvePicker(allowlist []string, shellMode bool) []string {
	families := Resolve(allowlist)
	out := make([]string, 0, len(families)+1)
	if shellMode {
		out = append(out, "")
	}
	for _, family := range families {
		out = append(out, family.ID)
	}
	if !shellMode {
		out = append(out, "")
	}
	return out
}

// Label is the picker display name for an agent id.
//
// "" is "None (attach only)". Catalog IDs use Family.Name. "shell" is
// "Project Shell". Unknown IDs pass through.
func Label(id string) string {
	switch id {
	case "":
		return "None (attach only)"
	case "shell":
		return "Project Shell"
	}
	if family, ok := Find(id); ok {
		return family.Name
	}
	return id
}
