package agentcontrol

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
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
	Runner  Runner
	Paste   func(string, string) error
	Key     func(string, string) error
	Literal func(string, string) error
	Now     func() time.Time

	// manager is the pooled tmux control-mode client used for sustained
	// targeted observation. It is created on first use so a one-shot list, get,
	// or read never pays for a control client it will not use, and released by
	// Close.
	managerOnce sync.Once
	manager     *tty.ControlManager
	// newManager is the injection point tests use to point the pool at their
	// own tmux socket.
	newManager func() *tty.ControlManager
}

func NewLocalTerminal() *LocalTerminal {
	return &LocalTerminal{Runner: execRunner{}, Paste: tty.SendPasteToTmux, Key: tty.SendKeyToTmux, Literal: tty.SendLiteralToTmux, Now: time.Now}
}

// Close releases the control-mode pool. A caller that ran a wait must call it;
// a caller that only inspected may call it harmlessly.
func (t *LocalTerminal) Close() {
	if t == nil {
		return
	}
	t.managerOnce.Do(func() {})
	if t.manager != nil {
		t.manager.Stop()
		t.manager = nil
	}
}

func (t *LocalTerminal) controlManager() *tty.ControlManager {
	t.managerOnce.Do(func() {
		factory := t.newManager
		if factory == nil {
			factory = tty.NewControlManager
		}
		t.manager = factory()
	})
	return t.manager
}

// Signal subscribes to the pooled control manager for one pinned pane.
//
// Focused is set because the manager schedules captures only for a focused,
// visible subscription; it is not application focus and it moves nothing. Width
// and Height are deliberately left at zero: a sized subscription would issue
// `refresh-client -C`, and a headless command resizing the user's pane is the
// one thing an observer must never do.
func (t *LocalTerminal) Signal(ctx context.Context, snap Snapshot) (<-chan Signal, func(), error) {
	if snap.Session == "" || snap.PaneID == "" {
		return nil, nil, fmt.Errorf("cannot observe an unpinned target")
	}
	// After Close the pool is gone and the once is spent, so controlManager
	// returns nil. Refusing here degrades the watch to bounded polling — the
	// documented fallback — instead of dereferencing a stopped manager.
	manager := t.controlManager()
	if manager == nil {
		return nil, nil, fmt.Errorf("control-mode pool is closed")
	}
	signals := make(chan Signal, 1)
	// The channel is closed exactly once, under the same lock every send takes,
	// so a control callback racing teardown cannot send on a closed channel.
	// Closing rather than merely stopping is what tells the watch loop the
	// control client is gone and it must fall back to bounded polling.
	var mu sync.Mutex
	done := false
	shut := func() {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		done = true
		close(signals)
	}

	now := t.Now
	if now == nil {
		now = time.Now
	}
	subscription, err := manager.Subscribe(tty.ControlRequest{
		Session:    snap.Session,
		Pane:       snap.PaneID,
		Scrollback: DetectionScrollback,
		Visible:    true,
		Focused:    true,
		OnSnapshot: func(control tty.ControlSnapshot) {
			mu.Lock()
			defer mu.Unlock()
			if done {
				return
			}
			select {
			case signals <- Signal{Screen: control.Output, Title: control.PaneTitle, CurrentCommand: control.CurrentCommand, At: now()}:
			default:
				// A watcher that has not drained the previous signal already
				// knows the pane changed. Dropping is correct, and it is what
				// keeps an output burst from backpressuring the control reader.
			}
		},
		OnFallback: func(error) { shut() },
	})
	if err != nil {
		return nil, nil, err
	}

	// Cancellation has to reach the subscription even when the watch loop is
	// blocked between ticks, so the context is joined here rather than polled.
	released := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-released:
		}
		subscription.Close()
		shut()
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			close(released)
			subscription.Close()
			shut()
		})
	}
	return signals, stop, nil
}

var paneFormat = tmuxformat.Fields("pane_id", "pane_pid", "pane_dead", "pane_in_mode", "pane_current_command", "pane_title", "pid", "pane_height")

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
	parts := tmuxformat.Split(lines[0])
	if len(parts) != 8 {
		return Snapshot{}, fmt.Errorf("unexpected tmux pane metadata")
	}
	pid, _ := strconv.Atoi(parts[1])
	serverPID, _ := strconv.Atoi(parts[6])
	paneHeight, _ := strconv.Atoi(parts[7])
	inc := tmuxserver.Combine(tmuxserver.Socket(), serverPID)
	target.Host = "local"
	target.Namespace = tmuxenv.Namespace()
	target.PaneID = parts[0]
	target.PanePID = pid
	target.ServerPID = serverPID
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
	return Snapshot{Target: target, Dead: parts[2] == "1", CopyMode: parts[3] != "0", PaneCount: 1, CurrentCommand: parts[4], ProcessIdentity: processIdentity, ShellReady: agentactivity.ForegroundShellReady(pid, parts[4]), Title: parts[5], Screen: string(screenOut), PaneHeight: paneHeight, CapturedAt: now}, nil
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
	return t.apply(current, tty.PromptSteps(quoteArgv(argv)))
}

