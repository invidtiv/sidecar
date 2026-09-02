//go:build darwin

package agentactivity

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func platformForegroundProcessGroup(panePID int) int {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", panePID)
	if err != nil || process == nil || process.Eproc.Tpgid <= 0 {
		return 0
	}
	return int(process.Eproc.Tpgid)
}

// platformForegroundProcesses collects the foreground job. The process table is
// walked exactly once, as it was before process-tree scoring landed; what
// changed is how much is kept per matching process, not how often the table is
// read. The per-process sysctl below is the same kern.procargs2 call that
// already ran for argv[0] — full argv comes out of the same buffer, so the
// richer answer costs no extra syscall.
func platformForegroundProcesses(group int) []foregroundProcess {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	matches := make([]foregroundProcess, 0, 2)
	for i := range processes {
		process := &processes[i]
		if int(process.Eproc.Pgid) != group {
			continue
		}
		pid := int(process.Proc.P_pid)
		argv := darwinProcessArgv(pid)
		if len(argv) == 0 || argv[0] == "" {
			continue
		}
		matches = append(matches, foregroundProcess{
			PID:       pid,
			ParentPID: int(process.Eproc.Ppid),
			Comm:      darwinComm(process.Proc.P_comm[:]),
			Argv0:     argv[0],
			Argv:      argv,
		})
	}
	return foregroundJobMembers(group, matches)
}

// platformProcessAgentHint reads AgentHintEnv from one process's environment.
//
// Upstream: `process_agent_hint`, src/platform/macos.rs:798 at d08e4468, which
// reads the environ section of the same kern.procargs2 buffer as argv.
//
// It re-reads that buffer rather than carrying the environment on
// foregroundProcess, and that is the deliberate cheap choice: the environment is
// consulted only by the hinted resolver, for at most the leader plus the job's
// members, and only when identification did not already answer. Storing it
// eagerly would make every ForegroundShellReady and every evidence-only resolve
// carry a process environment they are forbidden to look at.
//
// # It works here, but not for every process, and the exception is a trap
//
// macOS hands another process's environment to the same uid — but not when the
// target is a *restricted* executable. For those the kernel truncates the
// buffer after argv and reports success: no error, a well-formed layout, and
// parseDarwinProcessEnviron correctly reporting that there is nothing after the
// last argument. It is the same protection that stops DYLD_* being inherited
// into a protected binary, and every observation below fits that rule.
//
// Measured on Darwin 25.6.0, same uid, same session:
//
//   - our own pid: ~11KB, contains `PATH=`;
//   - an ordinary (locally built, unsigned) binary this process spawned, with
//     SIDECAR_AGENT=claude in its environment: ~11KB, and this function returns
//     "claude". So does the same binary as a live tmux pane's foreground
//     process, started with `tmux -e SIDECAR_AGENT=codex`: 11156 bytes, hint
//     read back through the resolver;
//   - a `/bin/sleep 120` child of this same process, spawned identically with
//     the same variable exported: 35 bytes. The entire buffer is argc, the exec
//     path and the two argv strings. No hint, no PATH. `/bin/sleep` is a
//     SIP-protected platform binary and that is the only difference.
//
// The consequence for the hint is a bound, not a hole: what publishes
// AgentHintEnv is the wrapper the pane is running, and a wrapper installed by
// the user — docker, colima, a nix or Homebrew shim — is readable. A wrapper
// that ships with the OS is not, which on macOS means `/usr/bin/sandbox-exec`,
// `/usr/bin/ssh` and the system shells cannot be hinted through.
//
// The trap is for proofs rather than for users, and it has already cost one
// investigation: a stand-in sandbox that execs `/bin/sleep`, or a hint published
// on a `/bin/sh` pane, reads back empty — which looks exactly like the hint
// being broken on macOS rather than like the one process shape where it is.
// Prove this with a binary you built (TestAnAgentHintCannotChangeTheOccupant in
// internal/agentlifecycle/lifecycleenv re-execs the test binary for this reason,
// and TestDarwinReadsAnotherProcessesHintUnlessTheBinaryIsRestricted below pins
// both directions as measured facts).
func platformProcessAgentHint(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return ""
	}
	return parseAgentEnvHint(parseDarwinProcessEnviron(data))
}

// darwinComm renders kinfo_proc's p_comm, the kernel's short process name. It is
// NUL-terminated and truncated to MAXCOMLEN, which is why processPriority
// compares it case-insensitively against a name rather than requiring equality
// with a path.
func darwinComm(comm []byte) string {
	if end := bytes.IndexByte(comm, 0); end >= 0 {
		comm = comm[:end]
	}
	return string(comm)
}

// darwinProcessArgv parses sysctl(KERN_PROCARGS2)'s native layout:
// argc, executable path, NUL padding, then argv strings, then the environment.
// Unlike ps command text, this preserves a path containing spaces and the
// argv[0] installed by exec -a.
func darwinProcessArgv(pid int) []string {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return parseDarwinProcessArgv(data)
}

