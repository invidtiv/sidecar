package agentintegration

import (
	"strings"
	"testing"
)

// TestTmuxCallsCarryTheNamespaceTheyWereAskedAbout pins the fix that the live
// Phase C gate found and no offline test had covered.
//
// Every tmux call in this package used to be a bare `tmux`, which answers from
// $TMUX or the default socket. That is the right server only by coincidence
// when the caller is asking about a pane somewhere else — which is exactly what
// `sidecar agent explain --shell TARGET` does. The first gate run reported pane
// %2 on the developer's real tmux server for a shell that was %1 on the
// sandbox's, confidently and with nothing in the output to suggest the question
// had been answered about the wrong machine's worth of state.
//
// The assertion is on the argv rather than on a live server on purpose: the bug
// was entirely in which socket was addressed, so the argv is the whole of it,
// and a test that needed two real tmux servers is a test nobody runs.
func TestTmuxCallsCarryTheNamespaceTheyWereAskedAbout(t *testing.T) {
	const socket = "/tmp/some-private-socket/default"

	t.Run("an explicit namespace becomes an explicit socket", func(t *testing.T) {
		args := tmuxArgs(socket, "list-panes", "-t", "sess")
		want := []string{"-S", socket, "list-panes", "-t", "sess"}
		if strings.Join(args, " ") != strings.Join(want, " ") {
			t.Fatalf("tmuxArgs = %v, want %v", args, want)
		}
	})

	t.Run("an empty namespace leaves the argv alone", func(t *testing.T) {
		// The polling surfaces ask about panes on the server they are already
		// talking to, and must keep answering from it. Prefixing -S "" would
		// address a socket literally named "".
		args := tmuxArgs("", "display-message", "-p", "#{pid}")
		want := []string{"display-message", "-p", "#{pid}"}
		if strings.Join(args, " ") != strings.Join(want, " ") {
			t.Fatalf("tmuxArgs = %v, want %v", args, want)
		}
	})

	t.Run("a source bound to a namespace resolves panes through it", func(t *testing.T) {
		// NewStoreSourceOn's whole purpose is that the namespace reaches the
		// tmux calls, so the property worth pinning is that the constructor
		// carries it rather than dropping it on the floor.
		src := NewStoreSourceOn(t.TempDir(), socket)
		if src.namespace != socket {
			t.Fatalf("NewStoreSourceOn kept namespace %q, want %q", src.namespace, socket)
		}
		if plain := NewStoreSource(t.TempDir()); plain.namespace != "" {
			t.Fatalf("NewStoreSource invented namespace %q", plain.namespace)
		}
	})
}
