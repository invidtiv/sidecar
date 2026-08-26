//go:build darwin

package agentactivity

import (
	"encoding/binary"
	"os"
	"strconv"
	"testing"
)

func TestDarwinProcessArgvLayoutParserPreservesExecAPath(t *testing.T) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, 3)
	data = append(data, []byte("/usr/local/bin/node\x00\x00\x00/Users/test/.local/bin/agent\x00--use-system-ca\x00index.js\x00")...)
	if got := parseDarwinProcessArgv0(data); got != "/Users/test/.local/bin/agent" {
		t.Fatalf("argv0 = %q", got)
	}
}

// TestForegroundAgentLiveProbe verifies a PID from an isolated tmux pane when
// explicitly supplied. Ordinary test runs skip it; the terminal fidelity proof
// harness uses it against installed agent CLIs without touching the main tmux
// server.
func TestForegroundAgentLiveProbe(t *testing.T) {
	raw := os.Getenv("SIDECAR_FOREGROUND_PROBE_PID")
	if raw == "" {
		t.Skip("set SIDECAR_FOREGROUND_PROBE_PID")
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := os.Getenv("SIDECAR_FOREGROUND_PROBE_WANT")
	if got := ResolveForegroundAgent(pid); got != want {
		t.Fatalf("ResolveForegroundAgent(%d) = %q, want %q", pid, got, want)
	}
}
