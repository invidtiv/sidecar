package agentactivity

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const foregroundIdentityTTL = 2 * time.Second

type foregroundIdentityEntry struct {
	group      int
	identity   string
	hinted     string
	hintsRead  bool
	resolvedAt time.Time
}

// foregroundProcess is the platform-neutral slice of process-table state the
// identity resolver works from.
//
// It started as pid/ppid/argv[0] — just enough to decide which members still
// belong to a foreground job. Process-tree scoring needs more, and each field
// below is here because one rule cannot be evaluated without it:
//
//   - PID and ParentPID answer "is this member still the shell's job". A daemon
//     may double-fork, be adopted by init, and retain its old process group;
//     Git's fsmonitor daemon does exactly that on macOS. See foregroundJobMembers.
//   - PID also identifies the process group leader, which scoring prefers over
//     every other member.
//   - Comm is the kernel's short process name. processPriority compares the
//     resolved name against it: a name that differs from comm was genuinely
//     unwrapped and scores higher than one that merely repeats it.
//   - Argv0 is argv[0] as executed, which is not the executable path — a
//     launcher running node with `exec -a claude` reports claude here.
//   - Argv is the whole command line, which is the only place the agent's name
//     appears when it is installed as a `#!/usr/bin/env node` shim.
//
// The environment is deliberately absent. It is read through a separate seam,
// lazily, and only on the path that is allowed to consider a hint — see
// platformProcessAgentHint and ResolveForegroundAgent.
type foregroundProcess struct {
	PID       int
	ParentPID int
	Comm      string
	Argv0     string
	Argv      []string
}

// foregroundJobMembers filters a raw process-group scan down to the members that
// still belong to the pane shell's job, leader first.
//
// The init-adopted filter is load-bearing and predates process-tree scoring: a
// double-forked daemon that keeps the pane's process group is not work the shell
// is waiting on, and counting it made ForegroundShellReady refuse to launch into
// a perfectly idle pane whenever Git's fsmonitor was running. The filter is
// applied here, once, to the richer list, so every consumer — the shell gate,
// evidence resolution and hint resolution alike — sees the same job.
//
// The group leader is exempt because it is authoritative even when its parent
// has exited; putting it first is what lets the shell gate's "exactly one
// member" check and the scoring ladder read the same slice.
func foregroundJobMembers(group int, processes []foregroundProcess) []foregroundProcess {
	var leader []foregroundProcess
	var members []foregroundProcess
	for _, process := range processes {
		if strings.TrimSpace(process.Argv0) == "" {
			continue
		}
		if process.PID != group && process.ParentPID == 1 {
			continue
		}
		if process.PID == group {
			leader = append(leader, process)
		} else {
			members = append(members, process)
		}
	}
	return append(leader, members...)
}

var foregroundIdentities = struct {
	sync.Mutex
	entries map[int]foregroundIdentityEntry
}{entries: make(map[int]foregroundIdentityEntry)}

// AgentHintEnv is the launch-time process-identity hint: a provider name a
// wrapper command may publish into its own environment so Sidecar can name an
// agent it cannot see.
//
// Herdr's equivalent is `HERDR_AGENT` (src/platform/mod.rs:346 at d08e4468,
// `parse_agent_env_hint`). It exists for the pane Sidecar cannot resolve from
// the process table at all: a sandbox or container wrapper whose foreground
// process is the sandbox, with the agent running out of reach inside it.
//
// It is a hint and never a claim. See ResolveForegroundAgent for the seam that
// keeps it away from lifecycle authority, and why that seam must not be
// collapsed.
const AgentHintEnv = "SIDECAR_AGENT"

// parseAgentEnvHint reads AgentHintEnv out of a raw NUL-separated environment
// block. Upstream: `parse_agent_env_hint`, src/platform/mod.rs:346 at d08e4468.
//
// The first matching record wins, including when its value is not a known
// agent — upstream returns on the first match rather than continuing to search,
// and a second SIDECAR_AGENT further down the block is not more trustworthy than
// the first. An unknown value is no hint, not an error: the value is validated
// through identifyProcessName, the one alias table, so a hint can only ever name
// a family Sidecar already knows.
func parseAgentEnvHint(environ []byte) string {
	prefix := []byte(AgentHintEnv + "=")
	for _, record := range bytes.Split(environ, []byte{0}) {
		value, ok := bytes.CutPrefix(record, prefix)
		if !ok {
			continue
		}
		return validatedAgentHint(string(value))
	}
	return ""
}

