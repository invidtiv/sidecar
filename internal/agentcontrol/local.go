package agentcontrol

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}
type execRunner struct{}

func (execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
}

type LocalTerminal struct {
	Runner Runner
	Paste  func(string, string) error
	Key    func(string, string) error
	Now    func() time.Time
}

func NewLocalTerminal() *LocalTerminal {
	return &LocalTerminal{Runner: execRunner{}, Paste: tty.SendPasteToTmux, Key: tty.SendKeyToTmux, Now: time.Now}
}

const paneFormat = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_dead}\x1f#{pane_in_mode}\x1f#{pane_current_command}\x1f#{pane_title}\x1f#{pid}"

func (t *LocalTerminal) Inspect(ctx context.Context, target Target) (Snapshot, error) {
	if t == nil {
		return Snapshot{}, fmt.Errorf("nil local terminal")
	}
	runner := t.Runner
	if runner == nil {
		runner = execRunner{}
	}
	if target.Namespace != "" && target.Namespace != tmuxenv.Namespace() {
		return Snapshot{}, fmt.Errorf("target namespace %q is not local tmux namespace %q", target.Namespace, tmuxenv.Namespace())
	}
	out, err := runner.Run(ctx, "list-panes", "-t", target.Session, "-F", paneFormat)
	if err != nil {
		return Snapshot{}, tmuxError(err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		return Snapshot{Target: target, PaneCount: len(lines)}, nil
	}
	parts := strings.Split(lines[0], "\x1f")
	if len(parts) != 7 {
		return Snapshot{}, fmt.Errorf("unexpected tmux pane metadata")
	}
	pid, _ := strconv.Atoi(parts[1])
	serverPID, _ := strconv.Atoi(parts[6])
	inc := tmuxserver.Combine(tmuxserver.Socket(), serverPID)
	target.Host = "local"
	target.Namespace = tmuxenv.Namespace()
	target.PaneID = parts[0]
	target.PanePID = pid
	target.ServerIncarnation = inc.String()
	screenOut, captureErr := runner.Run(ctx, "capture-pane", "-p", "-e", "-N", "-S", "-80", "-t", target.PaneID)
	if captureErr != nil {
		return Snapshot{}, tmuxError(captureErr, screenOut)
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	processIdentity := agentactivity.ResolveForegroundProcess(pid)
	return Snapshot{Target: target, Dead: parts[2] == "1", CopyMode: parts[3] != "0", PaneCount: 1, CurrentCommand: parts[4], ProcessIdentity: processIdentity, ShellReady: agentactivity.ForegroundShellReady(pid, parts[4]), Title: parts[5], Screen: string(screenOut), CapturedAt: now}, nil
}

func (t *LocalTerminal) Launch(ctx context.Context, snap Snapshot, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("launch target or argv is empty")
	}
	current, err := t.Inspect(ctx, snap.Target)
	if err != nil {
		return err
	}
	if !sameOccupant(snap.Target, current.Target) {
		return &Error{Code: ErrReplaced, Message: "managed pane was replaced before provider launch", Target: &snap.Target}
	}
	if err := shellReady(current); err != nil {
		return err
	}
	return t.send(ctx, current, quoteArgv(argv))
}

// Submit is the M0/M2 shared ordered text sender: revalidate the pinned pane,
// pass the exact text through tmux's bracketed-paste-aware buffer path, then
// submit Enter as a separate ordered key.
func (t *LocalTerminal) Submit(ctx context.Context, snap Snapshot, text string) error {
	current, err := t.Inspect(ctx, snap.Target)
	if err != nil {
		return err
	}
	if !sameOccupant(snap.Target, current.Target) {
		return &Error{Code: ErrReplaced, Message: "managed pane was replaced before input", Target: &snap.Target}
	}
	return t.send(ctx, current, text)
}

func (t *LocalTerminal) send(_ context.Context, snap Snapshot, text string) error {
	paste := t.Paste
	if paste == nil {
		paste = tty.SendPasteToTmux
	}
	key := t.Key
	if key == nil {
		key = tty.SendKeyToTmux
	}
	if err := paste(snap.PaneID, text); err != nil {
		return fmt.Errorf("type provider launch: %w", err)
	}
	if err := key(snap.PaneID, "Enter"); err != nil {
		return fmt.Errorf("submit provider launch: %w", err)
	}
	return nil
}

func quoteArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func tmuxError(err error, output []byte) error {
	if msg := strings.TrimSpace(string(output)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
