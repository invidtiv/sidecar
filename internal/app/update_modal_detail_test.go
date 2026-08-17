package app

import (
	"strings"
	"testing"
)

func TestWrapDetailLineKeepsWholeMessage(t *testing.T) {
	msg := "sqlite3-binding.c:1234:5: error: implicit declaration of function 'foo' is invalid"
	lines := wrapDetailLine(msg, 40)
	if len(lines) < 2 {
		t.Fatalf("expected the line to wrap, got %v", lines)
	}
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "is invalid") {
		t.Errorf("the end of the message must survive wrapping, got %q", joined)
	}
	for _, l := range lines {
		if len(l) > 40 {
			t.Errorf("line exceeds width: %q", l)
		}
	}
}

func TestWrapDetailLineIsBounded(t *testing.T) {
	lines := wrapDetailLine(strings.Repeat("verylongtoken ", 200), 40)
	if len(lines) > maxDetailWrapLines {
		t.Errorf("expected at most %d lines, got %d", maxDetailWrapLines, len(lines))
	}
	if !strings.HasSuffix(lines[len(lines)-1], "…") {
		t.Errorf("a clipped final line should be marked, got %q", lines[len(lines)-1])
	}
}

func TestWrapDetailLineShortLineUnchanged(t *testing.T) {
	lines := wrapDetailLine("  exit status 1  ", 40)
	if len(lines) != 1 || lines[0] != "exit status 1" {
		t.Errorf("short line should pass through trimmed, got %v", lines)
	}
}
