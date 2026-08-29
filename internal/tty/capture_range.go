package tty

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const captureRangeTimeout = 2 * time.Second

// CaptureRange is a bounded tmux history capture with absolute pane
// coordinates. StartLine and EndLine describe Output as a half-open range.
type CaptureRange struct {
	Output      string
	HistorySize int
	StartLine   int
	EndLine     int
}

// CapturePaneRange captures the inclusive tmux range [start, end]. Negative
// coordinates address history above the visible pane, as in capture-pane.
func CapturePaneRange(target string, start, end int) (CaptureRange, error) {
	if target == "" {
		return CaptureRange{}, fmt.Errorf("capture pane range: empty target")
	}
	if start > end {
		return CaptureRange{}, fmt.Errorf("capture pane range: start %d after end %d", start, end)
	}

	ctx, cancel := context.WithTimeout(context.Background(), captureRangeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", capturePaneRangeArgs(target, start, end)...)
	output, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return CaptureRange{}, fmt.Errorf("capture pane range: timeout after %s", captureRangeTimeout)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return CaptureRange{}, fmt.Errorf("capture pane range: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return CaptureRange{}, fmt.Errorf("capture pane range: %w", err)
	}
	return parseCapturePaneRange(string(output), start)
}

func capturePaneRangeArgs(target string, start, end int) []string {
	return []string{
		"display-message", "-t", target, "-p", "#{history_size}",
		";",
		// -N keeps each row's trailing blanks; see CapturePaneOutput for why the
		// trimmed form is ambiguous.
		"capture-pane", "-p", "-e", "-N", "-t", target,
		"-S", strconv.Itoa(start),
		"-E", strconv.Itoa(end),
	}
}

func parseCapturePaneRange(output string, requestedStart int) (CaptureRange, error) {
	header, paneOutput, ok := strings.Cut(output, "\n")
	if !ok {
		return CaptureRange{}, fmt.Errorf("capture pane range: missing history metadata")
	}
	historySize, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || historySize < 0 {
		return CaptureRange{}, fmt.Errorf("capture pane range: invalid history size %q", header)
	}
	startLine := requestedStart
	if startLine < 0 {
		startLine = max(historySize+startLine, 0)
	} else {
		startLine = historySize + startLine
	}
	lineCount := len(splitOutputLines(paneOutput))
	return CaptureRange{
		Output:      paneOutput,
		HistorySize: historySize,
		StartLine:   startLine,
		EndLine:     startLine + lineCount,
	}, nil
}
