package overview

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// Mutating a remote workspace from the Sessions browser.
//
// Every action here is one `sidecar <verb> --json` invocation on the host,
// through internal/hosts' request seam. There is no second protocol and no
// write path in hostserve: the host runs the same CLI verb an agent would run
// over plain ssh, and this file only decides which verb, with which arguments,
// and what to do with the answer.
//
// Three rules hold everywhere below, and each of them is a bug this area has
// already produced once:
//
//  1. A remote path is never resolved here. Not joined, not stat'ed, not passed
//     to a local git or filesystem call. On two machines with the same checkout
//     layout that does not fail, it succeeds against the wrong repository.
//  2. Nothing decides "remote" from remembered state. Every action re-resolves
//     its target from the form or the selected row at the moment it runs, so a
//     surface that went remote once cannot stay remote for the next local
//     action (the tty.Model defect of Phase A).
//  3. Every invocation is inside a tea.Cmd, bounded by a deadline, and parented
//     to the host context so quitting cancels it (td-052329).

// runRemoteSidecar is the seam every remote mutation runs through.
//
// A package variable for the same reason createManagedShell is one: the
// argument list a verb is invoked with is the contract this surface owns, and
// proving it is right must not need ssh, a network, or a second machine.
var runRemoteSidecar = runRemoteSidecarProduction

func runRemoteSidecarProduction(ctx context.Context, registry *hosts.Registry, hostID string, args []string, out any) error {
	if registry == nil {
		return &hosts.RunError{
			Failure: hosts.FailUnavailable, HostID: hostID, Args: args, ExitCode: -1,
			Detail: "no hosts are connected from this Sidecar",
		}
	}
	return registry.RunSidecar(ctx, hostID, args, out)
}

// resolveRemoteAgentCmd builds an agent's launch command for a shell on another
// machine. Deliberately the config-only resolution: see
// workspaceops.ResolveAgentCommandFromConfig for why the path-reading form is
// wrong across a host boundary.
var resolveRemoteAgentCmd = workspaceops.ResolveAgentCommandFromConfig

// Deadlines. A remote verb that outlives the interaction that asked for it is
// how a quit comes to take a minute (td-052329), so nothing here is unbounded —
// even though the local equivalents run on context.Background().
const (
	// remoteCreateShellTimeout covers create + an optional agent send. The host
	// spends up to createWaitDefault polling for a UI ack inside the create,
	// plus a tmux spawn.
	remoteCreateShellTimeout = 60 * time.Second
	// remoteWorktreeExecTimeout covers a worktree creation whose setup hook is
	// a real install. Long, but bounded and cancellable.
	remoteWorktreeExecTimeout = 5 * time.Minute
	// remoteQuickTimeout covers verbs that only touch the host's state tree:
	// planning a worktree, renaming, sending a command.
	remoteQuickTimeout = 30 * time.Second
)

// createTarget names where a create action will run: a project, and the host it
// lives on, or "" for this machine.
//
// It is resolved fresh from the form's current project key on every action
// rather than remembered, which is rule 2 above expressed as a type.
type createTarget struct {
	Project Project
	HostID  string
}

// Remote reports that this target is on another machine.
func (t createTarget) Remote() bool { return t.HostID != "" }

// resolveCreateTarget finds the project a create-form key names, on this
// machine or on a host.
//
// Local first, and by the same Key-or-Path match projectIndex uses, so a
// configuration with no hosts resolves exactly what it resolved before. A
// remote key is host-scoped (hosts.ScopedKey), so it cannot collide with a
// local one.
func (m *Model) resolveCreateTarget(key string) (createTarget, bool) {
	if idx := m.projectIndex(key); idx >= 0 {
		return createTarget{Project: m.projects[idx]}, true
	}
	hostID, _, scoped := hosts.SplitScopedKey(key)
	if !scoped {
		return createTarget{}, false
	}
	for _, project := range m.hostProjects[hostID] {
		if projectKey(project) == key {
			return createTarget{Project: project, HostID: hostID}, true
		}
	}
	return createTarget{}, false
}

// selectedCreateTarget is the create form's current project, wherever it lives.
func (m *Model) selectedCreateTarget() (createTarget, bool) {
	if m.createForm == nil {
		return createTarget{}, false
	}
	return m.resolveCreateTarget(m.createForm.ProjectKey())
}

