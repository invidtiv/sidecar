package tty

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/marcus/sidecar/internal/tmuxformat"
)

// maxBatchCaptures bounds one tmux client invocation. Larger sets are split
// across several invocations; the bound only keeps argv and the reply small.
const maxBatchCaptures = 64

// CapturePaneOutputs captures several panes in one tmux client invocation and
// returns each pane's text exactly as CapturePaneOutput would return it.
//
// A status refresh used to fork one tmux client per pane, which on a machine
// with a few dozen live panes was most of a catalog's wall time and nearly
// all of its system time. tmux runs a `;`-separated command list in order on
// one client, so each capture is framed by a display-message marker line
// carrying a per-call random nonce: a pane cannot forge it, and pane IDs never
// enter the format (display-message expands % through strftime).
//
// The result is advisory. tmux abandons the rest of a command list at the
// first failing command, so a pane that vanished since the listing truncates
// the batch there. Only captures framed on both sides are returned; a missing
// entry means the caller should capture that pane on its own, which also
// reproduces the exact per-pane error.
func CapturePaneOutputs(ctx context.Context, targets []string, scrollback int) map[string]string {
	out := make(map[string]string, len(targets))
	for start := 0; start < len(targets); start += maxBatchCaptures {
		end := min(start+maxBatchCaptures, len(targets))
		capturePaneBatch(ctx, targets[start:end], scrollback, out)
	}
	return out
}

func capturePaneBatch(ctx context.Context, targets []string, scrollback int, out map[string]string) {
	if len(targets) == 0 {
		return
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return
	}
	prefix := "sidecar-capture-" + hex.EncodeToString(nonce[:]) + " "
	marker := func(i int) string { return prefix + strconv.Itoa(i) }
	args := make([]string, 0, len(targets)*11+3)
	for i, target := range targets {
		args = append(args, "display-message", "-p", marker(i), ";", "capture-pane", "-p", "-e", "-N", "-t", target)
		if scrollback > 0 {
			args = append(args, "-S", fmt.Sprintf("-%d", scrollback))
		}
		args = append(args, ";")
	}
	args = append(args, "display-message", "-p", marker(len(targets)))
	// Output returns whatever stdout tmux produced even when the list stopped
	// early; parseCaptureBatch keeps only the fully framed captures.
	data, _ := exec.CommandContext(ctx, "tmux", tmuxformat.ClientArgs(args...)...).Output()
	if ctx.Err() != nil {
		return
	}
	for i, text := range parseCaptureBatch(data, len(targets), marker) {
		out[targets[i]] = text
	}
}

// parseCaptureBatch splits a framed reply into per-target captures, indexed by
// position. A capture is returned only when its opening marker and the next
// marker both appear, each as a whole line, in order.
func parseCaptureBatch(data []byte, count int, marker func(int) string) map[int]string {
	result := make(map[int]string, count)
	open := []byte(marker(0) + "\n")
	if !bytes.HasPrefix(data, open) {
		return result
	}
	pos := len(open)
	for i := 0; i < count; i++ {
		next := []byte(marker(i+1) + "\n")
		var end int
		if bytes.HasPrefix(data[pos:], next) {
			end = pos
		} else {
			idx := bytes.Index(data[pos:], append([]byte{'\n'}, next...))
			if idx < 0 {
				return result
			}
			end = pos + idx + 1
		}
		result[i] = string(data[pos:end])
		pos = end + len(next)
	}
	return result
}
