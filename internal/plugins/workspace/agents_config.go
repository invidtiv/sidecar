package workspace

import "github.com/marcus/sidecar/internal/agentcatalog"

// selectableAgentTypes returns the ordered agent list for worktree create and
// start-agent pickers. Empty config agents = full AgentTypeOrder (including None last).
// Config is an ordered allowlist of real agent IDs; unknown IDs are skipped.
// AgentNone is always appended last when not already present.
func (p *Plugin) selectableAgentTypes() []AgentType {
	return resolveSelectableAgents(p.configAgents(), AgentTypeOrder, false)
}

// selectableShellAgentTypes returns the ordered agent list for shell create.
// Empty config = ShellAgentOrder (None first). With config, None is forced first
// then allowlisted agents in config order.
func (p *Plugin) selectableShellAgentTypes() []AgentType {
	return resolveSelectableAgents(p.configAgents(), ShellAgentOrder, true)
}

func (p *Plugin) configAgents() []string {
	if p == nil || p.ctx == nil || p.ctx.Config == nil {
		return nil
	}
	return p.ctx.Config.Plugins.Workspace.Agents
}

// resolveSelectableAgents builds the picker list.
// shellMode=true forces None first; shellMode=false forces None last (worktree style).
func resolveSelectableAgents(configAgents []string, defaultOrder []AgentType, shellMode bool) []AgentType {
	if len(configAgents) == 0 {
		// Return a copy so callers can index safely without mutating globals.
		out := make([]AgentType, len(defaultOrder))
		copy(out, defaultOrder)
		return out
	}

	// The allowlist rule itself lives in internal/agentcatalog, so Configuration's
	// Agents page and these pickers cannot disagree about what a saved allowlist
	// means. None is placed by the shellMode rules below, never by config.
	resolved := agentcatalog.Resolve(configAgents)
	real := make([]AgentType, 0, len(resolved))
	for _, family := range resolved {
		real = append(real, AgentType(family.ID))
	}

	// If nothing valid remains, fall back to default order.
	if len(real) == 0 {
		out := make([]AgentType, len(defaultOrder))
		copy(out, defaultOrder)
		return out
	}

	if shellMode {
		out := make([]AgentType, 0, len(real)+1)
		out = append(out, AgentNone)
		out = append(out, real...)
		return out
	}
	out := make([]AgentType, 0, len(real)+1)
	out = append(out, real...)
	out = append(out, AgentNone)
	return out
}

// agentTypeIndexIn returns the index of agentType in list, or 0 if missing.
func agentTypeIndexIn(list []AgentType, agentType AgentType) int {
	for i, at := range list {
		if at == agentType {
			return i
		}
	}
	return 0
}

// clampAgentSelection ensures type/index are consistent with the selectable list.
// If agentType is not in list, falls back to first entry (or None for empty).
func clampAgentSelection(list []AgentType, agentType AgentType, idx int) (AgentType, int) {
	if len(list) == 0 {
		return AgentNone, 0
	}
	if idx >= 0 && idx < len(list) && list[idx] == agentType {
		return agentType, idx
	}
	for i, at := range list {
		if at == agentType {
			return agentType, i
		}
	}
	// Prefer first real agent for worktree lists (None last); first entry for shell (often None).
	return list[0], 0
}

// withPreferredAgent returns list with preferred inserted (before trailing None)
// when it is a known non-empty agent missing from the allowlist. Used so restart
// modals can still show an existing worktree's agent even if the user hid it.
func withPreferredAgent(list []AgentType, preferred AgentType) []AgentType {
	if preferred == AgentNone || preferred == "" || !isKnownAgentType(preferred) {
		return list
	}
	for _, at := range list {
		if at == preferred {
			return list
		}
	}
	out := make([]AgentType, 0, len(list)+1)
	inserted := false
	for _, at := range list {
		if at == AgentNone && !inserted {
			out = append(out, preferred)
			inserted = true
		}
		out = append(out, at)
	}
	if !inserted {
		out = append(out, preferred)
	}
	return out
}
