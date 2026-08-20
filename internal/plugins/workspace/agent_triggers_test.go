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
	if obs[0].Origin.TmuxSession != "t3" || obs[0].Origin.ProjectKey != "repo" {
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
