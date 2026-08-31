// Package lifecycleenv resolves the Sidecar-managed shell context a lifecycle
// report is written against.
//
// It is the boundary where untrusted provider input meets Sidecar's own view of
// the world, and the rule at that boundary is one-directional: every field that
// identifies *where* a report applies — host, tmux server, pane, provider
// process — is derived here from the environment and from live tmux, and none
// of it can be supplied or overridden by the caller. Provider input chooses only
// what to say (a lane, an outcome, a reason code), never who to say it about.
// That is what stops a hook reporting for another pane, another server, or
// another user's run.
//
// # Failing open
//
// A hook runs in the agent's critical path. The plan's rule is absolute: a
// reporting failure is diagnostic and must never change the provider's own
// behavior. So [Resolve] returns a Context with Managed false — and no error —
// for every ordinary "not applicable" case: not inside a Sidecar shell, no tmux,
// no pane, a shell created before this contract existed. The caller exits 0 and
// says nothing. An error is reserved for a context that claims to be managed but
// does not check out, which is a real problem worth surfacing.
package lifecycleenv

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// ErrContextMismatch means the environment claimed a managed shell but the live
// pane, server, or host did not agree. It maps to
// [agentlifecycle.ErrInvalidContext].
var ErrContextMismatch = errors.New("lifecycleenv: claimed context does not match the live one")

// Context is a verified managed-shell context.
//
// Identity is partially filled: Host, ServerIncarnation, PaneID, and
// ProcessGeneration are resolved here. Provider, RunID, and SessionFingerprint
// depend on what the caller knows about the provider and are filled by
// [Context.IdentityFor].
type Context struct {
	// Managed reports whether this process is inside a Sidecar-managed shell
	// with a complete environment contract. When false, every other field is
	// zero and a hook must do nothing at all.
	Managed bool

	// Session is the tmux session name that owns the pane.
	Session string
	// Namespace is the tmux socket path identifying this host-local namespace.
	Namespace string
	// Bin is the absolute path of the Sidecar binary that created the shell.
	Bin string

	// Host, ServerIncarnation, PaneID, and ProcessGeneration are the verified
	// identity components.
	Host              string
	ServerIncarnation string
	PaneID            string
	ProcessGeneration string

	// salt is the host-local salt used to fingerprint provider session ids.
	salt []byte
}

// IdentityFor completes the identity for one provider and optional provider
// session identifier.
//
// The run id is derived from the process generation and the session
// fingerprint together, so a run rotates when either changes: a provider
// restart in the same pane is a new run even if it resumes the same session,
// and a session switch inside one process is a new run even though the process
// did not change. Both are discontinuities that must not let earlier reports
// keep authority.
func (c Context) IdentityFor(provider, sessionID string) agentlifecycle.Identity {
	id := agentlifecycle.Identity{
		Host:              c.Host,
		ServerIncarnation: c.ServerIncarnation,
		PaneID:            c.PaneID,
		Provider:          provider,
		ProcessGeneration: c.ProcessGeneration,
	}
	if sessionID != "" {
		id.SessionFingerprint = c.Fingerprint(sessionID)
	}
	id.RunID = shortDigest(c.salt, "run\x00"+c.ProcessGeneration+"\x00"+id.SessionFingerprint)
	return id
}

// Fingerprint returns the host-salted digest of a provider session identifier.
//
// The digest is all the lifecycle store ever retains. The exact reference, where
// session restore needs it, travels separately to the agentsession-owned shell
// binding — so a leaked lifecycle log cannot be used to address anyone's
// provider sessions.
func (c Context) Fingerprint(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return shortDigest(c.salt, "session\x00"+sessionID)
}

