package workspaceops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

// Creating a shell is the smallest thing either Workspaces surface does, and it
// is the first one the global browser needs. It is here rather than in the
// project plugin because nothing about it is project-plugin shaped: given a
// directory, a session name, a display name, and a pane size, it makes a
// detached tmux session and reports what it made.
//
// The caller supplies the names. Both are project-scoped decisions — the next
// free "Shell N", a session name that does not collide — and the plugin and a
// global host resolve them from different places. Passing them in is what lets
// one implementation serve both.

// ShellSpec is everything creating a shell needs. Every field is resolved by
// the caller; nothing here is discovered.
type ShellSpec struct {
	// WorkDir is the directory the session starts in.
	WorkDir string
	// SessionName is the tmux session identity, already checked for collisions
	// by whoever owns the naming scheme.
	SessionName string
	// DisplayName is what a human sees. It is published to the session
	// environment as a cue; the manifest remains the authority.
	DisplayName string
	// Cols and Rows size the pane at creation. Without them tmux uses its
	// default 80x24 and anything started before the follow-up resize — an
	// editor especially — lays itself out for 24 rows. Zero means let tmux
	// decide.
	Cols, Rows int
}

// ShellResult reports what was created. PaneID is empty when tmux created the
// session but did not report a pane, which is survivable: the session exists
// and the pane can be resolved again later.
type ShellResult struct {
	SessionName string
	PaneID      string
}

// ManagedShellSpec adds the durable project identity that turns a tmux
// session into a Sidecar shell. The operation resolves no project state on its
// own; the caller explicitly chooses the owning project and agent metadata.
type ManagedShellSpec struct {
	ShellSpec
	ProjectRoot string
	AgentType   string
	SkipPerms   bool
}

// CreateManagedShell creates the tmux session and then records it in the
// owning project's manifest. A newly-created session is rolled back if the
// durable identity cannot be written; a pre-existing retry is never killed.
func CreateManagedShell(spec ManagedShellSpec) (ShellResult, error) {
	existed := SessionExists(spec.SessionName)
	result, err := CreateShell(spec.ShellSpec)
	if err != nil {
		return result, err
	}
	projectDir, err := projectdir.Resolve(spec.ProjectRoot)
	if err == nil {
		definition := shellstate.Definition{
			TmuxName: spec.SessionName, DisplayName: spec.DisplayName,
			Namespace: tmuxenv.Namespace(), CreatedAt: time.Now(), AgentType: spec.AgentType,
			SkipPerms: spec.SkipPerms, WorkDir: spec.WorkDir,
		}
		// Creation is the strongest liveness evidence there is: the session was
		// just made, in this server, by this process. Stamping cold-restore
		// eligibility here rather than waiting for a refresh cycle closes the
		// window in which a shell created seconds before a crash would come back
		// as "never confirmed live" and be left for the user to recreate by hand.
		// It costs no extra write — the record is being written anyway.
		if server := tmuxserver.Combine(tmuxserver.Socket(), ServerPID()).ServerID(); server != "" {
			definition.Restore = &shellstate.RestoreState{
				Eligible:        true,
				LastSeenServer:  server,
				LastSeenAliveAt: time.Now().UTC(),
			}
		}
		err = shellstate.AddAtPath(filepath.Join(projectDir, "shells.json"), definition)
	}
	if err != nil {
		if !existed {
			_ = exec.Command("tmux", "kill-session", "-t", spec.SessionName).Run()
		}
		return result, fmt.Errorf("record shell identity: %w", err)
	}
	return result, nil
}

