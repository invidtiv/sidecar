package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
	// FailUnsupported is exit 2: a usage or validation error. Distinct from
	// refused on purpose. The viewer builds every one of these argument lists
	// itself, so the remote rejecting one as unusable is not a decision about
	// the operation — it means the two Sidecars disagree about the verb, which
	// is a version-skew story with a different fix.
	FailUnsupported Failure = "unsupported"
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
	// 0 success, 1 state or identity failure, 2 usage or validation error.
	// Everything else came from the shell or from ssh, not from sidecar.
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
func isMissingSidecar(stderr string) bool {
	lowered := strings.ToLower(stderr)
	switch {
	case strings.Contains(lowered, "command not found"),
		strings.Contains(lowered, "sidecar: no such file"):
		return true
	case strings.Contains(lowered, "not found") && strings.Contains(lowered, "sidecar"):
		return true
	default:
		return false
	}
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
// Leading non-JSON lines are tolerated the way the protocol decoder tolerates
// blank ones — the result is found rather than demanded — but a stdout with no
// decodable value in it is reported honestly, including what was actually
// there, since the banner text is the evidence the user needs.
func decodeRemoteResult(stdout []byte, out any) string {
	if len(bytes.TrimSpace(stdout)) == 0 {
		return "the host ran the command but wrote nothing to stdout, so there is no result to read"
	}
	var shapeDetail string
	for _, offset := range jsonStarts(stdout) {
		decoder := json.NewDecoder(bytes.NewReader(stdout[offset:]))
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			continue
		}
		if err := json.Unmarshal(raw, out); err != nil {
			if shapeDetail == "" {
				shapeDetail = "the host returned JSON that is not the expected result: " + err.Error()
			}
			continue
		}
		return ""
	}
	if shapeDetail != "" {
		return shapeDetail
	}
	return "the remote output is not the expected result (a shell banner on stdout is the usual cause); the host wrote: " +
		firstRunLine(string(stdout))
}

// jsonStarts lists the byte offsets where a JSON value might begin: the first
// non-space byte of any line that opens an object or an array. Bounded, so a
// host that writes a megabyte of braces cannot turn this into a quadratic
// parse.
func jsonStarts(data []byte) []int {
	const maxCandidates = 32
	var offsets []int
	for offset := 0; offset < len(data) && len(offsets) < maxCandidates; {
		line := data[offset:]
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		trimmed := bytes.TrimLeft(line, " \t\r\v\f")
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			offsets = append(offsets, offset+(len(line)-len(trimmed)))
		}
		offset += len(line) + 1
	}
	return offsets
}

// firstRunLine is the one line of a remote's output worth putting in a row: the
// first non-blank one, bounded so a host cannot write a paragraph into the UI.
func firstRunLine(text string) string {
	const maxDetail = 200
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > maxDetail {
			return line[:maxDetail] + "…"
		}
		return line
	}
	return ""
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
