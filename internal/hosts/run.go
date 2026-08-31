package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/config"
)

// This file is the viewer's request seam: how a mutation reaches a host.
//
// It is deliberately not a second protocol. The serve stream stays
// one-directional and read-only, and a mutation is instead one `sidecar <verb>
// --json` invocation run over the SAME multiplexed ssh master the stream and
// the pane channels already ride. Two consequences are the whole reason for the
// shape: hostproto never grows a request direction, and every mutation the
// viewer can perform is by construction a documented CLI verb an agent can run
// over plain ssh. The seam here only has to start that invocation, bound it,
// read its JSON, and name its failures.

// Run bounds. A remote pipe is not a trusted source: a host whose login profile
// loops, or whose verb dumps a repository, must not be able to grow the
// viewer's heap. These are the same reasoning as hostproto.MaxLineBytes, one
// layer out.
const (
	// MaxRunOutputBytes caps a single invocation's stdout. Result objects are
	// hundreds of bytes; a megabyte is far past any of them and far short of
	// hurting.
	MaxRunOutputBytes = 1 << 20
	// MaxRunStderrBytes caps stderr, which is only ever read to explain a
	// failure. It matches the serve channel's stderr bound.
	MaxRunStderrBytes = 8 << 10
)

// DefaultRunTimeout bounds an invocation whose caller supplied no deadline.
//
// It exists because of td-052329: remote work that outlives the interaction
// that asked for it turns into a Bubble Tea command that never returns and, at
// shutdown, into a quit that visibly hangs. A caller running a verb that is
// legitimately slow (a worktree with a setup hook) should pass its own
// deadline; an unbounded call is never the right answer.
const DefaultRunTimeout = 30 * time.Second

// runWaitDelay bounds how long Wait may block after the process is gone. Same
// value and same reason as the serve channel's: OpenSSH multiplexing can leave
// a descendant holding the pipe, and os/exec's zero default waits for it.
const runWaitDelay = 250 * time.Millisecond

// Failure names why a remote invocation did not produce a result.
//
// The values are what a viewer switches on. They exist for the same reason
// hostproto's error codes do: a caller that has to string-match a message to
// decide whether to offer "retry" or "update Sidecar there" will get it wrong
// the first time the wording changes.
type Failure string

const (
	// FailUnavailable means the call was never attempted: the host is not
	// connected, or is not registered at all. Nothing ran on the remote side.
	FailUnavailable Failure = "unavailable"
	// FailTransport means ssh itself failed — it could not start, could not
	// connect, or the channel died before the command reported a status.
	FailTransport Failure = "transport"
	// FailNoSidecar means ssh ran but no sidecar binary did.
	FailNoSidecar Failure = "no-sidecar"
	// FailTimeout means the invocation exceeded its deadline.
	FailTimeout Failure = "timeout"
	// FailCanceled means the caller cancelled it — a closed modal, a quit.
	FailCanceled Failure = "canceled"
	// FailRefused is exit 1: the remote sidecar ran, understood the verb, and
	// declined. The message is the remote's own refusal and is worth showing
	// verbatim, because it was written for exactly this reader.
	FailRefused Failure = "refused"
	// FailUnsupported is exit 2: the command as written is not usable — an
	// unknown flag, a missing required flag, a flag combination the verb does
	// not offer. The viewer builds the SHAPE of every argument list itself, so
	// a host that cannot parse the shape means the two Sidecars disagree about
	// the verb, which is a version-skew story with its own fix.
	//
	// It deliberately does not cover a rejected value: the viewer does not
	// invent branch names or display names, the user supplies them, and telling
	// that user to update a binary because a name is already taken is a wrong
	// answer to a question they can fix themselves. That is FailRejected.
	FailUnsupported Failure = "unsupported"
	// FailRejected is exit 5: the remote sidecar parsed the command and refused
	// a value inside it — a display name already in use, a branch that exists,
	// a base ref that machine does not have. The remote's own sentence is the
	// whole answer; nothing here needs updating.
	FailRejected Failure = "rejected"
	// FailNoTarget is exit 3 from a target-taking verb: the host does not own
	// the workspace the action addressed. A row that has gone away on the other
	// machine, or one this Sidecar can see but that one does not manage.
	FailNoTarget Failure = "no-target"
	// FailExit is any other non-zero status.
	FailExit Failure = "exit"
	// FailNotResult means the command exited 0 but stdout was not the result.
	// Overwhelmingly this is a login profile writing to stdout.
	FailNotResult Failure = "not-result"
)