// DeleteManagedShell removes the durable identity and then closes the exact
// tmux session. If tmux has already exited, the requested state is achieved.
func DeleteManagedShell(projectRoot, sessionName, namespace string) error {
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return err
	}
	if err := shellstate.RemoveAtPath(filepath.Join(projectDir, "shells.json"), shellstate.Identity{TmuxName: sessionName, Namespace: namespace}); err != nil {
		return err
	}
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	if output, err := cmd.CombinedOutput(); err != nil && SessionExists(sessionName) {
		return fmt.Errorf("close tmux session: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// ForgetManagedShell drops the durable identity of a shell whose tmux session
// is already gone. It is DeleteManagedShell without the kill: there is nothing
// left to close, and issuing kill-session anyway would be one more spawn and
// one more way to hit the wrong target. Callers must have positive evidence the
// session is gone (see internal/shellliveness); this operation does not
// re-check that, because the surfaces that call it already asked tmux.
//
// observedAt is the CreatedAt the caller saw when it decided. The removal is
// refused with shellstate.ErrShellChanged if the entry on disk is newer, so a
// shell created under the same tmux name while the caller was confirming keeps
// its identity. The comparison happens under the manifest's exclusive lock,
// which is the same lock the creating write takes. Pass the zero time to remove
// unconditionally.
func ForgetManagedShell(projectRoot, sessionName, namespace string, observedAt time.Time) error {
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return err
	}
	return shellstate.RemoveIfUnchangedAtPath(
		filepath.Join(projectDir, "shells.json"),
		shellstate.Identity{TmuxName: sessionName, Namespace: namespace},
		observedAt,
	)
}

// ReapManagedShell is what a liveness pass calls instead of ForgetManagedShell.
//
// It is the same conditional write with one question asked first: did this shell
// exit, or did its tmux server? When the server is gone or has been replaced,
// the record is kept and marked as a cold-restore candidate instead of being
// tombstoned, because a session that vanished along with the process hosting it
// has not been closed by anyone — it is exactly what a cold restore exists to
// bring back.
//
// This is the fix for the failure mode that destroyed a user's sidecar and braid
// shell records: when a server dies, every session disappears in the same
// instant, and a per-shell "is this one gone?" question has no true answer.
// Answering it anyway tombstones the whole file. Routing the liveness path
// through here means a server death can no longer produce a deletion at all,
// rather than being guarded against producing one.
//
// currentServer is the tmux server observed at the moment of the write, as a
// "pid=N" id from tmuxserver.Incarnation.ServerID, or empty when no server is
// running.
func ReapManagedShell(projectRoot, sessionName, namespace string, observedAt time.Time, server tmuxserver.Incarnation) (shellstate.ReapOutcome, error) {
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return shellstate.ReapAbsent, err
	}
	return shellstate.ForgetOrPreserveAtPath(
		filepath.Join(projectDir, "shells.json"),
		shellstate.Identity{TmuxName: sessionName, Namespace: namespace},
		observedAt,
		ServerStateOf(server),
	)
}

// ServerStateOf maps an observed tmux incarnation onto the evidence the shell
// writer reasons about.
//
// The fourth case is the one that matters and the one that was missed: an
// incarnation can be Present and still carry pid 0, because a socket stat alone
// identifies a socket rather than a server process. That is not "the server is
// gone" and it is not "the server is this one" — it is "I cannot tell which
// server this is", and it has to arrive at the writer as exactly that. Reading
// it as death is what made two production surfaces stop tombstoning entirely.
func ServerStateOf(inc tmuxserver.Incarnation) shellstate.ServerState {
	switch {
	case inc.IsAbsent():
		return shellstate.ServerGone()
	case inc.IsPresent():
		if id := inc.ServerID(); id != "" {
			return shellstate.ServerRunning(id)
		}
		return shellstate.ServerUnknown()
	default:
		return shellstate.ServerUnknown()
	}
}

// ServerIncarnation observes the tmux server with its pid, distinguishing "no
// server is running" from "the question could not be answered".
//
// ServerPID alone cannot make that distinction — it returns 0 for both — and a
// caller that needs to decide whether a shell died with its server must not
// treat a failed subprocess as a dead server.
func ServerIncarnation() tmuxserver.Incarnation {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pid}").CombinedOutput()
	if err != nil {
		if noServerRunning(string(out)) {
			return tmuxserver.Absent()
		}
		return tmuxserver.Unknown()
	}
	pid, ok := tmuxserver.ParsePID(strings.TrimSpace(string(out)))
	if !ok {
		return tmuxserver.Unknown()
	}
	return tmuxserver.Combine(tmuxserver.Socket(), pid)
}

func noServerRunning(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "no sessions") ||
		strings.Contains(message, "error connecting to")
}

