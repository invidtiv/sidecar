package workspace

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

// Agent lane transitions become notifications here — and only here.
//
// The rules (which lane change is worth saying, how long it must hold, what
// self-dismisses) live in notify.LaneTracker, which knows nothing about tmux or
// this plugin. This file is the adapter: it turns whatever the poller has
// already resolved into observations, and turns the tracker's answer into the
// commands the app shell already understands. A headless watcher wanting the
// same notifications reimplements this file and nothing else.
//
// It runs from the single seam at the bottom of Plugin.Update rather than at
// each of the three sites that apply a status, because every one of those sites
// ends in one of a dozen early returns. Sweeping once per update is the same
// discipline the focus rule and the live-watch set already follow, and it costs
// one map walk over workspaces the plugin is holding in memory anyway.

// notifyAgentTransitions folds the current agent states into the lane tracker
// and returns the posts and self-dismissals it produced.
func (p *Plugin) notifyAgentTransitions(now time.Time) tea.Cmd {
	if p == nil {
		return nil
	}
	if len(p.pendingAgentLaneSeeds) > 0 {
		// A complete observation set is the tracker's deletion signal. Do not
		// apply retained waits until both independently loaded inventories are
		// ready, or the empty startup projection would immediately withdraw them.
		if !p.stateRestored {
			return nil
		}
		p.agentLaneTracker.Seed(p.pendingAgentLaneSeeds)
		p.pendingAgentLaneSeeds = nil
	}
	events := p.agentLaneTracker.Observe(p.agentLaneObservations(), now)
	if events.Empty() {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(events.Post)+len(events.Dismiss))
	for _, n := range events.Post {
		posted := n
		cmds = append(cmds, func() tea.Msg { return notify.PostMsg{Notification: posted} })
	}
	for _, id := range events.Dismiss {
		dismissed := id
		cmds = append(cmds, func() tea.Msg { return notify.DismissMsg{ID: dismissed} })
	}
	return tea.Batch(cmds...)
}

// queueAgentLaneSeeds applies only the pure structured ownership contract. The
// accepted records still wait for complete inventory before absence can mean a
// real lane departure.
func (p *Plugin) queueAgentLaneSeeds(all []notify.Notification) {
	for _, n := range all {
		if p.ownsAgentLaneNotification(n) {
			p.pendingAgentLaneSeeds = append(p.pendingAgentLaneSeeds, n)
		}
	}
}

func (p *Plugin) ownsAgentLaneNotification(n notify.Notification) bool {
	if p == nil {
		return false
	}
	return notify.TransitionOwnedByProject(n, p.laneProjectRoot())
}

// agentLaneObservations is the whole set of workspaces this plugin can speak
// for. It must be complete every time: a workspace missing from the set is how
// the tracker learns a shell is gone.
func (p *Plugin) agentLaneObservations() []notify.LaneObservation {
	out := make([]notify.LaneObservation, 0, len(p.shells)+len(p.worktrees))
	for _, wt := range p.worktrees {
		if o, ok := p.worktreeObservation(wt); ok {
			out = append(out, o)
		}
	}
	for _, shell := range p.shells {
		if o, ok := p.shellObservation(shell); ok {
			out = append(out, o)
		}
	}
	for _, nested := range p.nestedByWorkDir {
		for _, shell := range nested {
			if o, ok := p.shellObservation(shell); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

func (p *Plugin) worktreeObservation(wt *Worktree) (notify.LaneObservation, bool) {
	// Only a worktree running an agent whose activity we can actually read has
	// lanes worth notifying about. A plain worktree's lane is a projection of
	// legacy status and would announce transitions nobody made.
	if wt == nil || wt.Agent == nil || !supportsAgentActivity(wt.Agent.Type) {
		return notify.LaneObservation{}, false
	}
	return notify.LaneObservation{
		Key:          "worktree:" + wt.IdentityKey(),
		Label:        wt.Name,
		Context:      p.laneContext(wt.Branch),
		Provider:     string(wt.Agent.Type),
		Presentation: agentStatusPresentation(wt),
		Origin:       p.laneOrigin(wt.Agent.TmuxSession, wt.Path),
		ProjectRoot:  p.laneProjectRoot(),
	}, true
}

func (p *Plugin) shellObservation(shell *ShellSession) (notify.LaneObservation, bool) {
	if shell == nil || shell.Agent == nil || !supportsAgentActivity(shell.Agent.Type) {
		return notify.LaneObservation{}, false
	}
	return notify.LaneObservation{
		Key:          "shell:" + shell.TmuxName,
		Label:        shell.Name,
		Context:      p.laneContext(""),
		Provider:     string(shell.Agent.Type),
		Presentation: shellAgentStatusPresentation(shell),
		Origin:       p.laneOrigin(shell.TmuxName, shell.WorkDir),
		ProjectRoot:  p.laneProjectRoot(),
	}, true
}

// laneContext is the "where" half of a notification body: the project, plus the
// branch when there is one. With five agents running, the name alone is not
// enough to know which toast is which.
func (p *Plugin) laneContext(branch string) string {
	project := p.laneProjectKey()
	branch = strings.TrimSpace(branch)
	switch {
	case project != "" && branch != "" && branch != project:
		return project + "/" + branch
	case project != "":
		return project
	default:
		return branch
	}
}

func (p *Plugin) laneProjectKey() string {
	root := p.laneProjectRoot()
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

func (p *Plugin) laneProjectRoot() string {
	if p.ctx == nil {
		return ""
	}
	root := strings.TrimSpace(p.ctx.ProjectRoot)
	if root == "" {
		root = strings.TrimSpace(p.ctx.WorkDir)
	}
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

// laneOrigin records the shell the notification is about, in the same shape the
// CLI sends. It is what makes an agent's own `sidecar notify dismiss` and a
// lane trigger agree on identity.
func (p *Plugin) laneOrigin(tmuxSession, workDir string) notify.Origin {
	return notify.Origin{
		TmuxSession: strings.TrimSpace(tmuxSession),
		ProjectKey:  p.laneProjectKey(),
		WorkDir:     strings.TrimSpace(workDir),
	}
}

// AttentionOrigin projects the selected shell/worktree through the same
// identity vocabulary the global Sessions browser uses.
func (p *Plugin) AttentionOrigin() (plugin.AttentionOrigin, bool) {
	if p == nil || !p.focused {
		return plugin.AttentionOrigin{}, false
	}
	if shell := p.getSelectedShell(); shell != nil {
		return plugin.AttentionOrigin{TmuxSession: shell.TmuxName, ProjectKey: p.laneProjectKey(), WorkDir: shell.WorkDir}, true
	}
	if wt := p.selectedWorktree(); wt != nil {
		session := ""
		if wt.Agent != nil {
			session = wt.Agent.TmuxSession
		}
		return plugin.AttentionOrigin{TmuxSession: session, ProjectKey: p.laneProjectKey(), WorkDir: wt.Path}, true
	}
	return plugin.AttentionOrigin{}, false
}