// RunError is a failed remote invocation, with everything a row needs to say
// what happened and what to do about it.
type RunError struct {
	// Failure is the classification. Switch on this, not on the message.
	Failure Failure
	// HostID is the host the invocation was addressed to.
	HostID string
	// Args is the sidecar verb and its arguments, as given to RunSidecar.
	Args []string
	// ExitCode is the remote status, or -1 when the command never exited on
	// its own (never started, or killed by cancellation).
	ExitCode int
	// Stderr is the remote's stderr, bounded and trimmed.
	Stderr string
	// Detail is the sentence to show. It prefers the remote's own words.
	Detail string

	err error
}

func (e *RunError) Error() string {
	verb := strings.Join(e.Args, " ")
	if verb == "" {
		verb = "sidecar"
	} else {
		verb = "sidecar " + verb
	}
	detail := e.Detail
	if detail == "" {
		detail = string(e.Failure)
	}
	return fmt.Sprintf("host %s: %s: %s", e.HostID, verb, detail)
}

func (e *RunError) Unwrap() error { return e.err }

// Fix names what to do about a failure, in the imperative, the way State.Fix
// does. Empty when there is nothing useful to say.
func (e *RunError) Fix() string {
	switch e.Failure {
	case FailUnavailable:
		return "wait for the host to reconnect, or check `ssh <target>` works from here"
	case FailTransport:
		return "check the machine is on and `ssh <target>` works from here"
	case FailNoSidecar:
		return "install Sidecar on that machine, or set its `binary` path"
	case FailTimeout:
		return "the host did not answer in time; try again once it is responsive"
	case FailUnsupported:
		return "update Sidecar on whichever machine is older: this one asked for something that one does not accept"
	case FailNoTarget:
		return "refresh that host's workspaces: the session may have been renamed or removed on that machine"
	case FailNotResult:
		return "that machine's login shell prints to stdout; send it to stderr or guard it with a non-interactive check"
	default:
		return ""
	}
}

// RunFailure reports the classification of any error returned by RunSidecar,
// or "" for an error from somewhere else. It saves every call site an
// errors.As dance.
func RunFailure(err error) Failure {
	var runErr *RunError
	if errors.As(err, &runErr) {
		return runErr.Failure
	}
	return ""
}

// Output is what one remote invocation produced.
type Output struct {
	Stdout []byte
	Stderr []byte
	// ExitCode is the remote status, or -1 when the process did not exit on
	// its own — never started, or killed because the context ended.
	ExitCode int
	// Truncated marks output that hit the byte caps above.
	Truncated bool
}

// Invoker runs one prepared invocation and reports what it wrote and how it
// exited. It returns an error only when there is no status to report: a
// non-zero exit is a result, not a failure of the invoker.
//
// Injectable for the same reason Dialer is: the deadline discipline, the exit
// classification and the JSON decoding are all things that must be tested, and
// none of them should need ssh, a network, or a second machine to test.
type Invoker func(ctx context.Context, cmd *exec.Cmd) (Output, error)

// SidecarCommand builds the one-shot invocation of a sidecar verb on the host.
//
// It rides the same multiplexed master as the serve stream and the pane
// channels — same control directory, same SSHArgs — so a mutation costs a
// round trip rather than a connection. Exposed separately from RunSidecar
// because "what command did it actually run" is the first question any remote
// failure raises, and answering it should not require running anything.
func (c *Client) SidecarCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	transport, err := NewTransport(runHost(c.host), c.controlDir)
	if err != nil {
		return nil, err
	}
	return transport.Command(ctx, transport.SidecarCommand(args...)), nil
}

