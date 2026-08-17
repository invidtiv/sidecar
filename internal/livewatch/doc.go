// Package livewatch keeps an open pane in step with state it does not own.
//
// Three panes have the same defect for the same reason. The td issue card, the
// document preview and the diff view each read something external once, when
// they open, and then render that snapshot until the user reopens them. An
// agent adds a child issue, writes a markdown file, or lands a commit, and the
// pane the user is watching quietly stops being true. Watching work happen is
// most of why those panes exist, so a snapshot defeats them.
//
// The fix is one shape, applied three times:
//
//	observe a cheap external change signal
//	  -> coalesce a burst of them into one
//	  -> re-read, but only one re-read at a time
//	  -> apply only if the result actually differs
//
// This package owns the two halves of that shape that are worth sharing, and
// deliberately owns nothing else.
//
// [PathWatcher] is the observe-and-coalesce half: an fsnotify registration over
// a named set of files and directories, with a quiet period so a burst of
// writes lands as one signal, a latency cap so a continuously busy target still
// reports, and a Stop that gives every descriptor back. It watches only the
// paths it is given. None of the callers here walk a tree.
//
// [Refresher] is the decide half, and it holds no timers, descriptors or view
// state at all — just the four booleans every host was otherwise going to
// re-derive. It answers: should a re-read start now, is another one owed, and
// is this result different from what is already on screen. That last question
// is the one that matters most. All three tickets independently described the
// same symptom — a pane that visibly flashes every time an unrelated write
// touches the store it reads from — so the no-change gate lives here rather
// than in any one surface.
//
// Hosts bind through a thin per-surface file, because each surface preserves
// different UI state across the re-read: the issue card keeps scroll, cursor
// and hover; the document keeps its scroll offset; the diff keeps the selected
// file by path rather than by index, since a positional cursor silently
// re-points at a different file when the list above it changes.
//
// # Startup
//
// Nothing here runs on the startup path. A PathWatcher is created when a pane
// opens, from inside a tea.Cmd, never from a plugin's Init or before Start
// returns. A pane that is never opened costs nothing.
//
// # Cost
//
// A PathWatcher costs no processes and no wakeups while its targets are idle;
// fsnotify blocks. That is the whole reason to prefer it to a timer. Where a
// signal genuinely cannot be observed from the filesystem, poll — but say what
// the interval buys, and make it proportional to how often the thing really
// changes.
package livewatch
