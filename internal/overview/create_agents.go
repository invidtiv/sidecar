package overview

import "github.com/marcus/sidecar/internal/agentcatalog"

func createAgentLabel(agentType string) string {
	return agentcatalog.Label(agentType)
}

func resolveCreateAgents(configAgents []string, shellMode bool) []string {
	return agentcatalog.ResolvePicker(configAgents, shellMode)
}

func indexOfCreateAgent(list []string, agentType string) int {
	for i, at := range list {
		if at == agentType {
			return i
		}
	}
	return -1
}
