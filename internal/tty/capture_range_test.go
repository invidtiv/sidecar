package tty

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestCapturePaneRangeArgsUseArgvOnlyBounds(t *testing.T) {
	got := capturePaneRangeArgs("%12", -1200, -601)
	want := []string{
		"display-message", "-t", "%12", "-p", "#{history_size}\t#{pane_width}\t#{pane_height}\t#{alternate_on}\t#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}",
		";",
		"capture-pane", "-p", "-e", "-N", "-t", "%12",
		"-S", "-1200", "-E", "-601",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestParseCapturePaneRangeCarriesCaptureIdentityAndGeometry(t *testing.T) {
	got, err := parseCapturePaneRange("1500\t80\t24\t1\t42\t$1\t1234\tshell\t%12\nold-a\nold-b\n", -1200)
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneWidth != 80 || got.PaneHeight != 24 || !got.AltScreen || got.ServerPID != 42 ||
		got.SessionID != "$1" || got.SessionCreated != "1234" || got.Session != "shell" || got.Pane != "%12" {
		t.Fatalf("capture metadata = %#v", got)
	}
}

func TestParseCapturePaneRangeComputesAbsoluteCoordinates(t *testing.T) {
	got, err := parseCapturePaneRange("1500\nold-a\nold-b\n", -1200)
	if err != nil {
		t.Fatal(err)
	}
	if got.HistorySize != 1500 || got.StartLine != 300 || got.EndLine != 302 {
		t.Fatalf("range = %#v", got)
	}
	if got.Output != "old-a\nold-b\n" {
		t.Fatalf("output = %q", got.Output)
	}
}

func TestParseCapturePaneRangeClampsAtOldestHistory(t *testing.T) {
	got, err := parseCapturePaneRange("400\noldest\n", -1200)
	if err != nil {
		t.Fatal(err)
	}
	if got.StartLine != 0 || got.EndLine != 1 {
		t.Fatalf("range = %#v", got)
	}
}

func TestParseCapturePaneRangeRejectsMetadata(t *testing.T) {
	if _, err := parseCapturePaneRange("not-a-number\nline\n", -10); err == nil {
		t.Fatal("expected invalid metadata error")
	}
	if _, err := parseCapturePaneRange("10\t80\tbad\nline\n", -10); err == nil {
		t.Fatal("expected invalid target metadata error")
	}
}

func TestCapturePaneRangeBoundedRefusesOversizedCommandOutput(t *testing.T) {
	reader := strings.NewReader("0\n0123456789")
	_, err := readCaptureRangeOutput(reader, 4)
	if err == nil || !errors.Is(err, ErrCaptureRangeTooLarge) || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("bounded capture error = %v", err)
	}
	if reader.Len() != 7 {
		t.Fatalf("bounded reader consumed past the overflow sentinel: %d bytes remain", reader.Len())
	}
}

func TestCapturePaneRangeOutputAtLimitIsComplete(t *testing.T) {
	output, err := readCaptureRangeOutput(strings.NewReader("1234"), 4)
	if err != nil || string(output) != "1234" {
		t.Fatalf("at-limit output = %q, %v", output, err)
	}
}
