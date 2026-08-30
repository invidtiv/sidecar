package workspace

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

func triggerShell(name, tmux string, state agentactivity.State, at time.Time) *ShellSession {
	return &ShellSession{
		Name:     name,
		TmuxName: tmux,
		WorkDir:  "/tmp/repo",
		Agent: &Agent{
			Type:               AgentClaude,
			TmuxSession:        tmux,
			Activity:           agentactivity.Tracker{State: state, ChangedAt: at},
			ActivityCapturedAt: at,
		},
	}
}

func drain(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch v := msg.(type) {
	case tea.BatchMsg:
		out := make([]tea.Msg, 0, len(v))
		for _, c := range v {
			out = append(out, c())
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func drainAll(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	var run func(tea.Cmd)
	run = func(next tea.Cmd) {
		if next == nil {
			return
		}
		msg := next()
		switch batch := msg.(type) {
		case tea.BatchMsg:
			for _, child := range batch {
				run(child)
			}
		case nil:
		default:
			out = append(out, msg)
		}
	}
	run(cmd)
	return out
}

func retainedWaiting(id, project, tmux string, dismissed bool) notify.Notification {
	n := notify.Notification{
		ID:        id,
		Source:    notify.SourceWaiting,
		Sticky:    true,
		CreatedAt: time.Unix(8000, 0),
		Origin: notify.Origin{
			ProjectKey:  project,
			TmuxSession: tmux,
			WorkDir:     "/tmp/" + project,
		},
		Transition: &notify.TransitionMetadata{
			Class:       notify.TransitionWaiting,
			LaneKey:     "shell:" + tmux,
			ProjectRoot: "/tmp/" + project,
		},
	}
	if dismissed {
		at := time.Unix(8100, 0)
		n.DismissedAt = &at
	}
	return n
}

func notificationMessages(all []tea.Msg) (posts []notify.PostMsg, dismisses []notify.DismissMsg) {
	for _, msg := range all {
		switch msg := msg.(type) {
		case notify.PostMsg:
			posts = append(posts, msg)
		case notify.DismissMsg:
			dismisses = append(dismisses, msg)
		}
	}
	return posts, dismisses
}

// Plain shells have no readable activity, so they must produce no observations
// at all — otherwise every ordinary zsh would announce lane changes nobody made.
func TestLaneObservationsSkipUnreadableAgents(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{WorkDir: "/tmp/repo"}}
	p.shells = []*ShellSession{
		{Name: "plain", TmuxName: "t1"},
		{Name: "shell-agent", TmuxName: "t2", Agent: &Agent{Type: AgentShell}},
		triggerShell("Claude 1", "t3", agentactivity.StateWorking, time.Now()),
	}
	obs := p.agentLaneObservations()
	if len(obs) != 1 || obs[0].Key != "shell:t3" {
		t.Fatalf("observations = %#v, want only the claude shell", obs)
	}
	if obs[0].Provider != "claude" || obs[0].Context != "repo" || obs[0].Label != "Claude 1" {
		t.Fatalf("identity = %#v", obs[0])
	}
	if obs[0].Origin.TmuxSession != "t3" || obs[0].Origin.ProjectKey != "repo" || obs[0].ProjectRoot != "/tmp/repo" {
		t.Fatalf("origin = %#v", obs[0].Origin)
	}
}

// The adapter's whole job: a settled blocked lane becomes a PostMsg, and the
// transition away becomes a DismissMsg for the very notification it posted.
func TestNotifyAgentTransitionsPostsAndSelfDismisses(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{WorkDir: "/tmp/repo"}}
	p.agentLaneTracker.Debounce = time.Second
	now := time.Unix(9000, 0)

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateWorking, now)}
	if cmd := p.notifyAgentTransitions(now); cmd != nil {
		t.Fatalf("baseline round emitted a command")
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateBlocked, now)}
	if cmd := p.notifyAgentTransitions(now.Add(time.Second)); cmd != nil {
		t.Fatalf("undebounced transition emitted a command")
	}
	msgs := drain(t, p.notifyAgentTransitions(now.Add(2*time.Second)))
	if len(msgs) != 1 {
		t.Fatalf("settled blocked emitted %d messages", len(msgs))
	}
	post, ok := msgs[0].(notify.PostMsg)
	if !ok {
		t.Fatalf("message = %T, want notify.PostMsg", msgs[0])
	}
	if post.Notification.Source != notify.SourceWaiting || !post.Notification.Sticky {
		t.Fatalf("notification = %#v", post.Notification)
	}
	if post.Notification.Title != "Claude 1 needs input" {
		t.Fatalf("title = %q", post.Notification.Title)
	}
	if post.Notification.Transition == nil || post.Notification.Transition.ProjectRoot != "/tmp/repo" {
		t.Fatalf("transition owner = %#v, want producer project root", post.Notification.Transition)
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateWorking, now)}
	p.notifyAgentTransitions(now.Add(10 * time.Second))
	msgs = drain(t, p.notifyAgentTransitions(now.Add(12*time.Second)))
	if len(msgs) != 1 {
		t.Fatalf("answering the prompt emitted %d messages", len(msgs))
	}
	dismiss, ok := msgs[0].(notify.DismissMsg)
	if !ok {
		t.Fatalf("message = %T, want notify.DismissMsg", msgs[0])
	}
	if dismiss.ID != post.Notification.ID {
		t.Fatalf("dismissed %q, want %q", dismiss.ID, post.Notification.ID)
	}
}