// validatedAgentHint turns a raw hint value into a family id, or "".
//
// "shell" is rejected along with everything else unknown, because identifyAgentName
// drops that bucket: `SIDECAR_AGENT=bash` names no provider.
func validatedAgentHint(value string) string {
	return identifyAgentName(strings.TrimSpace(value))
}

// readProcessAgentHint is the seam through which AgentHintEnv is read, one
// process at a time.
//
// It is a variable so the precedence ladder can be driven from a table test
// without a live process table, and so a test can assert the *negative* that
// matters most: that the evidence-only resolver never reads an environment at
// all. Production always holds the platform implementation.
var readProcessAgentHint = platformProcessAgentHint

// ResolveForegroundAgent names the agent running in a pane, for detection and
// display. It is the hint-aware resolver.
//
// # Why this is a different function from ResolveForegroundProcess
//
// This one may consider AgentHintEnv. That one may not, and the split is the
// whole point rather than an accident of history.
//
// ResolveForegroundProcess feeds lifecycleenv.OccupantKind, which feeds
// VerifyReportedKind, which *refuses* a hook report whose claimed kind disagrees
// with the pane's occupant. AgentHintEnv is an environment variable: anything
// running in the session can set it. If a hint could reach OccupantKind, then
// exporting `SIDECAR_AGENT=codex` in a Claude pane would make Sidecar reject
// Claude's own reports — a writable variable would have acquired the power to
// switch off a lifecycle lane. So the hint stops here, on the display side,
// where being wrong costs a wrong badge and nothing else.
//
// Do not "simplify" these two back into one function. If a caller needs both
// answers it should ask for both.
//
// # Precedence
//
// Upstream's, from `probe_foreground_process_from_jobs` (src/pane.rs:608 at
// d08e4468):
//
//  1. a hint on the process group leader,
//  2. identification of the leader alone,
//  3. a hint on any non-leader member of the job,
//  4. scored identification across the whole job.
//
// A hint on the leader beats identification precisely because the case it exists
// for is the one where identification is about to answer "the sandbox". A hint
// deeper in the job ranks below the leader's own identity, because there the
// leader is real evidence and the member's hint may be inherited from an
// unrelated ancestor.
//
// Steps 2 and 4 are both identifyAgentInJob, which already prefers the leader;
// they are written out separately only because step 3 sits between them.
func ResolveForegroundAgent(panePID int) string {
	entry := resolveForegroundIdentity(panePID, true)
	if entry.hinted == "shell" {
		return ""
	}
	return entry.hinted
}

// ResolveForegroundProcess identifies the known program in the pane's actual
// foreground process group, including "shell" when the group belongs to an
// interactive shell. Unlike pane_current_command, this is process ownership
// evidence and is therefore safe for agent-control's shell-ready gate and for
// lifecycleenv's occupant check.
//
// Process evidence only. It never reads an environment; see ResolveForegroundAgent.
func ResolveForegroundProcess(panePID int) string {
	return resolveForegroundIdentity(panePID, false).identity
}

// resolveForegroundIdentity scans the pane's foreground job once and answers
// both questions from it, caching by pane PID and foreground group so
// active-agent polling does not scan the process table on every frame while a
// new foreground job still invalidates the cache immediately.
//
// withHint controls whether the environment is read at all. The evidence answer
// is always computed and always cached, so an evidence-only caller hits a cache
// entry a hinted caller warmed; the reverse misses once, which is the price of
// never paying the hint's cost on a path that is not allowed to use it.
func resolveForegroundIdentity(panePID int, withHint bool) foregroundIdentityEntry {
	if panePID <= 0 {
		return foregroundIdentityEntry{}
	}
	group := platformForegroundProcessGroup(panePID)
	if group <= 0 {
		return foregroundIdentityEntry{}
	}
	now := time.Now()
	foregroundIdentities.Lock()
	entry, ok := foregroundIdentities.entries[panePID]
	foregroundIdentities.Unlock()
	if ok && entry.group == group && now.Sub(entry.resolvedAt) < foregroundIdentityTTL &&
		(!withHint || entry.hintsRead) {
		return entry
	}

	processes := platformForegroundProcesses(group)
	entry = foregroundIdentityEntry{
		group:      group,
		identity:   foregroundEvidenceIdentity(group, processes),
		hintsRead:  withHint,
		resolvedAt: now,
	}
	if withHint {
		entry.hinted = foregroundHintedIdentity(group, processes)
		if entry.hinted == "" {
			// No hint anywhere and nothing the scoring pass could name: fall
			// back to the evidence answer so a hinted caller is never told less
			// than an unhinted one. In practice these agree; they differ only
			// for "shell", which the evidence answer carries and the hinted
			// path has no notion of.
			entry.hinted = entry.identity
		}
	}

	foregroundIdentities.Lock()
	if len(foregroundIdentities.entries) > 256 {
		for pid, cached := range foregroundIdentities.entries {
			if now.Sub(cached.resolvedAt) >= foregroundIdentityTTL {
				delete(foregroundIdentities.entries, pid)
			}
		}
	}
	foregroundIdentities.entries[panePID] = entry
	foregroundIdentities.Unlock()
	return entry
}

