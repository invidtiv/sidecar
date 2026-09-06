package sessionrestore

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The local binding of the executor to this machine.
//
// It lives here, and not in the CLI, because the CLI and the automatic startup
// restore must be the same restore. Two bindings would be two answers to "what
// does restoring actually do", and the difference would show up exactly once, in
// the situation nobody can reproduce.

// LocalDepsOptions tunes the timeouts of the local binding.
type LocalDepsOptions struct {
	// Namespace is the tmux socket path the restore is scoped to.
	Namespace string
	// ShellReady bounds the wait for a recreated shell to settle before a
	// resume is sent into it. Zero uses a sensible default.
	ShellReady time.Duration
	// ResumeReady bounds the wait for the provider to identify itself and
	// become ready. Zero uses a sensible default. There is no implicit
	// unbounded wait anywhere in this path.
	ResumeReady time.Duration
}

// LocalDeps binds the executor to real tmux, real shell creation, and real
// provider resume.
func LocalDeps(opts LocalDepsOptions) Deps {
	if opts.ShellReady <= 0 {
		opts.ShellReady = 30 * time.Second
	}
	if opts.ResumeReady <= 0 {
		opts.ResumeReady = 60 * time.Second
	}
	collector := Collector{Namespace: opts.Namespace}
	svc := agentcontrol.Service{Terminal: agentcontrol.NewLocalTerminal()}

	return Deps{
		Live: func(session string) LiveState {
			if !workspaceops.SessionExists(session) {
				return LiveAbsent
			}
			// A live name is only "ours" if the session itself says so. Deciding
			// this from the name would make the collision refusal decorative:
			// anything could take a Sidecar-shaped name and be converged onto.
			if collector.ManagedSessionOrDefault(context.Background(), session) {
				return LiveManaged
			}
			return LiveForeign
		},
		CurrentServer: collector.ServerIDOrDefault,
		CreateShell: func(_ context.Context, step Step) error {
			// CreateShell, not CreateManagedShell: the manifest record already
			// exists and is the thing being restored. Adding it again would fail,
			// and adding it with a fresh CreatedAt would discard the identity
			// every other fence in the system is keyed on.
			_, err := workspaceops.CreateShell(workspaceops.ShellSpec{
				WorkDir:                  step.WorkDir,
				SessionName:              step.Session,
				DisplayName:              step.Name,
				PreserveRecoveryEvidence: true,
			})
			return err
		},
		NoteLive: func(step Step) {
			// Re-stamp the eligibility marker to the server the shell is running
			// in now. Without this a restored shell keeps the marker of the
			// server it died in, and closing it later reads as another server
			// death rather than as a terminal someone closed.
			server := collector.ServerIDOrDefault()
			if server == "" || step.ProjectRoot == "" {
				return
			}
			observe := shellstate.ObserveLiveAtPath
			if step.Action == ActionPrefillResume {
				observe = shellstate.ObserveRestoredLiveAtPath
			}
			_, _ = observe(step.ManifestPath(), server,
				[]shellstate.Identity{{TmuxName: step.Session, Namespace: opts.Namespace}}, time.Now().UTC())
		},
		ResumePlanFor: func(step Step) (agentsession.ResumePlan, error) {
			return ResumePlanFor(step, opts.Namespace)
		},
		PrefillPlanFor: func(step Step) (agentsession.ResumePlan, error) {
			return PrefillPlanFor(step, opts.Namespace)
		},
		ResumeAgent: func(ctx context.Context, step Step, plan agentsession.ResumePlan) error {
			target := agentcontrol.Target{
				Host:      "local",
				Project:   step.Project,
				Session:   step.Session,
				Name:      step.Name,
				Namespace: opts.Namespace,
			}
			ready, err := svc.WaitShellReady(ctx, target, opts.ShellReady)
			if err != nil {
				return err
			}
			_, err = svc.StartResume(ctx, agentcontrol.ResumeRequest{
				Target:  ready.Target,
				Plan:    plan,
				Timeout: opts.ResumeReady,
			})
			return err
		},
		MarkPrefilled: func(step Step) (bool, error) {
			return shellstate.MarkRestorePrefilledAtPath(step.ManifestPath(), shellstate.Identity{
				TmuxName: step.Session, Namespace: opts.Namespace,
			}, time.Now().UTC())
		},
		CompletePrefilled: func(step Step) error {
			return shellstate.CompleteRestorePrefillAtPath(step.ManifestPath(), shellstate.Identity{
				TmuxName: step.Session, Namespace: opts.Namespace,
			}, time.Now().UTC())
		},
		PreparePrefill: func(ctx context.Context, step Step) (func(context.Context, agentsession.ResumePlan) error, error) {
			target := agentcontrol.Target{Host: "local", Project: step.Project, Session: step.Session, Name: step.Name, Namespace: opts.Namespace}
			ready, err := svc.WaitShellReady(ctx, target, opts.ShellReady)
			if err != nil {
				return nil, err
			}
			if !ready.ShellReady || ready.CopyMode || ready.PaneCount != 1 {
				return nil, fmt.Errorf("managed pane is not an idle foreground shell")
			}
			emptyInput, provenScreen, err := prefillInputEmpty(ctx, ready.PaneID)
			if err != nil {
				return nil, err
			}
			if !emptyInput {
				return nil, fmt.Errorf("shell input line is not empty; left it unchanged")
			}
			empty, err := svc.Terminal.Inspect(ctx, ready.Target)
			if err != nil {
				return nil, err
			}
			_, _, checkedScreen, screenErr := prefillPaneScreen(ctx, ready.PaneID)
			if screenErr != nil {
				return nil, screenErr
			}
			if empty.PaneID != ready.PaneID || empty.PanePID != ready.PanePID || empty.ServerPID != ready.ServerPID || !empty.ShellReady || empty.CopyMode || empty.Dead || strings.Join(checkedScreen, "\n")+"\n" != provenScreen {
				return nil, fmt.Errorf("shell changed while its empty input line was checked; left it unchanged")
			}
			return func(writeCtx context.Context, plan agentsession.ResumePlan) error {
				current, inspectErr := svc.Terminal.Inspect(writeCtx, empty.Target)
				if inspectErr != nil {
					return inspectErr
				}
				_, _, currentScreen, screenErr := prefillPaneScreen(writeCtx, empty.PaneID)
				if screenErr != nil {
					return screenErr
				}
				if current.PaneID != empty.PaneID || current.PanePID != empty.PanePID || current.ServerPID != empty.ServerPID || !current.ShellReady || current.CopyMode || current.Dead || strings.Join(currentScreen, "\n")+"\n" != provenScreen {
					return fmt.Errorf("shell input changed after the empty prompt was checked; left it unchanged")
				}
				return workspaceops.TypeInShell(writeCtx, empty.PaneID, agentcatalog.DisplayCommand(plan.Argv))
			}, nil
		},
	}
}