// runHost returns the registration to use for a mutation, carrying
// SIDECAR_ISOLATED_STATE when this process is itself isolated.
//
// This is a hard requirement rather than a nicety. A proof run reaches a real
// machine over a real ssh connection, and the remote sidecar has no other way
// to learn that the process asking it to create a shell is a test. With the
// variable set, the remote's own config.CheckStateIsolation refuses unless the
// host registration also points it at a temp state tree — so the failure mode
// of a misconfigured proof is a refusal, not a write into someone's live
// shells.json (td-8d18de).
//
// That check runs on the host BEFORE the verb's handler, not on its first
// write: internal/cli marks its mutating verbs and dispatch refuses them
// (Command.Mutates). It used to run only in cmd/sidecar/main.go, which a CLI
// verb never reaches, so what actually failed closed was the per-write
// assertion — after `tmux new-session`, after `git worktree add`. The sentence
// above was true about the outcome and wrong about the moment.
//
// An explicit host Env entry wins: a registration that deliberately sets the
// variable is a statement about that host, and this must not overwrite it.
func runHost(host Host) Host {
	if !config.IsolationAsserted() {
		return host
	}
	prefix := config.IsolationEnv + "="
	for _, entry := range host.Env {
		if strings.HasPrefix(entry, prefix) {
			return host
		}
	}
	host.Env = append(append([]string(nil), host.Env...), prefix+"1")
	return host
}

// OneShotClient builds a client for a single CLI invocation of RunSidecar.
//
// The health gate in RunSidecar exists to protect the TUI: attempting a
// mutation on a host nothing has dialled means waiting out an ssh connect
// timeout inside whatever asked for it. A one-shot CLI process is the opposite
// situation. Nothing has dialled because nothing had the chance to, the user
// named the host explicitly on the command line, and waiting out a connect
// timeout is the correct behaviour rather than the hang. So this marks the
// client reachable and lets ssh itself decide, which turns "no stream is
// running" back into what it should be — an attempt that may fail with the
// transport's own reason, not a refusal before anything was tried.
//
// It does not start a stream, so it observes nothing and must not be used
// where a Registry would do.
func OneShotClient(host Host, opts ClientOptions) *Client {
	client := NewClient(host, opts)
	client.setHealth(StateOnline, "", 0)
	return client
}

// RunSidecar runs a sidecar verb on the host and decodes its --json result
// into out. Pass a nil out for a verb whose result does not matter.
//
// It returns a *RunError for every failure, so a caller can render an
// actionable message without parsing text. The health gate is first and is not
// an optimisation: attempting a mutation on a host that is not connected means
// waiting out an ssh connect timeout inside whatever asked for it, which is the
// hang hostControlSpawner returns nil to avoid.
func (c *Client) RunSidecar(ctx context.Context, args []string, out any) error {
	fail := func(failure Failure, exitCode int, stderr, detail string, err error) *RunError {
		return &RunError{
			Failure: failure, HostID: c.host.ID, Args: args,
			ExitCode: exitCode, Stderr: stderr, Detail: detail, err: err,
		}
	}
	if len(args) == 0 {
		return fail(FailUnsupported, -1, "", "no remote verb was given", nil)
	}
	if health := c.Health(); !health.State.Shows() {
		detail := fmt.Sprintf("%s is %s", c.host.ID, health.State)
		if line := firstRunLine(health.Detail); line != "" {
			detail += ": " + line
		}
		return fail(FailUnavailable, -1, "", detail, nil)
	}

	// A caller's deadline wins; an absent one is replaced rather than
	// respected. See DefaultRunTimeout.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultRunTimeout)
		defer cancel()
	}

	cmd, err := c.SidecarCommand(ctx, args...)
	if err != nil {
		return fail(FailTransport, -1, "", err.Error(), err)
	}
	invoke := c.invoke
	if invoke == nil {
		invoke = runCommand
	}
	output, runErr := invoke(ctx, cmd)
	stderr := boundedText(output.Stderr, MaxRunStderrBytes)

	if failure, detail := classifyRun(ctx, output, runErr, stderr); failure != "" {
		return fail(failure, output.ExitCode, stderr, detail, runErr)
	}
	if out == nil {
		return nil
	}
	if detail := decodeRemoteResult(output.Stdout, out); detail != "" {
		// Say so when the cap is a plausible cause, rather than blaming a
		// banner for output this end truncated.
		if output.Truncated {
			detail += fmt.Sprintf(" (stdout was capped at %d bytes)", MaxRunOutputBytes)
		}
		return fail(FailNotResult, output.ExitCode, stderr, detail, nil)
	}
	return nil
}

