package workspace

import "github.com/marcus/sidecar/internal/agentcatalog"

// selectableAgentTypes returns the ordered agent list for worktree create and
// start-agent pickers. Empty config agents = full AgentTypeOrder (including None last).
// Config is an ordered allowlist of real agent IDs; unknown IDs are skipped.
// AgentNone is always appended last when not already present.
func (p *Plugin) selectableAgentTypes() []AgentType {
	return resolveSelectableAgents(p.configAgents(), false)
}

// selectableShellAgentTypes returns the ordered agent list for shell create.
// Empty config = ShellAgentOrder (None first). With config, None is forced first
// then allowlisted agents in config order.
func (p *Plugin) selectableShellAgentTypes() []AgentType {
	return resolveSelectableAgents(p.configAgents(), true)
}

func (p *Plugin) configAgents() []string {
	if p == nil || p.ctx == nil || p.ctx.Config == nil {
		return nil
	}
	return p.ctx.Config.Plugins.Workspace.Agents
}

// resolveSelectableAgents builds the picker list.
// shellMode=true forces None first; shellMode=false forces None last (worktree style).
func resolveSelectableAgents(configAgents []string, shellMode bool) []AgentType {
	ids := agentcatalog.ResolvePicker(configAgents, shellMode)
	out := make([]AgentType, len(ids))
	for i, id := range ids {
		out[i] = AgentType(id)
	}
	return out
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
