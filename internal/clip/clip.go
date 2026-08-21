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

// WriteAll writes text to the system clipboard, and only that one — over SSH it
// reaches nothing. It is for handing a writer to code that owns the call and
// gives nothing back to the program loop; everything that can return a tea.Cmd
// uses Copy or CopyFrom, which also reach the terminal's own clipboard.
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

// Message is what the user is told about a copy, phrased from the caller's own
// success wording. A failed native write is not a failed copy — the OSC 52
// write still went to the terminal, which over SSH is the half that was ever
// going to work — so the wording names the clipboard the text was sent to
// rather than reporting an error the user cannot act on. It is one sentence for
// every copy path so that the same outcome never reads two ways.
func (r Result) Message(ok string) string {
	if r.NativeErr != nil {
		return ok + " — sent to the terminal clipboard"
	}
	return ok
}

// CopyFrom copies text the command produces when it runs — read off disk, taken
// from a subprocess — rather than text the caller already holds, so work that
// belongs off the update loop stays off it. produce returns the message the host
// should show instead when there is nothing to copy, and wrap is handed the text
// that was copied so nothing has to be carried between them.
func CopyFrom(produce func() (string, tea.Msg), wrap func(Result, string) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		text, instead := produce()
		if text == "" {
			return instead
		}
		// A command may answer with the message a command tree would have
		// produced; Bubble Tea runs it exactly as if this had returned the tree.
		return Copy(text, func(r Result) tea.Msg {
			if wrap == nil {
				return nil
			}
			return wrap(r, text)
		})()
	}
}

// Copy writes text to every clipboard reachable from here — the system
// clipboard natively and the terminal's over OSC 52 — and hands the outcome to
// wrap, which turns it into the host's own message. wrap may return nil when
// the host says nothing about a copy.
func Copy(text string, wrap func(Result) tea.Msg) tea.Cmd {
	recordRecent(text)
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
