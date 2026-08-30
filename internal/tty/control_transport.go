package tty

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type controlChannel interface {
	Send(command string, callback func(controlResponse)) error
	// SendBatch writes an arbitrary command group atomically. Input uses this
	// so a fast key batch, an escape followed by a paste, and a mouse click's
	// press/release cannot be interleaved with another control command.
	SendBatch(commands []string, callbacks []func(controlResponse)) error
	// SendPair writes two commands in a single write so tmux reads and queues
	// them together and executes them back to back. tmux emits one response
	// block per command line, so two FIFO callbacks are registered; a parse
	// error in one line still produces that line's own error block, which keeps
	// the FIFO 1:1. This exists so a seed transaction's metadata and its
	// rendered capture describe the same moment.
	SendPair(first, second string, firstCallback, secondCallback func(controlResponse)) error
	// SendTriple has the same atomic-write and FIFO-response contract as
	// SendPair for transactions that require three distinct tmux responses.
	SendTriple(first, second, third string, firstCallback, secondCallback, thirdCallback func(controlResponse)) error
	Events() <-chan controlEvent
	Done() <-chan error
	Close() error
}

type controlChannelFactory func(session string) (controlChannel, error)

type processControlChannel struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan controlEvent
	done   chan error
	dead   chan struct{}
	ready  chan struct{}
	// waited closes once cmd.Wait has returned, which is also when stderr has
	// finished being copied into diagnostics.
	waited chan struct{}
	// diagnostics is whatever the command wrote to stderr, bounded.
	diagnostics *boundedTail

	parser controlParser

	writeMu sync.Mutex
	mu      sync.Mutex
	pending []func(controlResponse)
	readyOK bool

	closeOnce  sync.Once
	finishOnce sync.Once
}

func newProcessControlChannel(session string) (controlChannel, error) {
	return newProcessControlChannelCommand(session, exec.Command(
		"tmux", "-C", "attach-session", "-f", "ignore-size", "-t", session,
	))
}

// newProcessControlChannelForSocket attaches to an explicitly named tmux socket.
// It exists for tests: TMUX is scrubbed from the child environment so a suite
// running inside tmux can never resolve a target against the developer's live
// default server.
func newProcessControlChannelForSocket(socket, session string) (controlChannel, error) {
	cmd := exec.Command( //nolint:gosec
		"tmux", "-S", socket, "-C", "attach-session", "-f", "ignore-size", "-t", session,
	)
	cmd.Env = append(os.Environ(), "TMUX=")
	return newProcessControlChannelCommand(session, cmd)
}

func newProcessControlChannelCommand(session string, cmd *exec.Cmd) (controlChannel, error) {
	if session == "" {
		return nil, fmt.Errorf("tmux control: empty session")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux control stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux control stdin: %w", err)
	}
	channel := &processControlChannel{
		cmd:         cmd,
		stdin:       stdin,
		events:      make(chan controlEvent, 128),
		done:        make(chan error, 1),
		dead:        make(chan struct{}),
		ready:       make(chan struct{}),
		waited:      make(chan struct{}),
		diagnostics: &boundedTail{limit: maxControlStderrBytes},
	}
	// stderr is recorded rather than discarded, because it is the only place
	// the reason for a failed attach exists. `attach-session -t gone` writes
	// "can't find session: gone" here and exits 1; without this the caller sees
	// "exit status 1" and cannot tell a dead session from a dead link — which
	// is precisely the distinction a remote pane has nothing else to make. See
	// Model.handleControlDelivery's fallback case.
	//
	// Assigning a plain io.Writer rather than taking StderrPipe is deliberate:
	// cmd.Wait then copies stderr itself and does not return until the copy is
	// complete, so the text is guaranteed present once waited closes. A pipe
	// plus a copier goroutine races Wait's closing of that pipe, which is the
	// documented hazard in os/exec.
	cmd.Stderr = channel.diagnostics
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tmux control: %w", err)
	}
	go channel.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		close(channel.waited)
		channel.finish(err)
	}()

	select {
	case <-channel.ready:
		return channel, nil
	case err := <-channel.done:
		// The channel died before it answered, so the process is on its way
		// out. Give Wait a bounded moment to finish so whatever tmux (or ssh)
		// wrote to stderr can name the reason: readLoop's EOF and the process
		// exit race, and readLoop is usually first, so the raw error here is
		// often "reader EOF" with no cause in it at all.
		select {
		case <-channel.waited:
		case <-time.After(attachDiagnosticsWait):
		}
		_ = channel.Close()
		return nil, fmt.Errorf("tmux control attach: %w", channel.withDiagnostics(err))
	case <-time.After(3 * time.Second):
		_ = channel.Close()
		return nil, fmt.Errorf("tmux control attach: timeout")
	}
}

const (
	// maxControlStderrBytes bounds what one channel retains from stderr. A
	// login profile that prints on every ssh can be arbitrarily chatty, and the
	// only part that ever matters is the tail, where the failure is.
	maxControlStderrBytes = 4 << 10

	// attachDiagnosticsWait is how long a failed attach waits for the child's
	// stderr before reporting without it. The process has already exited by
	// this point, so this is a bound on a race, not on a subprocess.
	attachDiagnosticsWait = 500 * time.Millisecond
)

