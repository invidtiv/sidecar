package overview

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/hostproto"
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

// remoteHostUnavailable reports why a mutation cannot even be dispatched to a
// host: it is registered in configuration but has no live client — disabled, or
// not connected. Its rows and projects can legitimately still be on screen
// (last-known state outlives the connection on purpose), so the mutation
// surfaces must ask this before sending, or the ssh call fails and its reply is
// then read as stale by hostReplyStale — which reports the misleading "was
// removed or retargeted", because registered-without-incarnation is exactly the
// state a disabled host sits in.
//
// The fences are the same two hostReplyStale reads, deliberately: there must be
// exactly one answer to "can this host be asked anything right now?".
func (m *Model) remoteHostUnavailable(hostID string) string {
	if hostID == "" {
		return ""
	}
	if m.hostRegistered == nil || !m.hostRegistered[hostID] {
		return ""
	}
	if m.hostIncarnations != nil {
		if _, ok := m.hostIncarnations[hostID]; ok {
			return ""
		}
	}
	return hostID + " is disabled or not connected, so nothing can be changed there"
}

// hostVerbs is what a host said its CLI understands, read from the hello it
// sent. The zero value — an unknown host, or one whose Sidecar predates the
// field — means "assume nothing", which is what makes an older host degrade
// rather than fail.
//
// From the retained Health.Hello rather than from a version string: dev builds
// carry git revisions, so version comparison decides nothing, and the hello
// survives a reconnect so a host that is momentarily stale still answers.
func (m *Model) hostVerbs(hostID string) hostproto.VerbCapabilities {
	if hostID == "" {
		return hostproto.VerbCapabilities{}
	}
	health, ok := m.hostHealth[hostID]
	if !ok || health.Hello == nil {
		return hostproto.VerbCapabilities{}
	}
	return health.Hello.Capabilities.Verbs
}

// createFormHostID is the host the create form's current project key names, or
// "" for a local key. It answers even when the project itself no longer
// resolves — which is exactly when a create error needs to say which machine
// went away.
func (m *Model) createFormHostID() string {
	if m.createForm == nil {
		return ""
	}
	hostID, _, _ := hosts.SplitScopedKey(m.createForm.ProjectKey())
	return hostID
}

// remoteReplyDropped is what a surface says when it throws an answer away
// because the host it came from is gone. Saying nothing would leave a modal
// spinning on a machine that no longer exists.
func remoteReplyDropped(hostID string) string {
	return hostID + " was removed or retargeted while that was running; its answer was dropped"
}

// missingCreateTarget is what a create says when the project it was built for
// can no longer be resolved: a host removed or retargeted between opening the
// form and pressing Create.
func missingCreateTarget(hostID string) string {
	if hostID == "" {
		return "the project this create was started for is no longer available"
	}
	return hostID + " is no longer available, so this create has nowhere to run"
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
		message := detail
		if fix := runErr.Fix(); fix != "" {
			message = detail + " — " + fix
		}
		// Log the whole thing as well as returning it. The actionable half of
		// this sentence is the second half, which is exactly the half a narrow
		// modal drops, and until now nothing wrote it anywhere: a user who saw
		// "the remote Sidecar did not accept this…" had no way, from inside the
		// running app, to find out what the host actually said. This goes to
		// the ordinary debug log, alongside the command and the remote's own
		// stderr, so `sidecar -debug` and a bug report can both reach it.
		slog.Warn("remote mutation failed",
			"host", runErr.HostID,
			"command", "sidecar "+strings.Join(runErr.Args, " "),
			"failure", string(runErr.Failure),
			"exit", runErr.ExitCode,
			"detail", detail,
			"fix", runErr.Fix(),
			"stderr", runErr.Stderr,
		)
		return message
	}
	slog.Warn("remote mutation failed", "err", err)
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
	// The pending selection is cleared only when it was queued for the host this
	// reply came from: it named a row on a machine this configuration no longer
	// points at, and leaving it set makes the next creation search the wrong
	// snapshot. A stale reply from one host must not wipe the selection for a
	// shell just created on another (or locally) — that selection is still
	// waiting on a perfectly healthy snapshot.
	if m.pendingCreatedHost == hostID {
		m.clearPendingCreated()
	}
	// The form is only touched when it is still the form that asked. A user who
	// removed the host and immediately started a LOCAL create has an open
	// create modal that has nothing to do with this answer, and writing "mac-mini
	// was removed" into it turns an unrelated form into a stuck one.
	if !m.createOpen || m.createTargetHost != hostID {
		return nil
	}
	// And only when the form still points there. createTargetHost records who
	// asked; the form's own project key records where the next submission would
	// go, and a user who has already retargeted it has moved on.
	if target, ok := m.selectedCreateTarget(); ok && target.HostID != hostID {
		return nil
	}
	m.createBusy = false
	m.createModal = nil
	m.createPlan = nil
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