// Submit is the shared ordered text sender: revalidate the pinned pane, pass
// the exact text through tmux's bracketed-paste-aware buffer path, then submit
// Enter as a separate ordered key. The sequence itself is tty.PromptSteps, the
// same one the embedded terminal produces, so a headless prompt and a typed one
// deliver identical bytes.
func (t *LocalTerminal) Submit(ctx context.Context, snap Snapshot, text string) error {
	current, err := t.revalidate(ctx, snap, "input")
	if err != nil {
		return err
	}
	return t.apply(current, tty.PromptSteps(text))
}

// SendKeys encodes the complete logical-key list before it writes anything.
// Validation precedes revalidation precedes the first byte: a caller answering
// a blocked agent's UI is never left having delivered part of a sequence.
func (t *LocalTerminal) SendKeys(ctx context.Context, snap Snapshot, names []string) error {
	specs, err := tty.EncodeLogicalKeys(names)
	if err != nil {
		return &Error{Code: ErrNotReady, Message: err.Error(), Target: &snap.Target, Err: err}
	}
	current, err := t.revalidate(ctx, snap, "keys")
	if err != nil {
		return err
	}
	steps := make([]tty.InputStep, 0, len(specs))
	for _, spec := range specs {
		steps = append(steps, tty.KeyStep(spec))
	}
	return t.apply(current, steps)
}

func (t *LocalTerminal) revalidate(ctx context.Context, snap Snapshot, what string) (Snapshot, error) {
	current, err := t.Inspect(ctx, snap.Target)
	if err != nil {
		return Snapshot{}, err
	}
	if !sameOccupant(snap.Target, current.Target) {
		return Snapshot{}, &Error{Code: ErrReplaced, Message: "managed pane was replaced before " + what, Target: &snap.Target}
	}
	return current, nil
}

// apply performs an ordered input sequence against the pinned pane. The
// per-step functions stay injectable so tests can observe exactly what was
// written without a tmux server.
func (t *LocalTerminal) apply(snap Snapshot, steps []tty.InputStep) error {
	paste, key, literal := t.Paste, t.Key, t.Literal
	if paste == nil {
		paste = tty.SendPasteToTmux
	}
	if key == nil {
		key = tty.SendKeyToTmux
	}
	if literal == nil {
		literal = tty.SendLiteralToTmux
	}
	for _, step := range steps {
		var err error
		switch {
		case step.Kind == tty.InputText:
			err = paste(snap.PaneID, step.Text)
		case step.Key.Literal:
			err = literal(snap.PaneID, step.Key.Value)
		default:
			err = key(snap.PaneID, step.Key.Value)
		}
		if err != nil {
			return fmt.Errorf("send terminal input: %w", err)
		}
	}
	return nil
}

// Capture is the passive read. Each source is one capture-pane invocation and
// nothing else — no scrolling, no copy mode, no resize.
func (t *LocalTerminal) Capture(ctx context.Context, snap Snapshot, req ReadRequest) (string, error) {
	runner := t.Runner
	if runner == nil {
		runner = execRunner{}
	}
	if snap.PaneID == "" {
		return "", fmt.Errorf("cannot read an unpinned target")
	}
	args := []string{"capture-pane", "-p"}
	switch req.Source {
	case SourceVisible:
	case SourceDetection:
		// The detector's own slice, byte for byte: -e -N -S -80 is what Inspect
		// captures, so an argument about a status verdict is settled against
		// the evidence the verdict used.
		args = append(args, "-e", "-N", "-S", "-"+strconv.Itoa(DetectionScrollback))
	case SourceRecent, SourceRecentUnwrapped:
		lines := req.Lines
		if lines <= 0 {
			lines = DetectionScrollback
		}
		args = append(args, "-S", "-"+strconv.Itoa(lines))
		if req.Source == SourceRecentUnwrapped {
			// -J joins a line that tmux soft-wrapped back into one line, which
			// is what makes an agent's answer readable as text.
			args = append(args, "-J")
		}
	default:
		return "", fmt.Errorf("source %q is not a terminal capture", req.Source)
	}
	if req.ANSI && req.Source != SourceDetection {
		args = append(args, "-e")
	}
	args = append(args, "-t", snap.PaneID)
	out, err := runner.Run(ctx, args...)
	if err != nil {
		return "", tmuxError(err, out)
	}
	return lastLines(string(out), req.Lines), nil
}

// lastLines bounds a capture to its most recent limit lines. The tail is what a
// caller asking for "the last 40 lines" means; capture-pane's -S already chose
// how far back to start, and trimming from the front would return the oldest.
func lastLines(text string, limit int) string {
	if limit <= 0 || text == "" {
		return text
	}
	trailingNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) <= limit {
		return text
	}
	out := strings.Join(lines[len(lines)-limit:], "\n")
	if trailingNewline {
		out += "\n"
	}
	return out
}

// ValidateKeys reports whether every name in a logical-key sequence can be
// encoded, without touching a terminal. The CLI uses it to answer a typo as a
// usage error before it resolves a target or spawns tmux.
func ValidateKeys(names []string) error {
	_, err := tty.EncodeLogicalKeys(names)
	return err
}

// LogicalKeyVocabulary is the documented named-key allowlist.
func LogicalKeyVocabulary() []string { return tty.LogicalKeyVocabulary() }

// quoteArgv delegates to the catalog so the argv-to-shell boundary has one
// implementation shared by the terminal adapter, the Conversations resume path,
// and anything else that must hand a command line to a shell.
func quoteArgv(argv []string) string { return agentcatalog.ShellCommand(argv) }

func tmuxError(err error, output []byte) error {
	if msg := strings.TrimSpace(string(output)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
