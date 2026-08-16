package overview

import "strings"

// Display names match workspace.AgentDisplayNames so Overview can label
// agents without importing the workspace plugin.
var createAgentDisplayNames = map[string]string{
	"":            "None (attach only)",
	"claude":      "Claude Code",
	"codex":       "Codex CLI",
	"copilot":     "GitHub Copilot CLI",
	"antigravity": "Antigravity",
	"cursor":      "Cursor Agent",
	"opencode":    "OpenCode",
	"pi":          "Pi Agent",
	"amp":         "Amp",
	"grok":        "Grok",
	"shell":       "Project Shell",
}

// Worktree picker order when config.Agents is empty (None last).
var createAgentWorktreeOrder = []string{
	"claude", "codex", "copilot", "antigravity", "cursor", "opencode", "pi", "amp", "grok", "",
}

// Shell picker order when config.Agents is empty (None first).
var createAgentShellOrder = []string{
	"", "claude", "codex", "copilot", "antigravity", "cursor", "opencode", "pi", "amp", "grok",
}

func createAgentLabel(agentType string) string {
	if label, ok := createAgentDisplayNames[agentType]; ok {
		return label
	}
	if agentType == "" {
		return "None (attach only)"
	}
	return agentType
}

func isKnownCreateAgent(agentType string) bool {
	if agentType == "" {
		return false
	}
	_, ok := createAgentDisplayNames[agentType]
	return ok
}

func resolveCreateAgents(configAgents []string, shellMode bool) []string {
	fallback := createAgentWorktreeOrder
	if shellMode {
		fallback = createAgentShellOrder
	}
	if len(configAgents) == 0 {
		out := make([]string, len(fallback))
		copy(out, fallback)
		return out
	}
	seen := make(map[string]bool)
	var real []string
	for _, raw := range configAgents {
		id := strings.TrimSpace(raw)
		if id == "" || !isKnownCreateAgent(id) || seen[id] {
			continue
		}
		seen[id] = true
		real = append(real, id)
	}
	if len(real) == 0 {
		out := make([]string, len(fallback))
		copy(out, fallback)
		return out
	}
	if shellMode {
		return append([]string{""}, real...)
	}
	return append(real, "")
}

func indexOfCreateAgent(list []string, agentType string) int {
	for i, at := range list {
		if at == agentType {
			return i
		}
	}
	return -1
}