// classifyRun turns an exit status into a Failure and a sentence, or ("", "")
// for success.
//
// The order matters. A process that exited on its own is classified by its
// status even if the context expired a moment later, because the work was
// done; only a process with no status of its own is attributed to the deadline.
func classifyRun(ctx context.Context, output Output, err error, stderr string) (Failure, string) {
	detail := firstRunLine(stderr)

	if output.ExitCode < 0 {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return FailTimeout, "the host did not answer in time"
		case errors.Is(ctx.Err(), context.Canceled):
			return FailCanceled, "the request was cancelled"
		}
		if detail == "" {
			detail = errText(err)
		}
		if detail == "" {
			detail = "the ssh connection to the host failed"
		}
		return FailTransport, detail
	}

	// The remote sidecar's documented statuses (internal/cli/registry.go):
	// 0 success, 1 state or identity failure, 2 usage error, 3 the target names
	// nothing that project owns, 4 an instance declined, 5 a value in the
	// command was rejected. Everything outside that set came from the shell or
	// from ssh, not from sidecar.
	//
	// The 2/5 split is the one that carries information and the one this
	// changeset had wrong: 2 is "the two Sidecars disagree about the verb",
	// which a user fixes by updating a binary, and 5 is "that name is taken",
	// which they fix by typing a different name. Collapsing them told people to
	// upgrade in answer to an ordinary rename collision.
	switch output.ExitCode {
	case 0:
		return "", ""
	case 1:
		if isMissingSidecar(stderr) {
			return FailNoSidecar, missingSidecarDetail(detail)
		}
		if detail == "" {
			detail = "the host refused the operation without saying why"
		}
		return FailRefused, detail
	case 2:
		if detail == "" {
			detail = "the host did not accept the command"
		}
		return FailUnsupported, "the remote Sidecar did not accept this command: " + detail
	case 3:
		if detail == "" {
			detail = "the host does not own the workspace this action addressed"
		}
		return FailNoTarget, detail
	case 4:
		if detail == "" {
			detail = "the host declined the operation without saying why"
		}
		return FailRefused, detail
	case 5:
		if detail == "" {
			detail = "the host rejected that value without saying why"
		}
		return FailRejected, detail
	case 126, 127:
		return FailNoSidecar, missingSidecarDetail(detail)
	case 255:
		// ssh's own status. A remote sidecar never exits 255.
		if detail == "" {
			detail = "ssh could not run the command on the host"
		}
		return FailTransport, detail
	default:
		if isMissingSidecar(stderr) {
			return FailNoSidecar, missingSidecarDetail(detail)
		}
		if detail == "" {
			detail = fmt.Sprintf("the remote command exited %d", output.ExitCode)
		}
		return FailExit, detail
	}
}

func missingSidecarDetail(detail string) string {
	if detail == "" {
		return "no sidecar binary ran on the host"
	}
	return detail
}

