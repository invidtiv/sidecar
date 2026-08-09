package tty

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

type testWriteCloser struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	writeErr error
	writes   int
	closes   int
}

func (w *testWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.writes++
	return w.buffer.Write(p)
}

func TestProcessControlChannelSendTripleUsesOneWriteAndThreeFIFOResponses(t *testing.T) {
	writer := &testWriteCloser{}
	channel := &processControlChannel{
		stdin:   writer,
		events:  make(chan controlEvent, 3),
		done:    make(chan error, 1),
		dead:    make(chan struct{}),
		ready:   make(chan struct{}),
		readyOK: true,
	}
	var got []string
	callback := func(response controlResponse) {
		got = append(got, strings.Join(response.Lines, ""))
	}
	if err := channel.SendTriple("metadata", "capture-main", "capture-active", callback, callback, callback); err != nil {
		t.Fatal(err)
	}
	if writer.writes != 1 || writer.buffer.String() != "metadata\ncapture-main\ncapture-active\n" {
		t.Fatalf("writes = %d, payload = %q", writer.writes, writer.buffer.String())
	}
	for _, response := range []string{"meta", "main", "active"} {
		channel.dispatch(controlEvent{Kind: controlEventResponse, Response: controlResponse{Lines: []string{response}}})
	}
	for range 3 {
		event := <-channel.Events()
		if event.Callback == nil {
			t.Fatal("response did not retain its FIFO callback")
		}
		event.Callback(event.Response)
	}
	if strings.Join(got, ",") != "meta,main,active" {
		t.Fatalf("callbacks = %#v", got)
	}
}

func (w *testWriteCloser) Close() error {
	w.mu.Lock()
	w.closes++
	w.mu.Unlock()
	return nil
}

func TestProcessControlChannelCorrelatesResponsesFIFO(t *testing.T) {
	writer := &testWriteCloser{}
	channel := &processControlChannel{
		stdin:   writer,
		events:  make(chan controlEvent, 4),
		done:    make(chan error, 1),
		dead:    make(chan struct{}),
		ready:   make(chan struct{}),
		readyOK: true,
	}
	var got []string
	if err := channel.Send("first", func(response controlResponse) {
		got = append(got, strings.Join(response.Lines, ""))
	}); err != nil {
		t.Fatal(err)
	}
	if err := channel.Send("second", func(response controlResponse) {
		got = append(got, strings.Join(response.Lines, ""))
	}); err != nil {
		t.Fatal(err)
	}
	// Responses are correlated FIFO on the reader goroutine but delivered on the
	// single ordered event stream, so the callback travels with the event and is
	// run by whoever drains it. That is the barrier the screen model relies on.
	channel.dispatch(controlEvent{Kind: controlEventResponse, Response: controlResponse{Lines: []string{"one"}}})
	channel.dispatch(controlEvent{Kind: controlEventResponse, Response: controlResponse{Lines: []string{"two"}}})
	for range 2 {
		select {
		case event := <-channel.Events():
			if event.Kind != controlEventResponse || event.Callback == nil {
				t.Fatalf("event = %#v", event)
			}
			event.Callback(event.Response)
		default:
			t.Fatal("response was not placed on the ordered event stream")
		}
	}
	if strings.Join(got, ",") != "one,two" {
		t.Fatalf("callbacks = %#v", got)
	}
	if writer.buffer.String() != "first\nsecond\n" {
		t.Fatalf("writes = %q", writer.buffer.String())
	}
	if err := channel.Send("bad\ncommand", nil); err == nil {
		t.Fatal("multiline control command accepted")
	}
}

func TestProcessControlChannelWriteFailureSignalsDone(t *testing.T) {
	writer := &testWriteCloser{writeErr: errors.New("broken pipe")}
	channel := &processControlChannel{
		stdin:   writer,
		events:  make(chan controlEvent, 1),
		done:    make(chan error, 1),
		dead:    make(chan struct{}),
		ready:   make(chan struct{}),
		readyOK: true,
	}
	if err := channel.Send("capture-pane", nil); err == nil {
		t.Fatal("write error not returned")
	}
	select {
	case err := <-channel.Done():
		if err == nil || !strings.Contains(err.Error(), "write") {
			t.Fatalf("done error = %v", err)
		}
	default:
		t.Fatal("write failure did not signal Done")
	}
}