// procargs2ArgvStart finds where argv begins: past the executable path and the
// NUL padding the kernel writes after it. Upstream: `procargs2_argv_start`,
// src/platform/macos.rs:807 at d08e4468.
//
// # Known limitation, inherited from upstream
//
// It skips every NUL after the exec path, and an empty argv[0] is a bare NUL,
// so a process whose argv[0] is empty has its terminator swallowed as padding.
// Both readers below then start one string late: parseDarwinProcessArgv's
// argc-bounded loop takes its last element from the environment block, and
// parseDarwinProcessEnviron skips the first environment record. A hint in that
// first record is missed, and the returned argv may end with a `KEY=value`
// string.
//
// There is no honest structural fix here: padding and an empty argv[0] are the
// same bytes, and a content rule ("that looks like KEY=value") would misfire on
// a real `env FOO=bar` argument. So it is documented and pinned rather than
// guessed at — TestDarwinAnEmptyArgv0IsIndistinguishableFromPadding records the
// bound. Nothing observed in the wild produces it: a process that rewrites its
// title blanks the slots it stops using (Pi 0.84.3 blanks argv[1], not
// argv[0]), and an exec with an empty argv[0] is not an install shape any agent
// uses.
func procargs2ArgvStart(rest []byte) int {
	execEnd := bytes.IndexByte(rest, 0)
	if execEnd < 0 {
		return -1
	}
	pos := execEnd
	for pos < len(rest) && rest[pos] == 0 {
		pos++
	}
	if pos >= len(rest) {
		return -1
	}
	return pos
}

// parseDarwinProcessArgv reads exactly argc strings from the argv section.
//
// Upstream: `procargs2_argv`, src/platform/macos.rs:825 at d08e4468. Bounding
// the read by argc is what keeps the environment out of argv — the two sections
// are adjacent and separated by nothing but the count, so a parser that read
// until it ran out would report every environment variable as an argument.
// Upstream has a regression test for exactly that
// (`procargs2_argv_excludes_environment_entries`) and so does this port.
func parseDarwinProcessArgv(data []byte) []string {
	if len(data) < 4 {
		return nil
	}
	argc := int(int32(binary.NativeEndian.Uint32(data[:4])))
	if argc < 1 {
		return nil
	}
	rest := data[4:]
	current := procargs2ArgvStart(rest)
	if current < 0 {
		return nil
	}
	argv := make([]string, 0, argc)
	for i := 0; i < argc; i++ {
		if current >= len(rest) {
			break
		}
		end := bytes.IndexByte(rest[current:], 0)
		if end < 0 {
			end = len(rest) - current
		}
		end += current
		if end == current {
			// An empty slot ends argv rather than voiding it, which is where
			// this port deliberately parts company with upstream's
			// `procargs2_argv` (it returns None here and loses the whole
			// vector). A process that rewrites its own title on macOS writes
			// into this same memory and blanks the slots it no longer uses:
			// Pi 0.84.3 is a live case, reporting argc=2 with argv[0]="pi" and
			// argv[1]="". Voiding the vector there threw away the one element
			// that names the program, and the pane went from identified to
			// unidentified — a regression against the argv[0]-only parser this
			// replaced, on the exact provider Slice 3 exists to reach.
			//
			// Keeping the prefix reads no further than the old parser did:
			// the loop is still bounded by argc and this break only ever
			// stops it sooner. That bound is what normally keeps the
			// environment out of argv; the one case where it does not is an
			// empty argv[0], which is procargs2ArgvStart's inherited
			// limitation and is described there.
			// TestDarwinArgvSurvivesAProcessTitleRewrite pins this direction.
			break
		}
		argv = append(argv, string(rest[current:end]))
		current = end + 1
	}
	if len(argv) == 0 {
		return nil
	}
	return argv
}

// parseDarwinProcessEnviron returns the environment block that follows argv.
//
// An empty result means the kernel sent no environment section, not that the
// parse failed — see platformProcessAgentHint for when that happens and why the
// two are worth telling apart.
//
// Upstream: `procargs2_env`, src/platform/macos.rs:858 at d08e4468. It skips
// exactly argc NUL-terminated strings from the argv start, which is what stops
// an argument that happens to look like `SIDECAR_AGENT=claude` from being read
// as environment — a wrapper command whose *arguments* name an agent is not the
// same statement as a wrapper that exported one. Upstream tests both directions
// and so does this port.
func parseDarwinProcessEnviron(data []byte) []byte {
	if len(data) < 4 {
		return nil
	}
	argc := int(int32(binary.NativeEndian.Uint32(data[:4])))
	if argc < 1 {
		return nil
	}
	rest := data[4:]
	current := procargs2ArgvStart(rest)
	if current < 0 {
		return nil
	}
	for i := 0; i < argc; i++ {
		if current >= len(rest) {
			return nil
		}
		end := bytes.IndexByte(rest[current:], 0)
		if end < 0 {
			return nil
		}
		current += end + 1
	}
	if current > len(rest) {
		return nil
	}
	return rest[current:]
}

const processIdentitySupported = true