// isMissingSidecar matches the shapes a shell uses to say the binary is not
// there. Same distinctions as classifyStreamFailure, and for the same observed
// reason: a non-login ssh shell reports a Homebrew-installed binary as missing.
//
// The thing reported missing must be sidecar itself. Stderr also carries the
// verb's children — a project setup hook failing with `pnpm: command not
// found` exits the verb 1, and reading that as an uninstalled Sidecar tells
// the user to install a binary that plainly just ran.
func isMissingSidecar(stderr string) bool {
	for _, line := range strings.Split(strings.ToLower(stderr), "\n") {
		line = strings.TrimSpace(line)
		var missing string
		switch {
		case strings.HasSuffix(line, ": command not found"),
			strings.HasSuffix(line, ": not found"),
			strings.HasSuffix(line, ": no such file or directory"):
			// bash/dash name the command before the phrase:
			// "bash: line 1: sidecar: command not found".
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				missing = strings.TrimSpace(parts[len(parts)-2])
			}
		case strings.Contains(line, "command not found: "),
			strings.Contains(line, "no such file or directory: "):
			// zsh names it after the phrase:
			// "zsh:1: command not found: sidecar".
			missing = strings.TrimSpace(line[strings.LastIndex(line, ": ")+2:])
		}
		if missing == "sidecar" || strings.HasSuffix(missing, "/sidecar") {
			return true
		}
	}
	return false
}

// ResultValidator is implemented by a result type that can recognise its own
// verb's result.
//
// It exists because JSON decoding cannot: Go ignores unknown fields and
// tolerates missing ones, so almost any object "decodes" into any struct and
// comes back all-zero with a nil error. Tolerating unknown fields is real
// forward compatibility and must survive — a host one version ahead adds
// fields, and that is not an error — so the answer is not DisallowUnknownFields
// but a per-type statement of which fields make the value this verb's answer.
//
// Implement it on any type passed as RunSidecar's out. Without it the decoder
// still refuses an all-zero value, which is the floor; with it, a result that
// happens to share one field name with a log line is refused too.
type ResultValidator interface {
	// ValidRemoteResult reports whether the decoded value carries the fields
	// that make it this verb's result.
	ValidRemoteResult() bool
}

// decodeRemoteResult decodes the host's --json result out of stdout, and
// returns "" on success or the sentence to show on failure.
//
// Stdout is not necessarily the result, and that is not an edge case: a login
// profile that prints a banner is the failure a first-time user of this feature
// actually hits. hostproto's decoder names that cause rather than surfacing a
// JSON syntax error, and this must say the same thing, because it is the same
// machine with the same profile and the user needs the same fix.
//
// Two rules make "find the result" mean the result rather than the first thing
// that parses:
//
//  1. Candidates are tried LAST first. The CLI writes its result last; a
//     profile, a wrapper, a version nag all write before the verb runs.
//  2. A candidate that decodes to the zero value is rejected, not returned.
//     Without this a profile emitting structured log lines — `{"level":"info"}`
//     — decoded into every result type with a nil error, and the surface
//     rendered a blank confirmation for an operation that then really ran on
//     the user's machine. A type implementing ResultValidator says more
//     precisely what "not the result" means for it.
//
// Each candidate decodes into a scratch value and is copied out only once
// accepted, so a rejected object cannot leave half a result behind.
func decodeRemoteResult(stdout []byte, out any) string {
	if len(bytes.TrimSpace(stdout)) == 0 {
		return "the host ran the command but wrote nothing to stdout, so there is no result to read"
	}
	target := reflect.ValueOf(out)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return "the remote result could not be read: no destination was given for it"
	}
	elem := target.Type().Elem()
	// A caller passing &struct{}{} is asking only "did it exit 0 and write
	// JSON"; the zero value is the only value that type can hold, so the
	// emptiness rule has nothing to say about it.
	demandNonZero := !isEmptyResultType(elem)

	offsets := jsonStarts(stdout)
	var shapeDetail string
	sawZeroResult := false
	for i := len(offsets) - 1; i >= 0; i-- {
		decoder := json.NewDecoder(bytes.NewReader(stdout[offsets[i]:]))
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			continue
		}
		candidate := reflect.New(elem)
		if err := json.Unmarshal(raw, candidate.Interface()); err != nil {
			if shapeDetail == "" {
				shapeDetail = "the host returned JSON that is not the expected result: " + err.Error()
			}
			continue
		}
		if demandNonZero && !resultIsPresent(candidate, elem) {
			sawZeroResult = true
			continue
		}
		target.Elem().Set(candidate.Elem())
		return ""
	}
	if sawZeroResult {
		return "the host wrote JSON to stdout, but none of it is this command's result " +
			"(a login profile or wrapper logging to stdout is the usual cause); the host wrote: " +
			firstRunLine(string(stdout))
	}
	if shapeDetail != "" {
		return shapeDetail
	}
	return "the remote output is not the expected result (a shell banner on stdout is the usual cause); the host wrote: " +
		firstRunLine(string(stdout))
}

