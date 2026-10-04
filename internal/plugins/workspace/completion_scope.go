package workspace

// completionScope captures the requesting projection before a command leaves
// the update loop. Shell and pane completions do not participate in the single
// worktree lifecycle operation, but still belong to one project/worktree epoch.
func (p *Plugin) completionScope() OperationScope {
	if p == nil || p.ctx == nil {
		return OperationScope{}
	}
	return OperationScope{
		Epoch: p.ctx.Epoch, ProjectRoot: p.ctx.ProjectRoot, WorkDir: p.ctx.WorkDir,
		OperationID: "completion",
	}
}