func shortDigest(salt []byte, s string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// Resolve reads and verifies the managed-shell context.
//
// stateDir is where the host salt lives. An empty stateDir still resolves
// everything except the salt, which degrades fingerprints to unsalted digests
// rather than failing — a weaker privacy property, but a working one, and the
// alternative is a hook that cannot report because a directory was missing.
func Resolve(stateDir string) (Context, error) {
	// The one cheap check that decides whether any of this applies. A hook
	// outside a Sidecar shell reaches here, learns the answer with no
	// subprocess and no file access, and exits.
	if os.Getenv(shellstate.ManagedEnv) != "1" {
		return Context{}, nil
	}
	pane := tmuxenv.HostingPane()
	if pane == "" {
		// Managed shells always have a pane. Without one there is nothing a
		// report could be about, so this is "not applicable", not an error.
		return Context{}, nil
	}

	claimedSession := os.Getenv(shellstate.SessionEnv)
	claimedServer := os.Getenv(shellstate.ServerEnv)

	// Ask tmux what is actually true for this pane. This is the verification
	// the plan requires: the environment is a cue, tmux is the authority, and a
	// mismatch means the process is not where its environment says it is.
	live, err := inspectPane(pane)
	if err != nil {
		return Context{}, fmt.Errorf("%w: %v", ErrContextMismatch, err)
	}
	if live.paneID != pane {
		return Context{}, fmt.Errorf("%w: pane %s resolved to %s", ErrContextMismatch, pane, live.paneID)
	}
	if claimedSession != "" && live.session != claimedSession {
		return Context{}, fmt.Errorf("%w: environment claims session %q, pane is in %q",
			ErrContextMismatch, claimedSession, live.session)
	}
	if claimedServer != "" && strconv.Itoa(live.serverPID) != claimedServer {
		// The tmux server was replaced under this shell. Records namespaced to
		// the old server must not be joined by new ones claiming to be its
		// continuation.
		return Context{}, fmt.Errorf("%w: environment claims tmux server %s, live server is %d",
			ErrContextMismatch, claimedServer, live.serverPID)
	}
	if live.serverPID <= 0 {
		return Context{}, fmt.Errorf("%w: tmux reported no server pid", ErrContextMismatch)
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		return Context{}, fmt.Errorf("%w: host is unknown", ErrContextMismatch)
	}
	if claimed := os.Getenv(shellstate.HostEnv); claimed != "" && claimed != host {
		return Context{}, fmt.Errorf("%w: environment claims host %q, running on %q",
			ErrContextMismatch, claimed, host)
	}

	gen, err := providerGeneration(live.panePID)
	if err != nil {
		return Context{}, fmt.Errorf("%w: %v", ErrContextMismatch, err)
	}

	return Context{
		Managed:           true,
		Session:           live.session,
		Namespace:         namespaceOr(tmuxenv.Namespace()),
		Bin:               os.Getenv(shellstate.BinEnv),
		Host:              host,
		ServerIncarnation: "pid=" + strconv.Itoa(live.serverPID),
		PaneID:            pane,
		ProcessGeneration: gen,
		salt:              loadSalt(stateDir),
	}, nil
}

func namespaceOr(ns string) string {
	if ns != "" {
		return ns
	}
	return os.Getenv(shellstate.NamespaceEnv)
}

type paneInfo struct {
	paneID    string
	session   string
	panePID   int
	serverPID int
}

const paneFormat = "#{pane_id}\x1f#{session_name}\x1f#{pane_pid}\x1f#{pid}"

func inspectPane(pane string) (paneInfo, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, paneFormat).Output()
	if err != nil {
		return paneInfo{}, fmt.Errorf("tmux could not describe pane %s: %w", pane, err)
	}
	fields := strings.Split(strings.TrimRight(string(out), "\n"), "\x1f")
	if len(fields) < 4 {
		return paneInfo{}, fmt.Errorf("tmux returned %d fields for pane %s", len(fields), pane)
	}
	info := paneInfo{paneID: fields[0], session: fields[1]}
	if pid, err := strconv.Atoi(strings.TrimSpace(fields[2])); err == nil {
		info.panePID = pid
	}
	if pid, ok := tmuxserver.ParsePID(fields[3]); ok {
		info.serverPID = pid
	}
	return info, nil
}

// maxAncestry bounds the parent walk. A hook is a handful of levels below the
// provider; anything deeper is a loop or an unexpected shape, and walking
// forever in a hook that must return quickly is worse than not identifying the
// generation.
const maxAncestry = 24

// providerGeneration identifies the provider process this hook belongs to.
//
// The provider is the ancestor whose own parent is the pane's root process:
// walking up from the hook, the last process before the pane's shell is the
// thing the user actually started. That is the granularity a "generation" needs
// — relaunching the agent in the same pane must produce a new one, while the
// pane and its shell stay the same.
//
// When the walk cannot find the pane's shell (an unusual process tree, a hook
// that daemonised, ps unavailable) the pane's own root process is used instead.
// That is weaker — it will not rotate on an agent relaunch — but it is stable
// and truthful, and the resolver's other identity checks still apply.
func providerGeneration(panePID int) (string, error) {
	if panePID <= 0 {
		return "", errors.New("pane has no root process")
	}
	pid := os.Getpid()
	for i := 0; i < maxAncestry; i++ {
		parent, err := parentPID(pid)
		if err != nil {
			break
		}
		if parent == panePID {
			return generationString(pid), nil
		}
		if parent <= 1 {
			break
		}
		pid = parent
	}
	return generationString(panePID), nil
}

func generationString(pid int) string {
	// The start time disambiguates a recycled PID. Without it, a pane that
	// outlives enough process churn could see a new provider inherit a previous
	// one's generation and, with it, its authority.
	if start := processStart(pid); start != "" {
		return "pid=" + strconv.Itoa(pid) + ",start=" + start
	}
	return "pid=" + strconv.Itoa(pid)
}

func parentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func processStart(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	// Collapsed to a single token so it can never introduce whitespace into an
	// identity field, which validation would reject.
	return strings.Join(strings.Fields(string(out)), "-")
}

// saltFile is the host-local salt for session fingerprints.
const saltFile = "agent-lifecycle-salt"

// loadSalt reads, or creates, the host salt.
//
// A missing or unreadable salt degrades to an empty one rather than failing.
// The fingerprint is then an unsalted digest of the session id: still opaque,
// still not the session id, but no longer resistant to someone testing a
// guessed id against the log. That is a worse privacy property and a working
// hook, which is the correct trade for a diagnostic record.
func loadSalt(stateDir string) []byte {
	if stateDir == "" {
		return nil
	}
	path := filepath.Join(stateDir, saltFile)
	if data, err := os.ReadFile(path); err == nil && len(data) >= 16 {
		return data
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil
	}
	// 0600 rather than the state tree's usual 0644: this is the one file here
	// whose value is only useful to someone who should not have it.
	if err := os.WriteFile(path, salt, 0o600); err != nil {
		return nil
	}
	return salt
}