// Nested shells belong to the same set: leaving them out would make every
// sibling shell look like it had vanished, withdrawing live notifications.
func TestLaneObservationsIncludeNestedShells(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{WorkDir: "/tmp/repo"}}
	p.nestedByWorkDir = map[string][]*ShellSession{
		"/tmp/repo/wt": {triggerShell("Nested", "t9", agentactivity.StateWorking, time.Now())},
	}
	obs := p.agentLaneObservations()
	if len(obs) != 1 || obs[0].Key != "shell:t9" {
		t.Fatalf("observations = %#v", obs)
	}
}

func TestLaneProjectKeyUsesSharedProjectRootFromLinkedWorktree(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{WorkDir: "/tmp/sidecar-topic", ProjectRoot: "/tmp/sidecar"}}
	if got := p.laneProjectKey(); got != "sidecar" {
		t.Fatalf("lane project key = %q, want shared root key", got)
	}
}

func TestPluginUpdateDefersSameProjectSeedUntilInventoryAndDismissesOriginalOnRealLeave(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/repo", ProjectRoot: "/tmp/repo"}
	p.shellStartupLoading = true
	p.agentLaneTracker.Debounce = time.Second
	now := time.Unix(9000, 0)
	p.clock = func() time.Time { return now }
	seed := retainedWaiting("retained-wait", "repo", "t1", false)

	_, cmd := p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{seed}})
	if posts, dismisses := notificationMessages(drainAll(t, cmd)); len(posts) != 0 || len(dismisses) != 0 {
		t.Fatalf("seed update emitted posts=%v dismisses=%v", posts, dismisses)
	}
	_, cmd = p.Update(struct{}{})
	if _, dismisses := notificationMessages(drainAll(t, cmd)); len(dismisses) != 0 {
		t.Fatalf("pre-inventory update dismissed retained wait: %v", dismisses)
	}
	if len(p.pendingAgentLaneSeeds) != 1 {
		t.Fatalf("pending seeds = %d, want 1 until inventory is complete", len(p.pendingAgentLaneSeeds))
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateBlocked, now)}
	p.shellStartupLoading = false
	p.worktreesLoaded = true
	p.stateRestored = true
	_, cmd = p.Update(struct{}{})
	if posts, dismisses := notificationMessages(drainAll(t, cmd)); len(posts) != 0 || len(dismisses) != 0 {
		t.Fatalf("inventory reconciliation emitted posts=%v dismisses=%v", posts, dismisses)
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateWorking, now)}
	p.Update(struct{}{})
	now = now.Add(2 * time.Second)
	_, cmd = p.Update(struct{}{})
	_, dismisses := notificationMessages(drainAll(t, cmd))
	if len(dismisses) != 1 || dismisses[0].ID != seed.ID {
		t.Fatalf("real leave dismisses = %v, want original id %q", dismisses, seed.ID)
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateBlocked, now)}
	p.Update(struct{}{})
	now = now.Add(2 * time.Second)
	_, cmd = p.Update(struct{}{})
	posts, _ := notificationMessages(drainAll(t, cmd))
	if len(posts) != 1 || posts[0].Notification.ID == seed.ID {
		t.Fatalf("re-enter posts = %v, want one new waiting episode", posts)
	}
}

func TestPluginUpdateNeverSeedsForeignProjectWait(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/repo", ProjectRoot: "/tmp/repo"}
	p.stateRestored = true
	p.worktreesLoaded = true
	foreign := retainedWaiting("foreign-wait", "other", "foreign-tmux", false)

	_, seedCmd := p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{foreign}})
	_, nextCmd := p.Update(struct{}{})
	_, dismisses := notificationMessages(append(drainAll(t, seedCmd), drainAll(t, nextCmd)...))
	if len(dismisses) != 0 {
		t.Fatalf("foreign project wait was dismissed: %v", dismisses)
	}
	if len(p.pendingAgentLaneSeeds) != 0 {
		t.Fatalf("foreign project seed entered pending state: %#v", p.pendingAgentLaneSeeds)
	}
}