func prefillInputEmpty(ctx context.Context, pane string) (bool, string, error) {
	x, y, screen, err := prefillPaneScreen(ctx, pane)
	if err != nil {
		return false, "", err
	}
	if !renderedInputRightEmpty(screen, x, y) {
		return false, "", nil
	}
	if err := tty.SendKeys(pane, tty.KeySpec{Value: "Left"}); err != nil {
		return false, "", err
	}
	afterX, afterY, afterScreen, err := waitPrefillPaneCursor(ctx, pane, x, y)
	if err != nil {
		return false, "", err
	}
	if afterX != x || afterY != y {
		if restoreErr := tty.SendKeys(pane, tty.KeySpec{Value: "Right"}); restoreErr != nil {
			return false, "", restoreErr
		}
		_, _, _, _ = waitPrefillPaneCursor(ctx, pane, afterX, afterY)
		return false, "", nil
	}
	if strings.Join(afterScreen, "\n") != strings.Join(screen, "\n") {
		return false, "", nil
	}
	return true, strings.Join(afterScreen, "\n") + "\n", nil
}

func prefillPaneScreen(ctx context.Context, pane string) (int, int, []string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane, "#{cursor_x}|#{cursor_y}|#{pane_height}").Output()
	if err != nil {
		return 0, 0, nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
	if len(parts) != 3 {
		return 0, 0, nil, fmt.Errorf("unexpected pane cursor metadata")
	}
	x, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, nil, err
	}
	y, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, nil, err
	}
	height, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, nil, err
	}
	screen, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", pane, "-S", "0", "-E", strconv.Itoa(height-1)).Output()
	if err != nil {
		return 0, 0, nil, err
	}
	return x, y, strings.Split(strings.TrimSuffix(string(screen), "\n"), "\n"), nil
}

func renderedInputRightEmpty(rows []string, x, y int) bool {
	if y < 0 || y >= len(rows) || x < 0 {
		return false
	}
	if ansi.StringWidth(strings.TrimRight(rows[y], " \r")) > x {
		return false
	}
	for _, row := range rows[y+1:] {
		if strings.TrimSpace(row) != "" {
			return false
		}
	}
	return true
}

func waitPrefillPaneCursor(ctx context.Context, pane string, x, y int) (int, int, []string, error) {
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0, 0, nil, ctx.Err()
		case <-deadline.C:
			return prefillPaneScreen(ctx, pane)
		case <-ticker.C:
			nx, ny, rows, err := prefillPaneScreen(ctx, pane)
			if err != nil || nx != x || ny != y {
				return nx, ny, rows, err
			}
		}
	}
}
