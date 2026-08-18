// Package clip is the one path text takes to the clipboard.
//
// Every copy Sidecar performs goes through here so that a copy means the same
// thing everywhere: the system clipboard when a clipboard utility is reachable,
// and the terminal's own clipboard over OSC 52 — the half that works when
// Sidecar runs over SSH or inside tmux, where no local utility exists.
package clip

import (
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// writeNative is the system clipboard write. It is a variable so tests can
// exercise the failure half of a dual write without a clipboard utility.
var writeNative = clipboard.WriteAll

// WriteAll writes text to the system clipboard. Callers that can return a
// tea.Cmd should use Copy instead: it also reaches the terminal's clipboard.
func WriteAll(text string) error {
	return writeNative(text)
}

// ReadAll reads the system clipboard.
func ReadAll() (string, error) {
	return clipboard.ReadAll()
}

// Result reports what a copy achieved. Only the native half can report
// anything: an OSC 52 write is fire-and-forget by design — the terminal never
// answers — so a Result with no error means "both were attempted", not "both
// landed".
type Result struct {
	// NativeErr is the system clipboard write's failure. A copy is not over
	// when it is set: the OSC 52 write still happened, and over SSH it is the
	// half that was ever going to work.
	NativeErr error
}

// Copy writes text to every clipboard reachable from here — the system
// clipboard natively and the terminal's over OSC 52 — and hands the outcome to
// wrap, which turns it into the host's own message. wrap may return nil when
// the host says nothing about a copy.
func Copy(text string, wrap func(Result) tea.Msg) tea.Cmd {
	return tea.Sequence(
		tea.SetClipboard(text),
		func() tea.Msg {
			result := Result{NativeErr: WriteAll(text)}
			if wrap == nil {
				return nil
			}
			return wrap(result)
		},
	)
}