// The result types below each say which fields make a decoded object their
// verb's answer (hosts.ResultValidator).
//
// Without that, an object is this surface's result if it merely parses: Go
// ignores unknown fields and tolerates missing ones, so a login profile
// emitting `{"level":"info","msg":"loading nvm"}` decoded into every one of
// these with a nil error and an all-zero value. The consequences were not
// cosmetic — a blank confirmation for a worktree that would really be created,
// and a shell created on a host that the browser then never mentioned again —
// so each type states its own floor rather than trusting the decode.

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

// ValidRemoteResult: the session is the whole point of the call.
func (r remoteShellResult) ValidRemoteResult() bool {
	return strings.TrimSpace(r.Shell.Session) != ""
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

// ValidRemoteResult: the path is what the pending selection matches on and the
// session is how the row is addressed afterwards. `sidecar create worktree`
// always reports both, and a log line carrying a "path" key reports neither of
// the pair.
func (r remoteWorktreeResult) ValidRemoteResult() bool {
	return strings.TrimSpace(r.Path) != "" && strings.TrimSpace(r.Shell.Session) != ""
}

// remoteWorktreePlan carries `sidecar create worktree --plan --json` with a
// statement of what makes it a plan.
//
// Embedded rather than duplicated: the confirmation renders workspaceops'
// own plan fields, and a second copy of that struct here would be a second
// thing to keep in step with the CLI. The embedding promotes every JSON field,
// so what decodes is identical.
type remoteWorktreePlan struct {
	workspaceops.WorktreePlan
}

// ValidRemoteResult: branch and path are the two lines the confirmation is
// built from. A plan missing either is the blank confirmation this guard
// exists to stop — the user reads "Create  at " and presses Create anyway.
func (p remoteWorktreePlan) ValidRemoteResult() bool {
	return strings.TrimSpace(p.Branch) != "" && strings.TrimSpace(p.Path) != ""
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
//
// --agent is the durable half of "this is a Claude shell". The local create
// writes AgentType into shells.json as it creates, so HasAgent() is true from
// that moment; the remote create used to send no agent at all and leave the
// answer entirely to live screen identification, so a remote agent shell was
// absent from the Activity board while its agent booted and dropped off the
// board whenever identification missed a frame — where its local twin kept its
// card because the manifest said so.
//
// --run is the other half: the command that starts the process. The two travel
// together because the host reads them together — a create that names a command
// records the family and runs exactly that, and never reaches for agent control
// to start something of its own.
//
// agentType arrives empty for a host that did not advertise the flag, which is
// how an older machine keeps working; submitRemoteCreateShell owns that
// decision, because it is the only thing here that knows which host is being
// asked.
func remoteCreateShellArgs(projectRef, displayName, agentType, runCommand string) []string {
	args := []string{"create", "shell", "--project", projectRef}
	if displayName != "" {
		args = append(args, "--name", displayName)
	}
	if agentType != "" {
		args = append(args, "--agent", agentType)
	}
	if runCommand != "" {
		args = append(args, "--run", runCommand)
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
// plan is set. The two calls take the same base arguments on purpose, and the
// execute call additionally pins expectOID — the SourceOID of the plan the
// confirmation showed. Re-running from raw arguments alone is not enough: the
// host re-resolves the ref at execute time, and a ref that moved between plan
// and Create (an agent pushing to main is this feature's normal operating
// condition) would silently produce a worktree at the new head, where the
// identical local sequence refuses. With the pin, the host refuses with exit 5
// and the confirmation shows why.
func remoteWorktreeArgs(projectRef, name, base, agent, expectOID string, skipPerms, plan bool) []string {
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
	if expectOID != "" {
		args = append(args, "--expect-source-oid", expectOID)
	}
	if plan {
		args = append(args, "--plan")
	}
	// `--` before the positional. A worktree may legitimately be named "-fix",
	// and without the terminator the host's parser reads it as a flag and exits
	// 2 — which, being a usage error, told the user to update Sidecar.
	return append(args, "--json", "--", name)
}

// remoteRenameArgs is `sidecar shell rename`, which renames a shell record or a
// registered worktree's display name depending on what --target resolves to.
func remoteRenameArgs(projectRef, session, newName string) []string {
	// `--` for the same reason remoteWorktreeArgs passes one: shellstate accepts
	// a leading dash in a display name, so "-wip" must reach the host as a name
	// rather than as an unknown option.
	return []string{"shell", "rename", "--target", session, "--project", projectRef, "--json", "--", newName}
}

// remoteDeleteShellArgs is `sidecar shell delete`, the host-side verb that
// closes a managed shell's tmux session and tombstones its record.
//
// --project travels with --target for the reason send's does: the host resolves
// the target against that project's manifest and refuses a session it does not
// own, which is what keeps a name collision from becoming a killed session
// belonging to somebody else. No `--` terminator, because this verb takes no
// positional and a tmux session name is never a flag.
func remoteDeleteShellArgs(projectRef, session string) []string {
	return []string{"shell", "delete", "--target", session, "--project", projectRef, "--json"}
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
// One round trip where the host understands --agent, two where it does not. The
// host names the session; nothing here predicts it.
//
// agentType is durable state the host writes into its own manifest as the shell
// appears; agentCommand is this viewer's config-only resolution of how to launch
// it. Sending only the second is what left a remote agent shell with no durable
// evidence of its agent — see remoteCreateShellArgs.
//
// Sending both in the create is also what keeps the launch unambiguous. On a
// host with agent control enabled, `create shell --agent X` alone starts the
// provider itself; a `shell send --run` behind it would then be a second launch
// into a pane that already has one. Naming the command in the create is the
// caller saying it owns the launch, and the host does exactly that and no more.
func (m *Model) submitRemoteCreateShell(target createTarget, displayName, agentType, agentCommand string) tea.Cmd {
	registry := m.hostRegistry
	hostID, project := target.HostID, target.Project
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	projectRef := remoteProjectRef(project)
	// --agent only where the host said it understands it. A Sidecar that
	// predates the flag answers `unknown option "--agent"` and exits 2, so
	// sending it unconditionally turned a durability improvement into a total
	// failure of remote agent creation against a machine the user had not
	// updated yet. Dropping it falls back to exactly the two-step behaviour that
	// preceded it: the shell is created, and the `shell send --run` below starts
	// the agent.
	createRun := ""
	if m.hostVerbs(hostID).CreateShellAgent {
		if agentType != "" {
			createRun, agentCommand = agentCommand, ""
		}
	} else {
		agentType = ""
	}
	createArgs := remoteCreateShellArgs(projectRef, displayName, agentType, createRun)
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
	args := remoteWorktreeArgs(remoteProjectRef(project), name, base, agent, "", skipPerms, true)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteQuickTimeout)
		defer cancel()
		reply := globalWorktreePlannedMsg{remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation}, Project: project}
		var plan remoteWorktreePlan
		if err := runRemoteSidecar(ctx, registry, hostID, args, &plan); err != nil {
			reply.Err = err
			return reply
		}
		reply.Plan = &plan.WorktreePlan
		return reply
	}
}

// executeRemoteWorktree creates the worktree the confirmation described.
// expectOID is the confirmed plan's SourceOID; the host refuses rather than
// build from a ref that has moved since the confirmation was shown.
//
// The host runs its whole documented sequence — execute, journal, identity,
// configured setup, launch — because that sequence is `sidecar create worktree`
// and re-deriving it from here would be a second implementation of the ordering
// the CLI already proves.
func (m *Model) executeRemoteWorktree(target createTarget, name, base, agent, expectOID string, skipPerms bool) tea.Cmd {
	registry := m.hostRegistry
	hostID, project := target.HostID, target.Project
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	args := remoteWorktreeArgs(remoteProjectRef(project), name, base, agent, expectOID, skipPerms, false)
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

// deleteRemoteShell closes a shell on the host that owns it and forgets its
// record there, by running the host's own `sidecar shell delete`.
//
// The row is dropped when the answer comes back, before the host's next
// snapshot arrives — a latency mask, not a second source of truth. hostserve
// watches the shells.json it reports, so the machine that owns the state
// confirms the removal within a coalesce window; nothing here synthesizes the
// absence and nothing here would notice if the delete had silently failed,
// which is why a failure keeps the confirmation open and says what the host
// said.
func (m *Model) deleteRemoteShell(workspace workspaceinventory.Workspace) tea.Cmd {
	registry := m.hostRegistry
	hostID := workspace.HostID
	incarnation := m.hostIncarnationFor(hostID)
	parent := m.hostContext()
	id := workspace.ID
	// The project is carried by its host-scoped key and without a path: the
	// shared reply handler ends in a local re-inventory, and the safest thing to
	// hand it is an identifier this machine cannot resolve into a directory.
	project := Project{Name: workspace.ProjectName, Key: workspace.ProjectKey}

	refuse := func(reason string) tea.Cmd {
		return func() tea.Msg {
			return globalShellDeletedMsg{
				remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation},
				Project:     project, WorkspaceID: id, Err: errors.New(reason),
			}
		}
	}
	// Asked before dispatch: a host that is registered but has no live client
	// cannot be asked anything, and sending anyway produces an ssh failure whose
	// reply reads as "removed or retargeted" — the wrong sentence for a host the
	// user disabled.
	if reason := m.remoteHostUnavailable(hostID); reason != "" {
		return refuse(reason)
	}
	session := remoteTargetSession(workspace)
	if session == "" {
		return refuse("that row carries no tmux session name, so the host cannot be told which shell to delete")
	}
	args := remoteDeleteShellArgs(remoteWorkspaceProjectRef(workspace), session)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, remoteQuickTimeout)
		defer cancel()
		reply := globalShellDeletedMsg{
			remoteReply: remoteReply{HostID: hostID, Incarnation: incarnation},
			Project:     project, WorkspaceID: id,
		}
		var result remoteShellDeleteResult
		if err := runRemoteSidecar(ctx, registry, hostID, args, &result); err != nil {
			// Shaped here rather than at the confirmation, so the sentence the
			// user reads is the host's own words plus the fix — the same
			// treatment every other remote failure gets — and so the whole
			// failure, including the half a narrow modal drops, reaches the
			// debug log.
			reply.Err = errors.New(remoteActionError(err))
			return reply
		}
		return reply
	}
}

