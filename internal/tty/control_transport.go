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
	// SendPair writes two commands in a single write so tmux reads and queues
	// them together and executes them back to back. tmux emits one response
	// block per command line, so two FIFO callbacks are registered; a parse
	// error in one line still produces that line's own error block, which keeps
	// the FIFO 1:1. This exists so a seed transaction's metadata and its
	// rendered capture describe the same moment.
	SendPair(first, second string, firstCallback, secondCallback func(controlResponse)) error
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
	ready  chan struct{}

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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux control stderr: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux control stdin: %w", err)
	}
	channel := &processControlChannel{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan controlEvent, 128),
		done:   make(chan error, 1),
		ready:  make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tmux control: %w", err)
	}
	go channel.readLoop(stdout)
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go func() {
		err := cmd.Wait()
		channel.finish(err)
	}()

	select {
	case <-channel.ready:
		return channel, nil
	case err := <-channel.done:
		_ = channel.Close()
		return nil, fmt.Errorf("tmux control attach: %w", err)
	case <-time.After(3 * time.Second):
		_ = channel.Close()
		return nil, fmt.Errorf("tmux control attach: timeout")
	}
}

func (c *processControlChannel) Send(command string, callback func(controlResponse)) error {
	return c.write([]string{command}, []func(controlResponse){callback})
}

func (c *processControlChannel) SendPair(first, second string, firstCallback, secondCallback func(controlResponse)) error {
	return c.write([]string{first, second}, []func(controlResponse){firstCallback, secondCallback})
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
	case <-c.done:
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
	})
}
