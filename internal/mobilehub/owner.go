// Package mobilehub routes the public mobile protocol to the Sidecar process
// that owns a selected terminal. It does not own terminal state or leases.
package mobilehub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	OwnerHandshakeTimeout = 5 * time.Second
	OwnerStderrBytes      = 8 << 10
	ownerRoutePoll        = time.Second
)

var (
	ErrOwnerOutputOverflow = errors.New("mobile hub: owner output queue overflow")
	ErrOwnerClosed         = errors.New("mobile hub: owner stream closed")
)

// RouteRegistry is the narrow registered-host seam the hub uses. The
// production implementation is hosts.Registry; tests can inject owner
// processes without an SSH server.
type RouteRegistry interface {
	ValidateMobileRoute(hosts.MobileRouteAuthority) error
	MobileSidecarCommand(context.Context, hosts.MobileRouteAuthority) (*exec.Cmd, error)
}

// OwnerStream is one validated remote `mobile serve` process. It forwards raw
// v0 lines after consuming the owner's private hello; it never parses terminal
// payloads or creates requests of its own.
type OwnerStream struct {
	registry  RouteRegistry
	authority hosts.MobileRouteAuthority
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	lines     chan []byte
	cancel    context.CancelFunc
	done      chan struct{}
	stderr    *boundedBuffer
	writeMu   sync.Mutex
	closeOnce sync.Once
	inputOnce sync.Once
	stateMu   sync.Mutex
	terminal  error
	waitErr   error
	closing   bool
}

// StartOwner starts the owning Sidecar through the exact bound registry
// client, sends the protocol's private hello, validates the actual owner
// capabilities, and revalidates the route before returning.
func StartOwner(ctx context.Context, registry RouteRegistry, authority hosts.MobileRouteAuthority) (*OwnerStream, mobileproto.Response, error) {
	if registry == nil {
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: registry is required")
	}
	if err := registry.ValidateMobileRoute(authority); err != nil {
		return nil, mobileproto.Response{}, err
	}
	ownerCtx, cancel := context.WithCancel(ctx)
	cmd, err := registry.MobileSidecarCommand(ownerCtx, authority)
	if err != nil {
		cancel()
		return nil, mobileproto.Response{}, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: owner stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: owner stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: owner stderr: %w", err)
	}
	stream := &OwnerStream{registry: registry, authority: authority, cmd: cmd, stdin: stdin,
		lines: make(chan []byte, mobileproto.OutboundQueueDepth), cancel: cancel, done: make(chan struct{}),
		stderr: &boundedBuffer{limit: OwnerStderrBytes}}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		cancel()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: start owner: %w", err)
	}
	go func() { _, _ = io.Copy(stream.stderr, stderrPipe) }()
	go stream.run(stdout)
	request := mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestHello, RequestID: "hub-owner-hello"}
	data, _ := json.Marshal(request)
	if err := stream.writeLine(data, false); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, OwnerHandshakeTimeout)
	defer handshakeCancel()
	line, err := stream.readLine(handshakeCtx)
	if err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: owner hello: %w%s", err, stream.stderrSuffix())
	}
	var hello mobileproto.Response
	if err := json.Unmarshal(line, &hello); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: invalid owner hello: %w", err)
	}
	if err := validateOwnerHello(hello); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	if err := registry.ValidateMobileRoute(authority); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	if err := stream.terminalError(); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	go stream.monitorRoute(ownerCtx)
	return stream, hello, nil
}

func validateOwnerHello(hello mobileproto.Response) error {
	if hello.Version != mobileproto.Version || hello.Type != mobileproto.ResponseHello || hello.RequestID != "hub-owner-hello" || hello.APIInstance == "" || hello.Capabilities == nil {
		return fmt.Errorf("mobile hub: owner did not return a valid v%d hello", mobileproto.Version)
	}
	caps := hello.Capabilities
	if !caps.NormalizedFullFrames || !caps.Input || !caps.Resize || !caps.Reconnect || !caps.CatalogSnapshots || !caps.HistorySnapshots ||
		caps.MaximumColumns <= 0 || caps.MaximumColumns > mobileproto.MaxColumns || caps.MaximumRows <= 0 || caps.MaximumRows > mobileproto.MaxRows ||
		caps.MaximumInputBytes <= 0 || caps.MaximumInputBytes > mobileproto.MaxInputBytes || caps.MaximumLineBytes <= 0 || caps.MaximumLineBytes > mobileproto.MaxLineBytes ||
		caps.MaximumHistoryRows <= 0 || caps.MaximumHistoryRows > mobileproto.MaxHistoryRows || caps.MaximumHistoryBytes <= 0 || caps.MaximumHistoryBytes > mobileproto.MaxHistoryBytes ||
		caps.HeartbeatIntervalMS <= 0 || caps.PresenceTimeoutMS <= caps.HeartbeatIntervalMS {
		return fmt.Errorf("mobile hub: owner mobile v%d capabilities are incomplete", mobileproto.Version)
	}
	return nil
}