// ReapManagedShellFunc adapts ReapManagedShell to shellliveness.ForgetFunc.
//
// The outcome is dropped here rather than never produced: ReapManagedShell
// returns it because preserved-versus-tombstoned is the fact worth asserting on
// in a test, while the liveness surfaces need only success or failure — a
// preserved record simply reappears as an offline row on the next refresh, which
// is the correct display for a cold-restore candidate.
func ReapManagedShellFunc(projectRoot, sessionName, namespace string, observedAt time.Time, server tmuxserver.Incarnation) error {
	_, err := ReapManagedShell(projectRoot, sessionName, namespace, observedAt, server)
	return err
}

// ObserveManagedShellsLive records that these sessions are running under this
// tmux server, which is what makes them cold-restore candidates later.
//
// It writes only when a shell's marker actually changes, so calling it once per
// refresh cycle costs one manifest read and, in the steady state, no write.
func ObserveManagedShellsLive(projectRoot, namespace, currentServer string, sessionNames []string, now time.Time) (int, error) {
	if currentServer == "" || len(sessionNames) == 0 {
		return 0, nil
	}
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return 0, err
	}
	ids := make([]shellstate.Identity, 0, len(sessionNames))
	for _, name := range sessionNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		ids = append(ids, shellstate.Identity{TmuxName: name, Namespace: namespace})
	}
	return shellstate.ObserveLiveAtPath(filepath.Join(projectDir, "shells.json"), currentServer, ids, now)
}

// RestoreManagedShell moves a forgotten shell record back onto the project's
// live list. It does not start a tmux session.
func RestoreManagedShell(projectRoot, sessionName, namespace string) (shellstate.Definition, error) {
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return shellstate.Definition{}, err
	}
	return shellstate.RestoreAtPath(
		filepath.Join(projectDir, "shells.json"),
		shellstate.Identity{TmuxName: sessionName, Namespace: namespace},
	)
}

// ShellNames resolves the next generated display and session names from one
// project's inventory. It tolerates legacy names while never reusing a suffix.
func ShellNames(projectRoot string, existing []shellstate.Definition) (displayName, sessionName string) {
	base := "sidecar-sh-" + sanitizeShellName(filepath.Base(projectRoot))
	maxIndex := 0
	for _, shell := range existing {
		if shell.TmuxName == base {
			maxIndex = max(maxIndex, 1)
			continue
		}
		prefix := base + "-"
		if !strings.HasPrefix(shell.TmuxName, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(shell.TmuxName, prefix)); err == nil {
			maxIndex = max(maxIndex, n)
		}
	}
	next := maxIndex + 1
	return fmt.Sprintf("Shell %d", next), fmt.Sprintf("%s-%d", base, next)
}