func TestPluginUpdateNeverSeedsForeignProjectWithSameBasename(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/one/repo", ProjectRoot: "/tmp/one/repo"}
	p.stateRestored = true
	p.worktreesLoaded = true
	foreign := retainedWaiting("same-name-foreign-wait", "repo", "foreign-tmux", false)
	foreign.Origin.WorkDir = "/tmp/two/repo"
	foreign.Transition.ProjectRoot = "/tmp/two/repo"

	_, seedCmd := p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{foreign}})
	_, nextCmd := p.Update(struct{}{})
	_, dismisses := notificationMessages(append(drainAll(t, seedCmd), drainAll(t, nextCmd)...))
	if len(dismisses) != 0 {
		t.Fatalf("same-basename foreign project wait was dismissed: %v", dismisses)
	}
	if len(p.pendingAgentLaneSeeds) != 0 {
		t.Fatalf("same-basename foreign seed survived ownership filtering: %#v", p.pendingAgentLaneSeeds)
	}
}

func TestPluginUpdateSeedsOnlyUnambiguousLegacyWaits(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/one/repo", ProjectRoot: "/tmp/one/repo"}
	p.stateRestored = true
	p.worktreesLoaded = true
	projectOnly := retainedWaiting("project-only", "repo", "project-only-tmux", false)
	projectOnly.Origin.WorkDir = ""
	projectOnly.Transition.ProjectRoot = ""
	workDirOnly := retainedWaiting("workdir-only", "", "workdir-only-tmux", false)
	workDirOnly.Origin.WorkDir = "/tmp/one/repo/subdir"
	workDirOnly.Transition.ProjectRoot = ""

	p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{projectOnly, workDirOnly}})
	_, cmd := p.Update(struct{}{})
	_, dismisses := notificationMessages(drainAll(t, cmd))
	if len(dismisses) != 1 || dismisses[0].ID != workDirOnly.ID {
		t.Fatalf("legacy dismisses = %v, want only unambiguous workdir id %q", dismisses, workDirOnly.ID)
	}
}

func TestPluginUpdateSeedsRemovedExternalWorktreeByStoredProjectOwner(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/main/repo", ProjectRoot: "/tmp/main/repo"}
	p.shellStartupLoading = true
	seed := retainedWaiting("removed-external-wait", "repo", "removed-tmux", false)
	seed.Origin.WorkDir = "/tmp/repo-topic"
	seed.Transition.ProjectRoot = "/tmp/main/repo"

	_, seedCmd := p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{seed}})
	_, preInventoryCmd := p.Update(struct{}{})
	if _, dismisses := notificationMessages(append(drainAll(t, seedCmd), drainAll(t, preInventoryCmd)...)); len(dismisses) != 0 {
		t.Fatalf("removed external wait dismissed before inventory: %v", dismisses)
	}

	// The linked worktree was removed before restart, so complete inventory is
	// intentionally empty. Its stored owning root still gives this project the
	// authority to retire the retained wait once absence becomes authoritative.
	p.shellStartupLoading = false
	p.worktreesLoaded = true
	p.stateRestored = true
	_, cmd := p.Update(struct{}{})
	_, dismisses := notificationMessages(drainAll(t, cmd))
	if len(dismisses) != 1 || dismisses[0].ID != seed.ID {
		t.Fatalf("removed external wait dismisses = %v, want stored id %q", dismisses, seed.ID)
	}
}

func TestPluginUpdatePreservesUserDismissedWaitUntilLeaveAndReenter(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/tmp/repo", ProjectRoot: "/tmp/repo"}
	p.stateRestored = true
	p.worktreesLoaded = true
	p.agentLaneTracker.Debounce = time.Second
	now := time.Unix(9000, 0)
	p.clock = func() time.Time { return now }
	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateBlocked, now)}
	seed := retainedWaiting("dismissed-wait", "repo", "t1", true)

	p.Update(notify.SeedLaneTrackersMsg{Notifications: []notify.Notification{seed}})
	_, cmd := p.Update(struct{}{})
	if posts, dismisses := notificationMessages(drainAll(t, cmd)); len(posts) != 0 || len(dismisses) != 0 {
		t.Fatalf("dismissed blocked episode re-emitted posts=%v dismisses=%v", posts, dismisses)
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateWorking, now)}
	p.Update(struct{}{})
	now = now.Add(2 * time.Second)
	_, cmd = p.Update(struct{}{})
	if _, dismisses := notificationMessages(drainAll(t, cmd)); len(dismisses) != 0 {
		t.Fatalf("leaving user-dismissed episode dismissed again: %v", dismisses)
	}

	p.shells = []*ShellSession{triggerShell("Claude 1", "t1", agentactivity.StateBlocked, now)}
	p.Update(struct{}{})
	now = now.Add(2 * time.Second)
	_, cmd = p.Update(struct{}{})
	posts, _ := notificationMessages(drainAll(t, cmd))
	if len(posts) != 1 || posts[0].Notification.ID == seed.ID {
		t.Fatalf("new blocked episode posts = %v, want one new wait", posts)
	}
}