func (s *OwnerStream) run(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes+1)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) > mobileproto.MaxLineBytes {
			s.fail(fmt.Errorf("mobile hub: owner response exceeds %d bytes", mobileproto.MaxLineBytes))
			break
		}
		select {
		case s.lines <- line:
		default:
			s.fail(ErrOwnerOutputOverflow)
		}
		if s.terminalError() != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		s.fail(fmt.Errorf("mobile hub: read owner response: %w", err))
	} else if !s.isClosing() && s.terminalError() == nil {
		s.fail(io.EOF)
	}
	waitErr := s.cmd.Wait()
	s.stateMu.Lock()
	s.waitErr = waitErr
	if waitErr != nil && !s.closing && s.terminal == nil {
		s.terminal = fmt.Errorf("mobile hub: owner process: %w", waitErr)
	}
	s.stateMu.Unlock()
	close(s.lines)
	close(s.done)
}

func (s *OwnerStream) readLine(ctx context.Context) ([]byte, error) {
	if err := s.terminalError(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case line, ok := <-s.lines:
		if err := s.terminalError(); err != nil {
			return nil, err
		}
		if ok {
			return line, nil
		}
		if err := s.terminalError(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
}

// ReadLine reads one raw owner response or event with the caller's deadline.
func (s *OwnerStream) ReadLine(ctx context.Context) ([]byte, error) { return s.readLine(ctx) }

// WriteLine forwards exactly one client request after current route
// validation. It appends the JSONL delimiter and never generates heartbeat or
// any other request itself.
func (s *OwnerStream) WriteLine(line []byte) error { return s.writeLine(line, true) }

func (s *OwnerStream) writeLine(line []byte, validate bool) error {
	if len(line) == 0 || len(line) > mobileproto.MaxLineBytes || bytes.IndexByte(line, '\n') >= 0 {
		return fmt.Errorf("mobile hub: owner request line is empty, multiline or exceeds %d bytes", mobileproto.MaxLineBytes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.terminalError(); err != nil {
		return err
	}
	if s.isClosing() {
		return ErrOwnerClosed
	}
	if validate {
		if err := s.registry.ValidateMobileRoute(s.authority); err != nil {
			s.fail(err)
			return err
		}
	}
	if _, err := s.stdin.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		wrapped := fmt.Errorf("mobile hub: write owner request: %w", err)
		s.fail(wrapped)
		return wrapped
	}
	return nil
}

func (s *OwnerStream) monitorRoute(ctx context.Context) {
	ticker := time.NewTicker(ownerRoutePoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.registry.ValidateMobileRoute(s.authority); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

// Close closes owner stdin and cancels the SSH command. The owner's existing
// EOF and process cleanup own exact lease release.
func (s *OwnerStream) Close() {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closing = true
		s.stateMu.Unlock()
		s.closeInput()
		s.cancel()
	})
}

func (s *OwnerStream) fail(err error) {
	if err == nil {
		return
	}
	s.stateMu.Lock()
	if s.terminal == nil {
		s.terminal = err
	}
	s.stateMu.Unlock()
	s.closeInput()
	s.cancel()
}

func (s *OwnerStream) closeInput() {
	s.inputOnce.Do(func() { _ = s.stdin.Close() })
}

func (s *OwnerStream) terminalError() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.terminal
}

func (s *OwnerStream) isClosing() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closing
}

// Wait reports the owner process result. Context cancellation is a normal
// local close; callers still treat unexpected EOF before close as terminal.
func (s *OwnerStream) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		s.stateMu.Lock()
		closing, terminal, waitErr := s.closing, s.terminal, s.waitErr
		s.stateMu.Unlock()
		if terminal != nil {
			return terminal
		}
		if closing {
			return nil
		}
		if errors.Is(waitErr, context.Canceled) {
			return nil
		}
		return waitErr
	}
}

func (s *OwnerStream) stderrSuffix() string {
	if text := s.stderr.String(); text != "" {
		return ": " + text
	}
	return ""
}

type boundedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	writable := min(len(p), max(b.limit-b.buf.Len(), 0))
	_, _ = b.buf.Write(p[:writable])
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
