package workspacediff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// MaxDiffBytes bounds git patch output before parsing or transport encoding.
// The encoded content response has its own cap because JSON may expand bytes.
const MaxDiffBytes = 768 << 10

// Patch is a bounded prefix. Truncated means the total size is unknown.
type Patch struct {
	Raw       string
	Truncated bool
}

const diffTruncationNotice = "\n[Diff truncated: remaining content omitted]\n"

func (p Patch) display() string {
	if p.Truncated {
		return p.Raw + diffTruncationNotice
	}
	return p.Raw
}

// GitOutputBounded reads only limit+1 stdout bytes, then stops and reaps git.
// Stderr is drained separately into a bounded buffer, never into the patch.
func GitOutputBounded(ctx context.Context, dir string, limit int64, args ...string) (Patch, error) {
	if limit < 0 {
		return Patch{}, fmt.Errorf("git output limit must be nonnegative")
	}
	if err := ctx.Err(); err != nil {
		return Patch{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = dir
	// Git may spawn helpers that inherit stderr; do not wait indefinitely for
	// those descriptors after git exits or is canceled.
	cmd.WaitDelay = time.Second
	stderr := &cappedOutput{remaining: 16 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Patch{}, err
	}
	// CommandContext kills git, but an inherited stdout can keep ReadAll
	// blocked. Closing our read end makes cancellation independent of helpers.
	stopClose := context.AfterFunc(runCtx, func() { _ = stdout.Close() })
	defer stopClose()
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return Patch{}, ctx.Err()
		}
		return Patch{}, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	truncated := int64(len(out)) > limit
	if truncated || readErr != nil {
		cancel()
	}
	// Close the read end before waiting: a producer subprocess inheriting stdout
	// must also observe the stop rather than remain blocked on a full pipe.
	_ = stdout.Close()
	waitErr := cmd.Wait()
	return boundedGitResult(ctx, dir, args, out, limit, readErr, waitErr, stderr.text.String())
}

// boundedGitResult interprets a reaped process independently of whether the
// reader stopped early. A known process failure remains a failure.
func boundedGitResult(ctx context.Context, dir string, args []string, out []byte, limit int64, readErr, waitErr error, diagnostic string) (Patch, error) {
	truncated := int64(len(out)) > limit
	// Cancellation by the caller remains an error even if the prefix is full.
	if err := ctx.Err(); err != nil {
		return Patch{}, err
	}
	if readErr != nil {
		return Patch{}, readErr
	}
	var exit *exec.ExitError
	diffExit := len(args) > 0 && args[0] == "diff" && errors.As(waitErr, &exit) && exit.ExitCode() == 1
	// ErrWaitDelay is returned only when the command itself exited successfully
	// and an inherited I/O descriptor outlived the bounded drain.
	canceledAtLimit := truncated && (errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, exec.ErrWaitDelay))
	if truncated && errors.As(waitErr, &exit) {
		status, ok := exit.Sys().(syscall.WaitStatus)
		canceledAtLimit = canceledAtLimit || (ok && status.Signaled() && (status.Signal() == syscall.SIGKILL || status.Signal() == syscall.SIGPIPE))
	}
	if waitErr != nil && !diffExit && !canceledAtLimit {
		return Patch{}, fmt.Errorf("git %s in %s: %s: %w", strings.Join(args, " "), dir, strings.TrimSpace(diagnostic), waitErr)
	}
	if truncated {
		out = out[:limit]
	}
	return Patch{Raw: string(out), Truncated: truncated}, nil
}

type cappedOutput struct {
	text      strings.Builder
	remaining int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	keep := n
	if keep > b.remaining {
		keep = b.remaining
	}
	b.text.Write(p[:keep])
	b.remaining -= keep
	return n, nil
}
