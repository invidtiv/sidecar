package app

import "strings"

// Destination is the host-qualified identity both the project (`@`) and
// worktree (`W`) switchers will share. A same-named checkout on two machines
// is two destinations; persist and compare ProjectKey only as
// hosts.ScopedKey(HostID, ProjectKey).
//
// Slice 1 wires this into `@` listing, Enter bind, navbar, and toasts.
type Destination struct {
	HostID          string // empty = this machine
	HostIncarnation uint64 // 0 when local
	ProjectKey      string // owning host's inventory key = canonical(root) on that machine; NOT a local path
	ProjectName     string
	WorktreeKey     string // canonical worktree path on the owning host; empty = main checkout
	WorktreeName    string
	Root            string // hint from the owning host, never passed to local os/git/td
}

// FormatDestination is the one-line label `@`, `W`, the navbar, and toasts
// share. Local destinations are unprefixed; a worktree suffix is present only
// for a linked worktree. Host is the registered host id, not the SSH target.
func FormatDestination(d Destination) string {
	name := d.ProjectName
	if d.HostID != "" {
		name = "[" + d.HostID + "] " + name
	}
	if d.WorktreeKey != "" {
		name += " [[" + d.WorktreeName + "]]"
	}
	return name
}

// DestinationMatches reports whether query is a case-insensitive substring of
// the destination's host id, project name, Root, or worktree name. An empty
// query matches everything.
func DestinationMatches(d Destination, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	for _, field := range []string{d.HostID, d.ProjectName, d.Root, d.WorktreeName} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

// BoundDestinationNavbarLabel is the project-selector text for a bound
// destination, including the host.
func BoundDestinationNavbarLabel(d Destination) string {
	return FormatDestination(d)
}

// BoundDestinationTitleProject is the {project} value a bound destination
// feeds the terminal-title template, including the host.
func BoundDestinationTitleProject(d Destination) string {
	if d.HostID == "" {
		return d.ProjectName
	}
	return "[" + d.HostID + "] " + d.ProjectName
}

// BoundDestinationTitleWorktree is the {worktree} value a bound destination
// feeds the terminal-title template. Empty on the main checkout.
func BoundDestinationTitleWorktree(d Destination) string {
	if d.WorktreeKey == "" {
		return ""
	}
	if d.WorktreeName == "" {
		return "worktree"
	}
	return d.WorktreeName
}