// withDiagnostics folds the command's stderr into its error so the message
// survives the trip to whoever has to classify it.
func (c *processControlChannel) withDiagnostics(err error) error {
	message := strings.TrimSpace(c.diagnostics.String())
	if message == "" {
		return err
	}
	if err == nil {
		return fmt.Errorf("tmux control exited: %s", message)
	}
	return fmt.Errorf("%w: %s", err, message)
}

// boundedTail keeps the last limit bytes written to it. It is written by
// os/exec's copier and read by the attach path, so it locks.
type boundedTail struct {
	limit int
	mu    sync.Mutex
	buf   []byte
}

func (t *boundedTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if t.limit > 0 && len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *boundedTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (c *processControlChannel) Send(command string, callback func(controlResponse)) error {
	return c.write([]string{command}, []func(controlResponse){callback})
}

func (c *processControlChannel) SendBatch(commands []string, callbacks []func(controlResponse)) error {
	if len(commands) == 0 || len(commands) != len(callbacks) {
		return fmt.Errorf("tmux control: command/callback count mismatch")
	}
	return c.write(commands, callbacks)
}

func (c *processControlChannel) SendPair(first, second string, firstCallback, secondCallback func(controlResponse)) error {
	return c.write([]string{first, second}, []func(controlResponse){firstCallback, secondCallback})
}

func (c *processControlChannel) SendTriple(first, second, third string, firstCallback, secondCallback, thirdCallback func(controlResponse)) error {
	return c.write(
		[]string{first, second, third},
		[]func(controlResponse){firstCallback, secondCallback, thirdCallback},
	)
}

// write emits every command in one io.WriteString so tmux reads the whole group
// in a single read and queues the commands together.
//
// This is safe to call from the ordered actor goroutine even though it blocks:
// tmux never blocks writing to a control client. It buffers client output in
// memory and, when a client falls behind, either drops bytes (counted by
// client_discarded) or pauses the pane. Its event loop therefore keeps draining
// our stdin, so a command write cannot deadlock against a stalled reader.
func (c *processControlChannel) write(commands []string, callbacks []func(controlResponse)) error {
	var payload strings.Builder
	for _, command := range commands {
		if strings.ContainsAny(command, "\r\n") {
			return fmt.Errorf("tmux control: multiline command rejected")
		}
		payload.WriteString(command)
		payload.WriteByte('\n')
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	c.pending = append(c.pending, callbacks...)
	c.mu.Unlock()
	if written, err := io.WriteString(c.stdin, payload.String()); err != nil {
		// Unregistering the callbacks is only sound when nothing reached tmux: a
		// short write can still have delivered a whole command line, and tmux will
		// answer it with a response block that the remaining FIFO must still line
		// up against. On a partial write the callbacks are therefore left in place
		// — the write error kills the channel anyway, so they are dropped with it,
		// but they are never removed while a response for them may still arrive.
		if written == 0 {
			c.mu.Lock()
			if drop := min(len(callbacks), len(c.pending)); drop > 0 {
				c.pending = c.pending[:len(c.pending)-drop]
			}
			c.mu.Unlock()
		}
		c.finish(fmt.Errorf("tmux control write: %w", err))
		return err
	}
	return nil
}

func (c *processControlChannel) Events() <-chan controlEvent { return c.events }
func (c *processControlChannel) Done() <-chan error          { return c.done }

func (c *processControlChannel) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		closeErr = c.stdin.Close()
		if c.cmd.Process != nil {
			if err := c.cmd.Process.Kill(); closeErr == nil && err != nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func (c *processControlChannel) readLoop(reader io.Reader) {
	buffered := bufio.NewReader(reader)
	for {
		line, err := buffered.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			for _, event := range c.parser.FeedLine(line) {
				c.dispatch(event)
			}
		}
		if err != nil {
			if err == io.EOF {
				err = fmt.Errorf("tmux control: reader EOF")
			}
			c.finish(err)
			return
		}
	}
}

// dispatch places every parsed event on the single ordered stream. Command
// responses are correlated with their FIFO callback here — on the reader
// goroutine, where receive order is authoritative — but the callback is carried
// on the event and invoked downstream by the one actor that also consumes
// %output notifications. That is the ordering barrier the byte-fed screen model
// depends on: a capture response can never be processed ahead of pane bytes
// tmux emitted before it, nor behind bytes tmux emitted after it.
func (c *processControlChannel) dispatch(event controlEvent) {
	if event.Kind == controlEventResponse {
		c.mu.Lock()
		if !c.readyOK {
			c.readyOK = true
			close(c.ready)
			c.mu.Unlock()
			return
		}
		if len(c.pending) == 0 {
			c.mu.Unlock()
			return
		}
		event.Callback = c.pending[0]
		copy(c.pending, c.pending[1:])
		c.pending = c.pending[:len(c.pending)-1]
		c.mu.Unlock()
		if event.Callback == nil {
			return
		}
	}
	select {
	case c.events <- event:
	case <-c.dead:
	}
}

func (c *processControlChannel) finish(err error) {
	c.finishOnce.Do(func() {
		if err == nil {
			err = fmt.Errorf("tmux control exited")
		}
		select {
		case c.done <- err:
		default:
		}
		close(c.dead)
	})
}