func sanitizeShellName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// TmuxInstalled reports whether a tmux binary is on PATH. Creating a shell
// without one fails in a way worth refusing early and explaining.
func TmuxInstalled() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// SessionExists reports whether a tmux session of that name is already running.
func SessionExists(name string) bool {
	if name == "" {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

// PaneID returns a session's first pane identifier, or "" if tmux cannot say.
// Callers that cache do so around this; there is one tmux call, here.
func PaneID(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	output, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_id}").Output()
	if err != nil {
		return ""
	}
	paneID := strings.TrimSpace(string(output))
	if idx := strings.Index(paneID, "\n"); idx > 0 {
		paneID = paneID[:idx]
	}
	return paneID
}

// CreateShell makes the detached session described by spec.
//
// An existing session of that name is treated as success rather than an error:
// the caller asked for a session by that name and there is one. That is what
// makes a retry after an interrupted create safe.
func CreateShell(spec ShellSpec) (ShellResult, error) {
	result := ShellResult{SessionName: spec.SessionName}
	if spec.SessionName == "" {
		return result, fmt.Errorf("shell session name is required")
	}
	if SessionExists(spec.SessionName) {
		result.PaneID = PaneID(spec.SessionName)
		return result, nil
	}

	args := []string{
		"new-session",
		"-d",
		"-s", spec.SessionName,
		"-c", spec.WorkDir,
	}
	if spec.Cols > 0 && spec.Rows > 0 {
		args = append(args, "-x", strconv.Itoa(spec.Cols), "-y", strconv.Itoa(spec.Rows))
	}
	if err := NewSessionWithIdentity(args, spec.SessionName, spec.DisplayName); err != nil {
		return result, fmt.Errorf("create shell session: %w", err)
	}
	result.PaneID = PaneID(spec.SessionName)
	return result, nil
}

// NewSessionWithIdentity creates the session carrying its identity
// environment, falling back to a plain create if this tmux rejects
// new-session -e (added in tmux 3.2). Losing the environment cue is a degraded
// shell; failing to create the session is a broken one, and the fallback still
// publishes the values to the session environment for any pane opened later.
//
// It is exported for the creation paths that build their own new-session
// arguments rather than going through CreateShell.
func NewSessionWithIdentity(args []string, sessionName, displayName string) error {
	withEnv := append(append([]string(nil), args...), ShellEnvArgs(sessionName, displayName)...)
	if err := tty.NewSession(withEnv...); err == nil {
		return nil
	}
	if err := tty.NewSession(args...); err != nil {
		return err
	}
	SetShellEnv(sessionName, displayName)
	return nil
}

// ShellEnvArgs is the new-session flag set that publishes a shell's identity
// into its own environment, so a pane can name itself without asking Sidecar.
//
// It also publishes the managed-shell contract documented on
// [shellstate.ManagedEnv], which is what tells a provider hook it is allowed to
// report lifecycle state and which server incarnation to namespace it under.
//
// This function and [SetShellEnv] are a pair and must stay in step: the -e form
// needs tmux 3.2, and SetShellEnv is the fallback for older tmux and for the
// recreate paths. Anything added to one and not the other silently vanishes on
// exactly the machines least likely to be tested on.
func ShellEnvArgs(sessionName, displayName string) []string {
	args := []string{
		"-e", shellstate.SessionEnv + "=" + sessionName,
		"-e", shellstate.NameEnv + "=" + displayName,
	}
	for k, v := range managedShellEnv() {
		args = append(args, "-e", k+"="+v)
	}
	return args
}

// SetShellEnv publishes the display name to the tmux session environment.
// Panes already running keep the value they were created with — the manifest
// and `sidecar shell rename` remain the authority, this is only the cue.
func SetShellEnv(sessionName, displayName string) {
	if sessionName == "" {
		return
	}
	_ = tty.SetSessionEnv(sessionName, shellstate.SessionEnv, sessionName)
	_ = tty.SetSessionEnv(sessionName, shellstate.NameEnv, displayName)
	for k, v := range managedShellEnv() {
		_ = tty.SetSessionEnv(sessionName, k, v)
	}
}

// managedShellEnv builds the environment contract from the creating process's
// own view of the world.
//
// Every value is derived here, by Sidecar, and never accepted from provider
// input. A key whose value cannot be determined is omitted rather than set to
// something invented: a hook that finds an incomplete contract falls back to
// screen detection, which is correct, whereas a hook that trusts a guessed
// server or host would write records that can never be matched to a live pane.
//
// Ordering is not guaranteed because the result is a map, which is fine for
// `-e` flags and for set-environment, but means callers must not depend on
// argument order.
func managedShellEnv() map[string]string {
	env := map[string]string{
		shellstate.ManagedEnv: "1",
	}
	if ns := tmuxenv.Namespace(); ns != "" {
		env[shellstate.NamespaceEnv] = ns
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		env[shellstate.HostEnv] = host
	}
	if pid := ServerPID(); pid > 0 {
		env[shellstate.ServerEnv] = strconv.Itoa(pid)
	}
	// os.Executable resolves the running binary rather than searching PATH, so
	// a hook invokes the same Sidecar that created its shell even when another
	// one wins PATH — which, given this repo ships managed installs alongside a
	// Homebrew one, is the common case rather than the exotic one.
	if exe, err := os.Executable(); err == nil && exe != "" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
			exe = resolved
		}
		env[shellstate.BinEnv] = exe
	}
	return env
}

// ServerPID returns the PID of the tmux server on the current socket, or 0 when
// there is none or it cannot be read.
func ServerPID() int {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pid}").Output()
	if err != nil {
		return 0
	}
	pid, ok := tmuxserver.ParsePID(strings.TrimSpace(string(out)))
	if !ok {
		return 0
	}
	return pid
}