// A short write can still have delivered a whole command line, so tmux may
// answer commands the writer reported as failed. Unregistering their callbacks
// would shift every later response one slot down the FIFO. Nothing may be
// unregistered unless the write delivered nothing at all.
type partialWriteCloser struct {
	mu sync.Mutex
	n  int
}

func (w *partialWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n > len(p) {
		w.n = len(p)
	}
	return w.n, errors.New("broken pipe")
}

func (w *partialWriteCloser) Close() error { return nil }

func TestProcessControlChannelPartialWriteKeepsTheFIFOAligned(t *testing.T) {
	for _, tc := range []struct {
		name        string
		written     int
		wantPending int
	}{
		{name: "nothing reached tmux", written: 0, wantPending: 0},
		{name: "one command line reached tmux", written: len("first\n"), wantPending: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &processControlChannel{
				stdin:   &partialWriteCloser{n: tc.written},
				events:  make(chan controlEvent, 4),
				done:    make(chan error, 1),
				dead:    make(chan struct{}),
				ready:   make(chan struct{}),
				readyOK: true,
			}
			noop := func(controlResponse) {}
			if err := channel.SendPair("first", "second", noop, noop); err == nil {
				t.Fatal("write error not returned")
			}
			channel.mu.Lock()
			pending := len(channel.pending)
			channel.mu.Unlock()
			if pending != tc.wantPending {
				t.Fatalf("pending callbacks = %d, want %d", pending, tc.wantPending)
			}
		})
	}
}

func TestProcessControlChannelTriplePartialWriteKeepsTheFIFOAligned(t *testing.T) {
	for _, tc := range []struct {
		name        string
		written     int
		wantPending int
	}{
		{name: "nothing reached tmux", written: 0, wantPending: 0},
		{name: "one command line reached tmux", written: len("metadata\n"), wantPending: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &processControlChannel{
				stdin:   &partialWriteCloser{n: tc.written},
				events:  make(chan controlEvent, 4),
				done:    make(chan error, 1),
				dead:    make(chan struct{}),
				ready:   make(chan struct{}),
				readyOK: true,
			}
			noop := func(controlResponse) {}
			if err := channel.SendTriple("metadata", "capture-main", "capture-active", noop, noop, noop); err == nil {
				t.Fatal("write error not returned")
			}
			channel.mu.Lock()
			pending := len(channel.pending)
			channel.mu.Unlock()
			if pending != tc.wantPending {
				t.Fatalf("pending callbacks = %d, want %d", pending, tc.wantPending)
			}
		})
	}
}

func TestProcessControlChannelReadLoopHandlesHandshakeAndEOF(t *testing.T) {
	channel := &processControlChannel{
		stdin:  &testWriteCloser{},
		events: make(chan controlEvent, 1),
		done:   make(chan error, 1),
		dead:   make(chan struct{}),
		ready:  make(chan struct{}),
	}
	channel.readLoop(strings.NewReader("%begin 1 1 0\nattached\n%end 1 1 0\n"))
	select {
	case <-channel.ready:
	default:
		t.Fatal("attach response did not mark channel ready")
	}
	select {
	case err := <-channel.done:
		if err == nil || !strings.Contains(err.Error(), "EOF") {
			t.Fatalf("EOF error = %v", err)
		}
	default:
		t.Fatal("reader EOF did not signal Done")
	}
}

func TestProcessControlChannelCloseIsIdempotent(t *testing.T) {
	writer := &testWriteCloser{}
	channel := &processControlChannel{
		cmd:    &exec.Cmd{},
		stdin:  writer,
		events: make(chan controlEvent, 1),
		done:   make(chan error, 1),
		dead:   make(chan struct{}),
		ready:  make(chan struct{}),
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if writer.closes != 1 {
		t.Fatalf("stdin closed %d times, want 1", writer.closes)
	}
}

var _ io.WriteCloser = (*testWriteCloser)(nil)