// projectWorkspaces are the inventory rows a project key currently has, local
// or remote. Read-only over data already in the model; it reaches no
// filesystem, which is what makes it safe to ask about a remote project.
func (m *Model) projectWorkspaces(key string) []workspaceinventory.Workspace {
	if result, ok := m.results[key]; ok {
		return result.Workspaces
	}
	hostID, _, scoped := hosts.SplitScopedKey(key)
	if !scoped {
		return nil
	}
	for _, result := range m.hostResults[hostID] {
		if result.ProjectKey == key {
			return result.Workspaces
		}
	}
	return nil
}

// hostIncarnationFor stamps an outgoing request with the host-client
// incarnation that will be current when its answer comes back.
func (m *Model) hostIncarnationFor(hostID string) uint64 {
	if hostID == "" || m.hostIncarnations == nil {
		return 0
	}
	return m.hostIncarnations[hostID]
}

// hostReplyStale reports a remote answer that must not be applied: its host was
// removed from configuration, or retargeted at another machine, while the
// request was in flight.
//
// The fences are the ones handleHostUpdate already uses — m.hostRegistered and
// m.hostIncarnations — rather than a second mechanism, because a config save
// reconciles hosts live (config_surface.go → SyncHosts) and there must be
// exactly one answer to "is this host still the host I asked?".
func (m *Model) hostReplyStale(hostID string, incarnation uint64) bool {
	if hostID == "" {
		return false
	}
	if m.hostRegistered != nil && !m.hostRegistered[hostID] {
		return true
	}
	if m.hostIncarnations != nil {
		expected, ok := m.hostIncarnations[hostID]
		if !ok || expected != incarnation {
			return true
		}
	}
	return false
}

// remoteReplyDropped is what a surface says when it throws an answer away
// because the host it came from is gone. Saying nothing would leave a modal
// spinning on a machine that no longer exists.
func remoteReplyDropped(hostID string) string {
	return hostID + " was removed or retargeted while that was running; its answer was dropped"
}

// remoteActionError is the sentence a failed remote mutation shows: the
// remote's own words, then what to do about it — the same shape a host health
// row uses, because it is the same reader with the same problem.
func remoteActionError(err error) string {
	if err == nil {
		return ""
	}
	var runErr *hosts.RunError
	if errors.As(err, &runErr) {
		detail := strings.TrimSpace(runErr.Detail)
		if detail == "" {
			detail = runErr.Error()
		}
		if fix := runErr.Fix(); fix != "" {
			return detail + " — " + fix
		}
		return detail
	}
	return err.Error()
}

// dropRemoteCreateReply throws away a create or plan answer from a host that is
// no longer the host it was addressed to, and un-sticks the modal that was
// waiting on it.
//
// The answer is dropped rather than applied because it describes a machine this
// configuration no longer points at; the modal is un-stuck rather than left
// spinning because a user who removed a host should not be left waiting on it.
func (m *Model) dropRemoteCreateReply(hostID string) tea.Cmd {
	if !m.createOpen {
		m.clearPendingCreated()
		return nil
	}
	m.createBusy = false
	m.createModal = nil
	m.createPlan = nil
	m.clearPendingCreated()
	m.setCreateError(remoteReplyDropped(hostID))
	return nil
}

// applyRemoteWorktreeCreated folds a host's completed worktree creation back
// into the browser.
//
// There is no local record, no journal to finalize and no session to launch:
// `sidecar create worktree` did all of that on the machine that owns the
// repository, in its own documented order. All that is left is to say what
// happened and to select the row when the host's next snapshot carries it.
func (m *Model) applyRemoteWorktreeCreated(msg globalWorktreeCreatedMsg) tea.Cmd {
	m.createBusy = false
	if msg.Err != nil {
		m.createError = remoteActionError(msg.Err)
		m.createModal = nil
		return nil
	}
	m.pendingCreatedHost = msg.HostID
	m.pendingCreatedPath = msg.RemotePath
	m.pendingCreatedTmux = ""
	m.showIdleWorktrees = true
	m.closeCreateShell()
	return nil
}