// dropRemoteDeleteReply throws away a delete answer from a host that is no
// longer the host it was addressed to — removed from configuration, or
// retargeted at another machine, while the round trip was in flight.
//
// The same fence every other remote reply opens with (hostReplyStale), and it
// matters more here than anywhere else: applying the answer would run
// forgetSessionsRow, so a `deleted` from the PREVIOUS machine would drop a row
// belonging to the current one, and the correcting snapshot comes from a host
// this configuration no longer watches. The 30s remoteQuickTimeout is the whole
// window a user has to retarget a host in Configuration — which calls SyncHosts
// and bumps or clears the incarnation — while a delete is running.
//
// The row is deliberately left alone rather than dropped, and the confirmation
// is left open with the reason rather than closed: a delete that silently
// appeared to work against a machine that is no longer addressed is exactly the
// outcome the fence exists to prevent. On the failure branch this also replaces
// the raw ssh error the user would otherwise read with the sentence that is
// actually true about their configuration.
func (m *Model) dropRemoteDeleteReply(msg globalShellDeletedMsg) tea.Cmd {
	// Only when the confirmation is still the one that asked. A user who
	// cancelled and opened a delete for another row has a modal that has nothing
	// to do with this answer, and clearing its busy flag would un-stick a round
	// trip that is still running.
	if !m.deleteOpen || m.deleteWorkspace.ID != msg.WorkspaceID {
		return nil
	}
	m.deleteBusy = false
	m.deleteModal = nil
	m.deleteError = remoteReplyDropped(msg.HostID)
	return nil
}

