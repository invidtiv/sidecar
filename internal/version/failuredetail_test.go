package version

import (
	"errors"
	"strings"
	"testing"
)

func TestFailureDetailIncludesOutputTail(t *testing.T) {
	r := Result{
		Status: StatusFailed,
		Err:    errors.New("go install github.com/marcus/sidecar/cmd/sidecar@v1.0.1: exit status 1"),
		Output: "go: downloading github.com/marcus/sidecar v1.0.1\n" +
			"# github.com/mattn/go-sqlite3\n" +
			"sqlite3-binding.c:1234:5: error: expected ';'\n",
	}
	lines := FailureDetail(r, 6)
	if len(lines) != 4 {
		t.Fatalf("expected error plus 3 output lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "exit status 1") {
		t.Errorf("first line should be the error, got %q", lines[0])
	}
	if !strings.Contains(lines[3], "expected ';'") {
		t.Errorf("compiler error must survive; got %q", lines[3])
	}
}

func TestFailureDetailKeepsTailNotHead(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("go: downloading noise\n")
	}
	b.WriteString("ld: symbol not found\n")

	lines := FailureDetail(Result{Err: errors.New("boom"), Output: b.String()}, 3)
	if len(lines) != 4 {
		t.Fatalf("expected error plus 3 lines, got %d", len(lines))
	}
	if lines[3] != "ld: symbol not found" {
		t.Errorf("last output line must be kept, got %q", lines[3])
	}
}

func TestFailureDetailSkipsBlankLinesAndHandlesNoOutput(t *testing.T) {
	lines := FailureDetail(Result{Err: errors.New("boom"), Output: "\n\n  \n"}, 5)
	if len(lines) != 1 || lines[0] != "boom" {
		t.Errorf("blank output should contribute nothing, got %v", lines)
	}

	if got := FailureDetail(Result{Output: "ignored"}, 0); got != nil {
		t.Errorf("no error and no line budget should yield nothing, got %v", got)
	}
}