// resultIsPresent reports whether a decoded candidate is actually this verb's
// result rather than an object that merely survived decoding.
func resultIsPresent(candidate reflect.Value, elem reflect.Type) bool {
	if validator, ok := candidate.Interface().(ResultValidator); ok {
		return validator.ValidRemoteResult()
	}
	return !reflect.DeepEqual(candidate.Interface(), reflect.New(elem).Interface())
}

// isEmptyResultType reports a result type with nothing to fill.
func isEmptyResultType(elem reflect.Type) bool {
	return elem.Kind() == reflect.Struct && elem.NumField() == 0
}

// jsonStarts lists the byte offsets where a JSON value might begin: the first
// non-space byte of any line that opens an object or an array. Bounded, so a
// host that writes a megabyte of braces cannot turn this into a quadratic
// parse.
//
// The bound keeps the LAST candidates. The CLI writes its result after
// anything a profile or wrapper logs, so when a host emits more JSON-looking
// lines than the cap it is the earliest that are disposable — capping from the
// front dropped the result behind a chatty structured-log profile, the exact
// false failure the last-first decode order exists to prevent.
func jsonStarts(data []byte) []int {
	const maxCandidates = 32
	var ring [maxCandidates]int
	total := 0
	for offset := 0; offset < len(data); {
		line := data[offset:]
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		trimmed := bytes.TrimLeft(line, " \t\r\v\f")
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			ring[total%maxCandidates] = offset + (len(line) - len(trimmed))
			total++
		}
		offset += len(line) + 1
	}
	count := min(total, maxCandidates)
	offsets := make([]int, 0, count)
	for i := total - count; i < total; i++ {
		offsets = append(offsets, ring[i%maxCandidates])
	}
	return offsets
}

// firstRunLine is the one line of a remote's output worth putting in a row: the
// first non-blank one, bounded so a host cannot write a paragraph into the UI,
// and stripped of control characters so a host cannot write escape sequences
// into the local terminal — this is host-controlled text on a display path.
func firstRunLine(text string) string {
	const maxDetail = 200
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(stripControls(line))
		if line == "" {
			continue
		}
		if len(line) > maxDetail {
			cut := maxDetail
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			return line[:cut] + "…"
		}
		return line
	}
	return ""
}

// stripControls drops C0 and C1 control characters (keeping tabs), which is
// what turns an ESC/OSC sequence from a compromised host into inert text.
func stripControls(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, text)
}

func boundedText(data []byte, limit int) string {
	if len(data) > limit {
		data = data[:limit]
	}
	return strings.TrimSpace(string(data))
}

// runCommand is the production Invoker: run the ssh child, capture both pipes
// under a cap, and never outlive the call.
func runCommand(_ context.Context, cmd *exec.Cmd) (Output, error) {
	stdout := &syncBuffer{limit: MaxRunOutputBytes}
	stderr := &syncBuffer{limit: MaxRunStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Without this, a ControlMaster descendant holding the pipe makes Wait
	// block long after the process is gone — the minute-long quit delay of
	// td-052329, on the mutation path this time.
	cmd.WaitDelay = runWaitDelay

	err := cmd.Run()

	output := Output{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		ExitCode:  -1,
		Truncated: stdout.Truncated() || stderr.Truncated(),
	}
	if cmd.ProcessState != nil {
		// -1 here when the process was signalled rather than exiting, which is
		// exactly the distinction classifyRun needs.
		output.ExitCode = cmd.ProcessState.ExitCode()
	}
	if output.ExitCode >= 0 {
		return output, nil
	}
	return output, err
}
