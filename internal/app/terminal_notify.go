package app

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

// terminalNotifyWriter is the app's writer for the direct-terminal
// notification transport.
//
// Inside the TUI the renderer owns the screen. A delivery goroutine that wrote
// an escape sequence straight to the terminal would land in the middle of a
// frame, so this writer only collects the bytes; the delivery command drains
// them and hands them to Bubble Tea as raw output, which sequences them with
// everything else the program emits.
//
// Draining is not addressed to a particular delivery. Two concurrent
// deliveries may see each other's bytes, and it does not matter: every
// sequence written is emitted exactly once, by whichever command drains it.
type terminalNotifyWriter struct {
	mu      sync.Mutex
	pending []byte
}

func (w *terminalNotifyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	return len(p), nil
}

// drain returns everything written since the last call, and empty when the
// transport wrote nothing — which is the common case, because the transport is
// off unless the user enabled it and this process is inside SSH.
func (w *terminalNotifyWriter) drain() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return ""
	}
	out := string(w.pending)
	w.pending = nil
	return out
}

// terminalNotifyMsg emits whatever the transport has written, for a caller that
// already has a tea.Msg to return. Nil when nothing was written.
//
// Every path that calls Deliver must reach this or [Model.terminalNotifyCmd].
// A path that forgets both does not merely fail to notify: the bytes stay
// buffered and are emitted later by whichever delivery drains next, so one
// unrelated notification puts two sequences on the terminal.
func terminalNotifyMsg(writer *terminalNotifyWriter) tea.Msg {
	if writer == nil {
		return nil
	}
	sequence := writer.drain()
	if sequence == "" {
		return nil
	}
	return tea.Raw(sequence)()
}

// terminalNotifyCmd is [terminalNotifyMsg] for a caller that returns a command.
func (m *Model) terminalNotifyCmd() tea.Cmd {
	msg := terminalNotifyMsg(m.terminalNotifyWriter)
	if msg == nil {
		return nil
	}
	return func() tea.Msg { return msg }
}
