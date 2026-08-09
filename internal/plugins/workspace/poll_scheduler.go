package workspace

func agentPollKey(worktreeName string) string {
	return "agent:" + worktreeName
}

func shellPollKey(tmuxName string) string {
	return "shell:" + tmuxName
}