// dropRemoteWorkspaceRow removes a row from the last-known results of the host
// that reported it, so a deletion the user just confirmed is visible before
// that host says so again.
//
// Only the optimism is here; the truth is on the host. Its next snapshot
// restates the whole project, so a delete that did not really happen puts the
// row back rather than leaving this browser lying indefinitely — and with
// hostserve watching the shells.json it reports, that correction arrives within
// a coalesce window rather than on the next inventory tick.
//
// A local row is left alone: its project is re-inventoried after the mutation,
// which is a stronger answer than this one.
func (m *Model) dropRemoteWorkspaceRow(id string) {
	workspace, ok := m.catalog[id]
	if !ok || workspace.HostID == "" {
		return
	}
	results := m.hostResults[workspace.HostID]
	for i := range results {
		for j := range results[i].Workspaces {
			if results[i].Workspaces[j].ID != id {
				continue
			}
			results[i].Workspaces = append(results[i].Workspaces[:j], results[i].Workspaces[j+1:]...)
			m.hostResults[workspace.HostID] = results
			// syncBoard rebuilds the list projection too, so the row leaves both
			// surfaces at once — a card that outlived its row is the parity bug
			// the shared catalog exists to prevent.
			m.syncBoard()
			return
		}
	}
}

// remoteShellDeleteResult is the subset of `sidecar shell delete --json` this
// surface reads. Nothing here needs the payload — the row is addressed by the
// ID it already has — so the type exists to say what a real answer looks like.
type remoteShellDeleteResult struct {
	Shell  string `json:"shell"`
	Status string `json:"status"`
}

