package tty

import (
	"slices"
	"testing"
)

func TestCapturePaneRangeArgsUseArgvOnlyBounds(t *testing.T) {
	got := capturePaneRangeArgs("%12", -1200, -601)
	want := []string{
		"display-message", "-t", "%12", "-p", "#{history_size}",
		";",
		"capture-pane", "-p", "-e", "-N", "-t", "%12",
		"-S", "-1200", "-E", "-601",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
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
}
