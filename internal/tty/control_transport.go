package tty

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type controlChannel interface {
	Send(command string, callback func(controlResponse)) error
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

func newProcessControlChannelForSocket(socket, session string) (controlChannel, error) {
	return newProcessControlChannelCommand(session, exec.Command(
		"tmux", "-S", socket, "-C", "attach-session", "-f", "ignore-size", "-t", session,
	))
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
	if strings.ContainsAny(command, "\r\n") {
		return fmt.Errorf("tmux control: multiline command rejected")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	c.pending = append(c.pending, callback)
	c.mu.Unlock()
	if _, err := io.WriteString(c.stdin, command+"\n"); err != nil {
		c.mu.Lock()
		if len(c.pending) > 0 {
			c.pending = c.pending[:len(c.pending)-1]
		}
		c.mu.Unlock()
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

func (c *processControlChannel) dispatch(event controlEvent) {
	if event.Kind != controlEventResponse {
		select {
		case c.events <- event:
		case <-c.done:
		}
		return
	}

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
	callback := c.pending[0]
	copy(c.pending, c.pending[1:])
	c.pending = c.pending[:len(c.pending)-1]
	c.mu.Unlock()
	if callback != nil {
		callback(event.Response)
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
