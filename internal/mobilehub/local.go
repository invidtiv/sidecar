package mobilehub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

type LocalServiceFactory func(io.Reader, io.Writer) (*mobile.Service, error)

type localStream struct {
	input     *io.PipeWriter
	cancel    context.CancelFunc
	lines     chan []byte
	done      chan struct{}
	writeMu   sync.Mutex
	stateMu   sync.Mutex
	terminal  error
	closing   bool
	closeOnce sync.Once
}

// StartLocal starts the hub machine's owning service in process, consumes and
// validates its private hello, and returns the same bounded line seam used for
// remote owners.
func StartLocal(ctx context.Context, factory LocalServiceFactory) (LineStream, mobileproto.Response, error) {
	if factory == nil {
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: local service factory is required")
	}
	requests, input := io.Pipe()
	output, responses := io.Pipe()
	service, err := factory(requests, responses)
	if err != nil {
		_ = input.Close()
		_ = requests.Close()
		_ = output.Close()
		_ = responses.Close()
		return nil, mobileproto.Response{}, err
	}
	ownerCtx, cancel := context.WithCancel(ctx)
	stream := &localStream{input: input, cancel: cancel, lines: make(chan []byte, mobileproto.OutboundQueueDepth), done: make(chan struct{})}
	go func() {
		err := service.Run(ownerCtx)
		_ = responses.CloseWithError(err)
	}()
	go stream.read(output)
	request := mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestHello, RequestID: "hub-owner-hello"}
	data, _ := json.Marshal(request)
	if err := stream.WriteLine(data); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, OwnerHandshakeTimeout)
	defer handshakeCancel()
	line, err := stream.ReadLine(handshakeCtx)
	if err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	var hello mobileproto.Response
	if err := json.Unmarshal(line, &hello); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, fmt.Errorf("mobile hub: invalid local owner hello: %w", err)
	}
	if err := validateOwnerHello(hello); err != nil {
		stream.Close()
		return nil, mobileproto.Response{}, err
	}
	return stream, hello, nil
}

func (s *localStream) read(output *io.PipeReader) {
	defer close(s.done)
	defer close(s.lines)
	defer func() { _ = output.Close() }()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes+1)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		select {
		case s.lines <- line:
		default:
			s.fail(ErrOwnerOutputOverflow)
			return
		}
	}
	if err := scanner.Err(); err != nil && !s.isClosing() {
		s.fail(fmt.Errorf("mobile hub: read local owner response: %w", err))
	} else if !s.isClosing() && s.terminalError() == nil {
		s.fail(io.EOF)
	}
}

func (s *localStream) WriteLine(line []byte) error {
	if len(line) == 0 || len(line) > mobileproto.MaxLineBytes || hasNewline(line) {
		return fmt.Errorf("mobile hub: local owner request is empty, multiline or oversized")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.terminalError(); err != nil {
		return err
	}
	if s.isClosing() {
		return ErrOwnerClosed
	}
	if _, err := s.input.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		wrapped := fmt.Errorf("mobile hub: write local owner request: %w", err)
		s.fail(wrapped)
		return wrapped
	}
	return nil
}

func (s *localStream) ReadLine(ctx context.Context) ([]byte, error) {
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
		if !ok {
			return nil, io.EOF
		}
		return line, nil
	}
}

func (s *localStream) Close() {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closing = true
		s.stateMu.Unlock()
		_ = s.input.Close()
		s.cancel()
	})
}

func (s *localStream) fail(err error) {
	if err == nil {
		return
	}
	s.stateMu.Lock()
	if s.terminal == nil {
		s.terminal = err
	}
	s.stateMu.Unlock()
	_ = s.input.CloseWithError(err)
	s.cancel()
}

func (s *localStream) terminalError() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.terminal
}

func (s *localStream) isClosing() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closing
}

func hasNewline(line []byte) bool {
	for _, value := range line {
		if value == '\n' || value == '\r' {
			return true
		}
	}
	return false
}

var _ LineStream = (*localStream)(nil)
