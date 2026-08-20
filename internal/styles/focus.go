package styles

import "sync/atomic"

// Focus exclusivity across the whole app
//
// A pane only knows its own surface's focus: a plugin (or a pane tree host)
// answers "which of MY panes has the keyboard", and it has no way to learn that
// something outside it took the keyboard. When the app shell grew a surface that
// can hold focus without being a pane — the notification centre, a full-height
// right panel drawn beside the content rather than over it — that gap became
// visible: the centre lit its own border while the workspace, the global
// Workspaces browser, the file browser or the git diff underneath kept drawing
// its focused pane as focused, so two panes claimed the keyboard at once.
//
// The fix is one signal at the lowest shared seam rather than a clause in each
// surface. Every bordered pane in the app is painted by RenderPanel or
// RenderPanelWithGradient in this package, so the shell records for the duration
// of a content render that focus is held outside the panes, and those two
// functions draw the normal border instead of the focused one. Every surface
// inherits it — including surfaces added later, which never have to know the
// centre exists. Nothing about a surface's own focus state is touched, so when
// the keyboard comes back its focused pane re-lights exactly where it was.
//
// It is process-global for the same reason the theme is: rendering happens on
// one goroutine and a copy threaded through every render signature would be a
// copy that can go stale. The flag is atomic anyway so a reader on another
// goroutine cannot tear.
//
// This is a FOCUS signal only. Attention — a toast, a pane flash — must never
// set it: a notification appearing is not the user moving the keyboard, and
// dropping the focus ring under one would make every toast look like a focus
// change. Chrome drawn outside the content region (the centre panel itself,
// toasts, the flash block, modals) is rendered while the flag is clear and is
// therefore unaffected.
var focusHeldOutsidePanes atomic.Bool

// SetFocusHeldOutsidePanes tells the renderer that an app-level surface outside
// every pane owns the keyboard. The shell sets it before it renders the content
// region and clears it after, so it describes the frame being drawn rather than
// a mode the app is in.
func SetFocusHeldOutsidePanes(held bool) { focusHeldOutsidePanes.Store(held) }

// FocusHeldOutsidePanes reports the current signal.
func FocusHeldOutsidePanes() bool { return focusHeldOutsidePanes.Load() }
