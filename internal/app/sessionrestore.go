package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/sessionrestore"
	"github.com/marcus/sidecar/internal/startuptrace"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// Automatic cold restore, after the first frame.
//
// The restore is the same planner and executor `sidecar session status/restore`
// run — the plan requires it, and the reason is not tidiness: two
// implementations would be two answers to "what is restorable", and the one the
// user read in the CLI would not be the one that ran at startup.
//
// Everything here happens strictly after the first ready frame. That is not a
// preference either. The restore reads every project's manifest, asks tmux for
// its inventory, and may create sessions; doing any of it in Init would put a
// filesystem walk and a subprocess spawn in front of the first paint, on a
// machine where an endpoint security agent can make each of those expensive.
// The startup trace has to show no tmux spawn and no restore write before
// `first ready frame`, and the latch is what guarantees it rather than the
// scheduler happening to cooperate.
//
// Under the default `ask` policy this restores shells and layout and then posts
// exactly one grouped summary. It never resumes a conversation on its own: a
// resume can spend money and change a repository, and a reboot is not consent.

// SessionRestoredMsg reports a completed automatic restore.
type SessionRestoredMsg struct {
	Result sessionrestore.Result
	// Pending are the conversations that could be resumed and are waiting on the
	// user. They are named in the summary rather than acted on.
	Pending []sessionrestore.Step
	Err     error
}

// sessionRestoreHost owns the lifetime of the restore so shutdown can cancel a
// run that is mid-flight rather than leaving it creating sessions into a
// process that is going away.
var sessionRestoreHost struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// startSessionRestoreOnce makes the restore fire on the first WindowSizeMsg and
// no later one, so a terminal resize does not schedule a second restore.
var startSessionRestoreOnce sync.Once

// sessionRestoreGate is the barrier the restore waits behind, indirected so a
// test can prove the ordering rather than assert it in a comment. In production
// it is the first-ready-frame latch and nothing else.
var sessionRestoreGate = func() <-chan struct{} { return firstReadyFrameLatch.wait() }

// sessionRestoreWork is the effectful half, indirected for the same reason: a
// test needs to observe whether any work happened before the gate opened, and
// the only honest way to observe that is to be the work.
var sessionRestoreWork = runSessionRestoreWork

// ShutdownSessionRestore cancels an in-flight automatic restore.
func ShutdownSessionRestore() {
	sessionRestoreHost.mu.Lock()
	cancel := sessionRestoreHost.cancel
	sessionRestoreHost.cancel = nil
	sessionRestoreHost.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// restoreSessionsCmd returns the post-first-frame restore, or nil when there is
// nothing this configuration would ever do.
//
// Returning nil in the disabled case matters: it means a user who has turned
// restore off pays no goroutine, no manifest read, and no tmux call at all.
func restoreSessionsCmd(cfg *config.Config) tea.Cmd {
	if cfg == nil {
		return nil
	}
	section := cfg.Plugins.Workspace.SessionRestore
	// recreateShells off means there is nothing to schedule, whatever
	// resumeAgents says. That is not a shortcut: the planner refuses to
	// recreate before it ever considers an agent, so a shell that is not being
	// recreated cannot host a resume and cannot produce a pending confirmation
	// either. Scheduling a command that provably cannot act would cost a
	// goroutine parked on the first-frame latch for the life of the process.
	if !section.RecreateShells {
		return nil
	}
	mode, err := sessionrestore.ParseResumeMode(section.ResumeAgents)
	if err != nil {
		mode = sessionrestore.ResumeAsk
	}
	restoreCfg := sessionrestore.Config{RecreateShells: section.RecreateShells, ResumeAgents: mode}

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		sessionRestoreHost.mu.Lock()
		if prev := sessionRestoreHost.cancel; prev != nil {
			prev()
		}
		sessionRestoreHost.cancel = cancel
		sessionRestoreHost.mu.Unlock()

		// Nothing above this point touches the filesystem, tmux, or a provider.
		// Everything that does is on the far side of the gate.
		select {
		case <-sessionRestoreGate():
		case <-ctx.Done():
			return nil
		}
		return sessionRestoreWork(ctx, restoreCfg)
	}
}

