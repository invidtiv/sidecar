package livewatch

import (
	tea "charm.land/bubbletea/v2"
)

// The Bubble Tea half of the seam. Every host needs the same three commands —
// start a watcher off the update goroutine, wait for the next signal, hand the
// descriptors back — and every host was otherwise going to write them slightly
// differently. Getting any of the three subtly wrong is how a pane leaks
// descriptors or stops updating, so they live here once.

// Start returns a command that creates a watcher and points it at targets.
//
// The work happens inside the command, off the update goroutine, because
// creating a watcher opens a descriptor and registering targets touches the
// filesystem — neither belongs on the path that paints the first frame. Hosts
// call this when a pane opens, never from Init or Start.
//
// wrap turns the outcome into the host's own message. A host must adopt the
// watcher it is handed or stop it: a pane that closed while its watcher was
// being created still has to give the descriptors back.
func Start(cfg Config, targets []Target, wrap func(*PathWatcher, error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		w, err := NewPathWatcher(cfg)
		if err != nil {
			return wrap(nil, err)
		}
		w.Watch(targets...)
		return wrap(w, nil)
	}
}

// Listen returns a command that blocks until w reports a change, then emits
// msg.
//
// It is one-shot by design, matching how Bubble Tea commands work: the host
// re-arms it when it handles msg. Returning nil once the watcher stops ends the
// chain cleanly, so a stopped watcher never leaves a goroutine parked on a
// receive.
func Listen(w *PathWatcher, msg tea.Msg) tea.Cmd {
	if w == nil {
		return nil
	}
	signals := w.Signals()
	return func() tea.Msg {
		if _, ok := <-signals; !ok {
			return nil
		}
		return msg
	}
}

// Release returns a command that stops w.
//
// Stop blocks until the watcher's goroutine has finished, so it must not run on
// the update goroutine where it would stall the UI. Hosts use this from
// teardown paths; a host already off the update loop can call Stop directly.
func Release(w *PathWatcher) tea.Cmd {
	if w == nil {
		return nil
	}
	return func() tea.Msg {
		w.Stop()
		return nil
	}
}
