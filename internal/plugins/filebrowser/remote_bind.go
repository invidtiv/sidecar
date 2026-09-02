package filebrowser

import (
	"strings"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The bound-host half of the Files plugin.
//
// One rule governs everything here: while bound, this plugin must not read
// this machine's disk for the project it is showing. A same-named checkout is
// a different project, and rendering its bytes under a remote label is the
// failure the remote-project work exists to prevent — a green test that does
// it has still failed.

// remoteBound reports that this plugin is showing another machine's project.
func (p *Plugin) remoteBound() bool {
	return p.ctx != nil && p.ctx.HostID != ""
}

// remoteWorkspaceID is the durable identity the host resolves content and
// listings against: the bound worktree, or the project's main checkout.
//
// It is composed here rather than remembered, so a bind that moves to another
// worktree cannot leave this surface reading the previous one.
func (p *Plugin) remoteWorkspaceID() string {
	if p.ctx == nil || p.ctx.ProjectKey == "" {
		return ""
	}
	key := p.ctx.HostWorktreeKey
	if key == "" {
		key = p.ctx.ProjectKey
	}
	return p.ctx.ProjectKey + ":" + string(workspaceinventory.KindWorktree) + ":" + key
}

// remoteRoot is the host's path for the bound workspace. It is an identity and
// a label, never an argument to a local os, git, or td call.
func (p *Plugin) remoteRoot() string {
	if p.ctx == nil {
		return ""
	}
	if p.ctx.HostWorktreeKey != "" {
		return p.ctx.HostWorktreeKey
	}
	return p.ctx.ProjectKey
}

func (p *Plugin) hostVerbs() hostproto.VerbCapabilities {
	if p.ctx == nil || p.ctx.HostVerbs == nil {
		return hostproto.VerbCapabilities{}
	}
	return p.ctx.HostVerbs()
}

func (p *Plugin) hostShows() bool {
	return p.ctx != nil && p.ctx.HostShows != nil && p.ctx.HostShows()
}

// remoteUnavailable is why this bound surface cannot browse, or "" when it
// can. A local project always answers "".
func (p *Plugin) remoteUnavailable() string {
	if !p.remoteBound() {
		return ""
	}
	if p.ctx.RemoteRunner == nil {
		return "[" + p.ctx.HostID + "] is not reachable from this Sidecar"
	}
	return remoteTreeUnavailable(p.ctx.HostID, p.remoteWorkspaceID(), p.hostVerbs(), p.hostShows())
}

// remoteAvailable reports that a bound surface has everything it needs to list
// and read the host.
func (p *Plugin) remoteAvailable() bool {
	return p.remoteBound() && p.remoteUnavailable() == ""
}

// treeSource is the listing seam for whichever machine owns this project.
func (p *Plugin) treeSource() TreeSource {
	if p.treeSourceOverride != nil {
		return p.treeSourceOverride
	}
	if !p.remoteAvailable() {
		return nil
	}
	return &remoteTreeSource{
		hostID:      p.ctx.HostID,
		workspaceID: p.remoteWorkspaceID(),
		run:         p.ctx.RemoteRunner,
	}
}

// unavailableReason is the sentence the Files tab paints instead of a tree.
func (p *Plugin) unavailableReason() string {
	reason := p.remoteUnavailable()
	if reason == "" {
		return ""
	}
	return strings.TrimSpace(reason)
}
