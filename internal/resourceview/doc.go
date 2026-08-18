// Package resourceview renders one external resource document in one content
// box, and a tabbed set of them in one pane leaf.
//
// It is deliberately host-independent. It knows about resource.Document,
// resource.Reference and resource.Error and nothing about panes, tmux,
// workspaces, projects, or which of the two terminal surfaces is showing it.
// The project Workspace and the global Workspaces browser both bind it, and
// anything that behaves differently between them is a bug rather than a
// preference.
//
// It is also passive. A provider supplies data, never presentation: no ANSI,
// no keybindings, no actions, no colors. The one thing a document can do is
// name a validated http(s) source URL, and even that is opened by the host
// through its own confirmed path.
//
// Resolution happens elsewhere. The view asks for a reference to be resolved
// by returning a command the host supplies; it never starts a process itself,
// which is what keeps process work out of View and Update.
package resourceview
