package tty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/tmuxformat"
)

const captureRangeTimeout = 2 * time.Second

// DefaultCaptureRangeMaxBytes bounds the output retained by the shared range
// capture. Callers with a smaller wire budget use CapturePaneRangeBounded.
const DefaultCaptureRangeMaxBytes = 16 << 20

var ErrCaptureRangeTooLarge = errors.New("capture pane range output too large")

// CaptureRange is a bounded tmux history capture with absolute pane
// coordinates. StartLine and EndLine describe Output as a half-open range.
type CaptureRange struct {
	Output         string
	HistorySize    int
	StartLine      int
	EndLine        int
	PaneWidth      int
	PaneHeight     int
	AltScreen      bool
	ServerPID      int
	SessionID      string
	SessionCreated string
	Session        string
	Pane           string
}

// CapturePaneRange captures the inclusive tmux range [start, end]. Negative
// coordinates address history above the visible pane, as in capture-pane.
func CapturePaneRange(target string, start, end int) (CaptureRange, error) {
	return CapturePaneRangeBounded(target, start, end, DefaultCaptureRangeMaxBytes)
}

// CapturePaneRangeBounded captures a range while refusing to retain more than
// maxBytes. The capture metadata and rows come from one tmux command list; the
// caller can therefore fence the result against the exact pane identity,
// geometry and alternate-screen state observed for that capture.
func CapturePaneRangeBounded(target string, start, end, maxBytes int) (CaptureRange, error) {
	if target == "" {
		return CaptureRange{}, fmt.Errorf("capture pane range: empty target")
	}
	if start > end {
		return CaptureRange{}, fmt.Errorf("capture pane range: start %d after end %d", start, end)
	}
	if maxBytes <= 0 {
		return CaptureRange{}, fmt.Errorf("capture pane range: invalid byte limit %d", maxBytes)
	}

	ctx, cancel := context.WithTimeout(context.Background(), captureRangeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", tmuxformat.ClientArgs(capturePaneRangeArgs(target, start, end)...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CaptureRange{}, fmt.Errorf("capture pane range: stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return CaptureRange{}, fmt.Errorf("capture pane range: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxBytes)+1))
	if len(output) > maxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return CaptureRange{}, fmt.Errorf("%w: exceeds %d bytes", ErrCaptureRangeTooLarge, maxBytes)
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return CaptureRange{}, fmt.Errorf("capture pane range: read output: %w", readErr)
	}
	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return CaptureRange{}, fmt.Errorf("capture pane range: timeout after %s", captureRangeTimeout)
	}
	if err != nil {
		if stderr.Len() > 0 {
			return CaptureRange{}, fmt.Errorf("capture pane range: %s", strings.TrimSpace(stderr.String()))
		}
		return CaptureRange{}, fmt.Errorf("capture pane range: %w", err)
	}
	return parseCapturePaneRange(string(output), start)
}

func capturePaneRangeArgs(target string, start, end int) []string {
	return []string{
		"display-message", "-t", target, "-p", "#{history_size}\t#{pane_width}\t#{pane_height}\t#{alternate_on}\t#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}",
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
	metadata := strings.Split(strings.TrimSpace(header), "\t")
	historySize, err := strconv.Atoi(metadata[0])
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
	result := CaptureRange{
		Output:      paneOutput,
		HistorySize: historySize,
		StartLine:   startLine,
		EndLine:     startLine + lineCount,
	}
	if len(metadata) == 9 {
		width, widthErr := strconv.Atoi(metadata[1])
		height, heightErr := strconv.Atoi(metadata[2])
		pid, pidErr := strconv.Atoi(metadata[4])
		if widthErr != nil || heightErr != nil || pidErr != nil || width <= 0 || height <= 0 || pid <= 0 {
			return CaptureRange{}, fmt.Errorf("capture pane range: invalid target metadata %q", header)
		}
		result.PaneWidth, result.PaneHeight = width, height
		result.AltScreen = tmuxFormatBool(metadata[3])
		result.ServerPID, result.SessionID, result.SessionCreated = pid, metadata[5], metadata[6]
		result.Session, result.Pane = metadata[7], metadata[8]
	} else if len(metadata) != 1 {
		return CaptureRange{}, fmt.Errorf("capture pane range: invalid target metadata %q", header)
	}
	return result, nil
}