// runSessionRestoreWork is everything the restore actually does, and every line
// of it runs after the first ready frame.
func runSessionRestoreWork(ctx context.Context, restoreCfg sessionrestore.Config) tea.Msg {
	defer startuptrace.Begin("session restore")()

	collector := sessionrestore.Collector{
		StateDir:  config.StateDir(),
		Namespace: tmuxenv.Namespace(),
	}
	// Startup follows configuration rather than an explicit request, and it is
	// never Confirmed: an `auto` policy is the user's standing authorization and
	// resumes, while `ask` produces a pending list and runs nothing.
	in, err := collector.Collect(ctx, restoreCfg, sessionrestore.Request{Startup: true})
	if err != nil {
		return SessionRestoredMsg{Err: err}
	}

	plan := sessionrestore.Build(in)
	if len(plan.Executable()) == 0 && len(plan.PendingConfirmation()) == 0 {
		// Nothing to do and nothing to ask about. Staying silent here is
		// deliberate: an ordinary restart where every shell is still running is
		// the common case, and a notification for it would be noise.
		return nil
	}

	result := sessionrestore.Execute(ctx, plan, sessionrestore.LocalDeps(sessionrestore.LocalDepsOptions{
		Namespace: collector.Namespace,
	}))
	return SessionRestoredMsg{Result: result, Pending: plan.PendingConfirmation()}
}

// handleSessionRestored turns a completed restore into one grouped summary.
func (m *Model) handleSessionRestored(msg SessionRestoredMsg) tea.Cmd {
	if msg.Err != nil {
		slog.Warn("session restore", "error", msg.Err)
		return nil
	}
	title, body, targets := summariseRestore(msg)
	if title == "" {
		return nil
	}
	return PostNotification(notify.Notification{
		Source:   notify.SourceSession,
		Severity: notify.SeverityInfo,
		// Sticky because a pending resume is a question, and a question that
		// counts down and disappears has not been asked.
		Sticky:  len(msg.Pending) > 0,
		Title:   title,
		Body:    body,
		Targets: targets,
	})
}

// summariseRestore builds one grouped summary rather than one notification per
// shell. A reboot can produce a dozen restored shells, and a dozen toasts is a
// worse answer to "what happened" than one line that counts them.
func summariseRestore(msg SessionRestoredMsg) (title, body string, targets []notify.Target) {
	counts := msg.Result.Counts()
	restored := counts[sessionrestore.StatusRestored] + counts[sessionrestore.StatusResumed]
	resumed := counts[sessionrestore.StatusResumed]
	failed := counts[sessionrestore.StatusFailed]
	refused := counts[sessionrestore.StatusRefused]

	if restored == 0 && failed == 0 && refused == 0 && len(msg.Pending) == 0 {
		return "", "", nil
	}

	var parts []string
	if restored > 0 {
		parts = append(parts, fmt.Sprintf("%s restored", plural(restored, "shell")))
	}
	if resumed > 0 {
		parts = append(parts, fmt.Sprintf("%s resumed", plural(resumed, "conversation")))
	}
	if refused > 0 {
		parts = append(parts, fmt.Sprintf("%s refused", plural(refused, "shell")))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%s failed", plural(failed, "shell")))
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to restore")
	}
	title = "Session restore: " + strings.Join(parts, ", ")

	var lines []string
	for _, o := range msg.Result.Outcomes {
		switch o.Status {
		case sessionrestore.StatusRestored, sessionrestore.StatusResumed, sessionrestore.StatusRefused, sessionrestore.StatusFailed:
			label := o.Step.Name
			if label == "" {
				label = o.Step.Session
			}
			lines = append(lines, fmt.Sprintf("%s: %s", label, o.Detail))
			targets = append(targets, notify.Target{Kind: notify.TargetSession, Value: o.Step.Session, Project: o.Step.Project})
		}
	}
	if len(msg.Pending) > 0 {
		names := make([]string, 0, len(msg.Pending))
		for _, s := range msg.Pending {
			label := s.Name
			if label == "" {
				label = s.Session
			}
			names = append(names, label)
		}
		lines = append(lines, fmt.Sprintf(
			"%s can be resumed: %s. Run `sidecar session restore --agents --yes` to resume them.",
			plural(len(msg.Pending), "conversation"), strings.Join(names, ", ")))
	}
	if failed > 0 {
		lines = append(lines, "Failed steps are retryable; no shell record was removed.")
	}
	return title, strings.Join(lines, "\n"), targets
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
