package workspaceops

import (
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// ServerStateOf is the single funnel every reap path goes through to turn an
// observed tmux incarnation into the evidence the shell writer reasons about, so
// it is the cheapest place to pin the distinction the blocker turned on.
//
// The row that matters is "present but pid 0". A socket stat says a socket file
// exists; it says nothing about which server process is bound to it. Reading
// that as a dead server is what made two production surfaces preserve and mark
// every shell the user closed, and then recreate them after the next restart.
func TestServerStateOfDistinguishesUnidentifiedFromGone(t *testing.T) {
	tests := []struct {
		name        string
		inc         tmuxserver.Incarnation
		wantKnown   bool
		wantRunning bool
		wantID      string
	}{
		{
			name:        "a fully identified server",
			inc:         tmuxserver.Present(11, 22, 4242),
			wantKnown:   true,
			wantRunning: true,
			wantID:      "pid=4242",
		},
		{
			name: "a socket stat with no pid is unidentified, NOT gone",
			// This is exactly what tmuxserver.Socket() returns, and exactly what
			// both broken bindings used to pass to the writer.
			inc:       tmuxserver.Present(11, 22, 0),
			wantKnown: false,
		},
		{
			name:      "tmux answering no-server-running is positive evidence of death",
			inc:       tmuxserver.Absent(),
			wantKnown: true,
		},
		{
			name:      "an unanswered question is not evidence of anything",
			inc:       tmuxserver.Unknown(),
			wantKnown: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ServerStateOf(tc.inc)
			if got.Known() != tc.wantKnown {
				t.Errorf("Known() = %v, want %v", got.Known(), tc.wantKnown)
			}
			if got.Running() != tc.wantRunning {
				t.Errorf("Running() = %v, want %v", got.Running(), tc.wantRunning)
			}
			if got.ID() != tc.wantID {
				t.Errorf("ID() = %q, want %q", got.ID(), tc.wantID)
			}
		})
	}
}

// TestServerRunningRefusesAnEmptyID keeps the unsafe state unrepresentable: a
// caller that has no id cannot assert a running server by accident.
func TestServerRunningRefusesAnEmptyID(t *testing.T) {
	for _, id := range []string{"", "   "} {
		got := shellstate.ServerRunning(id)
		if got.Known() || got.Running() {
			t.Fatalf("ServerRunning(%q) claimed to identify a server: %+v", id, got)
		}
	}
}