// remoteShellDeleteStatus is the only status `sidecar shell delete` writes
// (shellStatusDeleted, internal/cli/shell_delete.go). It is restated here rather
// than imported because this is the viewer's end of a host boundary and the
// value on the wire may come from a different build of that CLI.
const remoteShellDeleteStatus = "deleted"

// ValidRemoteResult: the deleted session, and the one status the verb writes.
// Without this floor an object that merely parses is accepted as the answer, and
// a delete that never ran is reported as a delete that did — the failure
// recorded at the top of this file's result section, arriving here on the one
// verb whose optimistic row drop would then hide a live shell until the host's
// next snapshot brought it back.
//
// The status is matched rather than merely required, because "non-empty" is not
// a statement about this verb at all. A future host that reported a partial
// failure — the session killed, the record not tombstoned — would satisfy a
// non-empty check and be read as success, and the row would be dropped for a
// shell whose identity is still there. Refusing an unrecognised status instead
// surfaces the mutation as failed and keeps the row, which is the direction this
// surface must err in.
func (r remoteShellDeleteResult) ValidRemoteResult() bool {
	return strings.TrimSpace(r.Shell) != "" && strings.TrimSpace(r.Status) == remoteShellDeleteStatus
}

// remoteRenameResult is the subset of `sidecar shell rename --json` this
// surface reads. The host's normalisation wins over the one done locally for
// immediate feedback.
//
// Shell is carried only to recognise the result: the verb always names the
// session it renamed, and a log line that happens to have a "name" key does
// not.
type remoteRenameResult struct {
	Shell string `json:"shell"`
	Name  string `json:"name"`
}

// ValidRemoteResult: the renamed session and its new name are both always
// present in shellstate.RenameResult.
func (r remoteRenameResult) ValidRemoteResult() bool {
	return strings.TrimSpace(r.Shell) != "" && strings.TrimSpace(r.Name) != ""
}