// foregroundEvidenceIdentity is the process-only answer: scored identification
// across the job, falling back to "shell" when the job names no agent but does
// contain an interactive shell.
//
// The shell fallback is not part of upstream's scoring — Herdr has no "shell"
// answer at all — and it is kept separate from it here for the same reason
// isGenericRuntimeOrShell is kept separate from identifyProcessName's shell
// bucket: "an interactive shell is sitting in this pane" is a launch-readiness
// fact, not an identity.
func foregroundEvidenceIdentity(group int, processes []foregroundProcess) string {
	if agent, _ := identifyAgentInJob(group, processes); agent != "" {
		return agent
	}
	for _, process := range processes {
		if identifyArgv0(process.Argv0) == "shell" {
			return "shell"
		}
	}
	return ""
}

// foregroundHintedIdentity applies the precedence documented on
// ResolveForegroundAgent. It reads an environment only where the precedence
// requires it: the leader's hint is read before the leader is identified, so
// that read cannot be skipped, but a member's hint is read only after
// identification of the leader has already failed.
func foregroundHintedIdentity(group int, processes []foregroundProcess) string {
	for _, process := range processes {
		if process.PID != group {
			continue
		}
		if hint := readProcessAgentHint(process.PID); hint != "" {
			return hint
		}
		if agent, _ := identifyAgentInJob(group, []foregroundProcess{process}); agent != "" {
			return agent
		}
		break
	}
	for _, process := range processes {
		if process.PID == group {
			continue
		}
		if hint := readProcessAgentHint(process.PID); hint != "" {
			return hint
		}
	}
	agent, _ := identifyAgentInJob(group, processes)
	return agent
}

// ForegroundShellReady is the strict launch gate for a managed tmux pane. It
// accepts only the pane shell's own process group with exactly one member, and
// that member must resolve to a known interactive shell. Unknown helpers are
// busy, not ignorable.
//
// It reads argv[0] and nothing else on purpose. Process-tree scoring exists to
// look *past* a runtime to the program behind it, which is the opposite of what
// this question wants: a pane running `node` is occupied whether or not the node
// turns out to be an agent, and a hint is irrelevant because nobody launches
// into a hint.
func ForegroundShellReady(panePID int, currentCommand string) bool {
	if panePID <= 0 || platformForegroundProcessGroup(panePID) != panePID {
		return false
	}
	processes := platformForegroundProcesses(panePID)
	if len(processes) != 1 || identifyArgv0(processes[0].Argv0) != "shell" {
		return false
	}
	return identifyProcessName(strings.ToLower(strings.TrimSpace(currentCommand))) == "shell"
}

func identifyArgv0(argv0 string) string {
	argv0 = strings.TrimSpace(argv0)
	if argv0 == "" {
		return ""
	}
	resolved := argv0
	if target, err := filepath.EvalSymlinks(argv0); err == nil {
		resolved = target
	}
	name := strings.TrimPrefix(filepath.Base(resolved), "-")
	return identifyProcessName(name)
}

// HasProcessIdentity reports whether this platform can disambiguate a pane's
// foreground job by argv[0].
//
// It exists so the answer is read from the build rather than restated as a
// GOOS list somewhere else — the host protocol's hello carries this bit, and a
// second copy of "which platforms are implemented" would be wrong the moment a
// third one is added.
func HasProcessIdentity() bool { return processIdentitySupported }