// remoteShellResult is the subset of `sidecar create shell --json` this surface
// reads. Only the session matters: it is the identity the follow-up agent send
// addresses and the row the next snapshot will carry.
type remoteShellResult struct {
	Shell struct {
		DisplayName string `json:"displayName"`
		Session     string `json:"session"`
		WorkDir     string `json:"workDir"`
	} `json:"shell"`
}

// remoteWorktreeResult is the subset of `sidecar create worktree --json` this
// surface reads.
type remoteWorktreeResult struct {
	Shell struct {
		DisplayName string `json:"displayName"`
		Session     string `json:"session"`
	} `json:"shell"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// remoteProjectRef is the value passed as --project on the host.
//
// The host's own root path rather than its display name. The CLI's project
// matcher resolves --project against a registered project's key, its canonical
// path, or its directory's base name — a display name only coincides with the
// last of those, so "API Server" at /srv/api would not resolve at all.
//
// This is not a remote path being resolved here. It is an opaque string handed
// straight back to the machine that produced it, for that machine to resolve;
// nothing local ever joins, stats, or reads it.
func remoteProjectRef(project Project) string {
	if root := strings.TrimSpace(project.Path); root != "" {
		return root
	}
	return project.Name
}

// remoteWorkspaceProjectRef is the same identifier for a row rather than a
// picker entry.
func remoteWorkspaceProjectRef(workspace workspaceinventory.Workspace) string {
	if root := strings.TrimSpace(workspace.ProjectRoot); root != "" {
		return root
	}
	return workspace.ProjectName
}

// The argument lists. Pure functions, so the contract between this surface and
// the host's CLI is a thing a test can read rather than a thing a test has to
// reconstruct from a mock.

// remoteCreateShellArgs is `sidecar create shell` in one of a host's projects.
// An empty display name is left off entirely: the host names the shell from its
// own manifest,
// and guessing from here would number it against the wrong machine's shells.
func remoteCreateShellArgs(projectRef, displayName string) []string {
	args := []string{"create", "shell", "--project", projectRef}
	if displayName != "" {
		args = append(args, "--name", displayName)
	}
	return append(args, "--json")
}

// remoteShellSendArgs is `sidecar shell send --run`, the verb that starts an
// agent in a shell the caller is not sitting in. --project is passed with it
// because the host resolves --target against that project's manifest and
// refuses a session it does not own.
func remoteShellSendArgs(projectRef, session, command string) []string {
	return []string{"shell", "send", "--target", session, "--project", projectRef, "--run", command, "--json"}
}

// remoteWorktreeArgs is `sidecar create worktree`, in its planning form when
// plan is set. The two calls take the same arguments on purpose: the plan the
// confirmation showed and the worktree that gets created are resolved from one
// argument list, so they cannot describe different things.
func remoteWorktreeArgs(projectRef, name, base, agent string, skipPerms, plan bool) []string {
	args := []string{"create", "worktree", "--project", projectRef}
	if base != "" {
		args = append(args, "--base", base)
	}
	if agent != "" {
		args = append(args, "--agent", agent)
	}
	if skipPerms {
		args = append(args, "--skip-permissions")
	}
	if plan {
		args = append(args, "--plan")
	}
	return append(args, "--json", name)
}

// remoteRenameArgs is `sidecar shell rename`, which renames a shell record or a
// registered worktree's display name depending on what --target resolves to.
func remoteRenameArgs(projectRef, session, newName string) []string {
	return []string{"shell", "rename", "--target", session, "--project", projectRef, "--json", newName}
}

// remoteTargetSession is the tmux session name a remote row is addressed by.
//
// A worktree that is not running has no session in the snapshot, but its name
// is still derivable — WorktreeSessionName is a string transform over the last
// path element, not a filesystem lookup — and it is the same derivation the
// host's own target resolver matches against.
func remoteTargetSession(workspace workspaceinventory.Workspace) string {
	if session := strings.TrimSpace(workspace.TmuxName); session != "" {
		return session
	}
	if workspace.Kind == workspaceinventory.KindWorktree {
		return workspaceops.WorktreeSessionName(workspace.Path, workspace.Name)
	}
	return ""
}

// submitRemoteCreateShell creates a shell on a host and, when the form chose an
// agent, starts it there.
//
// Two round trips rather than one, mirroring exactly what the local path does:
// createManagedShell, then StartAgentInShell against the session that came
// back. The host names the session; nothing here predicts it.
func (m *Model) submitRemoteCreateShell(target createTarget, displayName, agentCommand string) tea.Cmd {
	registry := m.hostRegistry
	hostID, project := target.HostID, target.Project
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	projectRef := remoteProjectRef(project)
	createArgs := remoteCreateShellArgs(projectRef, displayName)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteCreateShellTimeout)
		defer cancel()
		reply := globalShellCreatedMsg{remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation}, Project: project}
		var result remoteShellResult
		if err := runRemoteSidecar(ctx, registry, hostID, createArgs, &result); err != nil {
			reply.Err = err
			return reply
		}
		reply.Tmux = result.Shell.Session
		if agentCommand == "" {
			return reply
		}
		if reply.Tmux == "" {
			reply.Err = errors.New("the host created the shell but did not name its session, so the agent was not started")
			return reply
		}
		if err := runRemoteSidecar(ctx, registry, hostID, remoteShellSendArgs(projectRef, reply.Tmux, agentCommand), nil); err != nil {
			reply.Err = err
		}
		return reply
	}
}

// planRemoteWorktree resolves a worktree plan on the host without creating
// anything. `--plan` stops after validation, so a cancelled confirmation leaves
// the remote machine exactly as it was.
func (m *Model) planRemoteWorktree(target createTarget, name, base, agent string, skipPerms bool) tea.Cmd {
	registry := m.hostRegistry
	hostID, project := target.HostID, target.Project
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	args := remoteWorktreeArgs(remoteProjectRef(project), name, base, agent, skipPerms, true)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteQuickTimeout)
		defer cancel()
		reply := globalWorktreePlannedMsg{remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation}, Project: project}
		var plan workspaceops.WorktreePlan
		if err := runRemoteSidecar(ctx, registry, hostID, args, &plan); err != nil {
			reply.Err = err
			return reply
		}
		reply.Plan = &plan
		return reply
	}
}

// executeRemoteWorktree creates the worktree the confirmation described.
//
// The host runs its whole documented sequence — execute, journal, identity,
// configured setup, launch — because that sequence is `sidecar create worktree`
// and re-deriving it from here would be a second implementation of the ordering
// the CLI already proves.
func (m *Model) executeRemoteWorktree(target createTarget, name, base, agent string, skipPerms bool) tea.Cmd {
	registry := m.hostRegistry
	hostID, project := target.HostID, target.Project
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	args := remoteWorktreeArgs(remoteProjectRef(project), name, base, agent, skipPerms, false)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteWorktreeExecTimeout)
		defer cancel()
		reply := globalWorktreeCreatedMsg{remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation}, Project: project}
		var result remoteWorktreeResult
		if err := runRemoteSidecar(ctx, registry, hostID, args, &result); err != nil {
			reply.Err = err
			return reply
		}
		reply.RemotePath = result.Path
		reply.RemoteSession = result.Shell.Session
		return reply
	}
}

// renameRemoteWorkspace renames a shell record or a worktree display name on
// the host. One verb covers both because `shell rename --target` resolves the
// target against the project's manifest and dispatches on what it found.
func (m *Model) renameRemoteWorkspace(workspace workspaceinventory.Workspace, newName string) tea.Cmd {
	registry := m.hostRegistry
	hostID := workspace.HostID
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	id := workspace.ID
	session := remoteTargetSession(workspace)
	args := remoteRenameArgs(remoteWorkspaceProjectRef(workspace), session, newName)
	if session == "" {
		return func() tea.Msg {
			return renameShellDoneMsg{
				remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation},
				ID:          id,
				NewName:     newName,
				Err:         errors.New("that row carries no tmux session name, so the host cannot be told which workspace to rename"),
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteQuickTimeout)
		defer cancel()
		reply := renameShellDoneMsg{remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation}, ID: id, NewName: newName}
		var result remoteRenameResult
		if err := runRemoteSidecar(ctx, registry, hostID, args, &result); err != nil {
			reply.Err = err
			return reply
		}
		if strings.TrimSpace(result.Name) != "" {
			reply.NewName = result.Name
		}
		return reply
	}
}

// remoteRenameResult is the subset of `sidecar shell rename --json` this
// surface reads. The host's normalisation wins over the one done locally for
// immediate feedback.
type remoteRenameResult struct {
	Name string `json:"name"`
}
