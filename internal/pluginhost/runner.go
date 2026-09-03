package pluginhost

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/procgroup"
)

// RunSpec is one invocation of a provider executable. It is deliberately a
// value with no callbacks: everything the child sees is decided before it
// starts, and nothing about the host leaks in.
type RunSpec struct {
	// Argv is executed directly. No shell, ever. Argv[0] may be an absolute
	// path or resolve through PATH.
	Argv []string
	// Dir is a neutral Sidecar config directory, never a selected repository.
	Dir string
	// Env is the complete environment. Nothing is inherited implicitly.
	Env []string
	// Stdin is written once and then closed.
	Stdin []byte
	// MaxStdout bounds what is retained from stdout. The stream keeps being
	// drained past the bound so the child cannot block on a full pipe; the
	// excess is counted and discarded.
	//
	// There is no stderr equivalent, and that is the contract, not an
	// omission: stderr is never retained at all, and stopping its drain at a
	// byte bound would deadlock a chatty provider against a full pipe.
	MaxStdout int
}

// RunResult is what an invocation produced. Stderr content is never present:
// only its byte count, which is all the protocol allows Sidecar to keep.
type RunResult struct {
	Stdout          []byte
	StdoutBytes     int
	StdoutTruncated bool
	StderrBytes     int
	ExitCode        int
	Duration        time.Duration
	// TimedOut reports that the process group was killed because ctx expired
	// or was canceled, rather than the child exiting on its own.
	TimedOut bool
}

// Runner is the process adapter. The default implementation spawns a real
// child; tests substitute a fake to exercise the protocol layer without a
// process, and the executable fixture tests exercise the real one.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

// ExecRunner runs a real child process in its own process group.
type ExecRunner struct{}

var _ Runner = ExecRunner{}

// Run starts the command, writes stdin, drains stdout and stderr concurrently
// into bounded sinks, and waits. On timeout or cancellation it kills the whole
// process group — so a forked descendant dies with it — then finishes draining
// and reaps.
//
// Deliberately not exec.CommandContext: that kills only the direct child, which
// leaves a descendant holding the stdout pipe and hangs the drain forever.
func (ExecRunner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if len(spec.Argv) == 0 {
		return RunResult{}, errors.New("empty argv")
	}
	started := time.Now()

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	procgroup.Set(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RunResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, err
	}

	if err := cmd.Start(); err != nil {
		return RunResult{Duration: time.Since(started)}, err
	}

	// The watchdog owns the kill. It fires exactly once and always stops when
	// the invocation finishes, so no goroutine outlives the call.
	done := make(chan struct{})
	killed := make(chan struct{})
	var killOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
			killOnce.Do(func() {
				close(killed)
				procgroup.Kill(cmd)
			})
		case <-done:
		}
	}()

	go func() {
		// A child that never reads stdin would block this write forever; the
		// watchdog's kill closes the pipe and releases it. The error is
		// deliberately ignored — a child that ignores its request will fail the
		// response check anyway.
		_, _ = stdin.Write(spec.Stdin)
		_ = stdin.Close()
	}()

	var wg sync.WaitGroup
	var out boundedSink
	var errSink countingSink
	out.limit = spec.MaxStdout
	wg.Add(2)
	go func() { defer wg.Done(); out.drain(stdout) }()
	go func() { defer wg.Done(); errSink.drain(stderr) }()
	wg.Wait()

	waitErr := cmd.Wait()
	close(done)

	result := RunResult{
		Stdout:          out.buf,
		StdoutBytes:     out.total,
		StdoutTruncated: out.total > out.limit,
		StderrBytes:     errSink.total,
		Duration:        time.Since(started),
	}
	select {
	case <-killed:
		result.TimedOut = true
	default:
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, waitErr
	}
	return result, nil
}

// boundedSink keeps the first limit bytes and counts the rest. It never stops
// reading: a child blocked on a full stdout pipe would never exit, and an
// invocation that cannot exit cannot be reaped.
type boundedSink struct {
	limit int
	buf   []byte
	total int
}

func (s *boundedSink) drain(r io.Reader) {
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			s.total += n
			if room := s.limit - len(s.buf); room > 0 {
				take := n
				if take > room {
					take = room
				}
				s.buf = append(s.buf, chunk[:take]...)
			}
		}
		if err != nil {
			return
		}
	}
}

// countingSink discards everything and remembers only how much there was. This
// is what stderr gets: the protocol says its content never reaches a pane, a
// toast, a log, a diagnostic, or a crash report.
type countingSink struct {
	total int
}

func (s *countingSink) drain(r io.Reader) {
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		s.total += n
		if err != nil {
			return
		}
	}
}
