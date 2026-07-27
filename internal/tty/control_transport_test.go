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
	closes   int
}

func (w *testWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.buffer.Write(p)
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
	channel.dispatch(controlEvent{Kind: controlEventResponse, Response: controlResponse{Lines: []string{"one"}}})
	channel.dispatch(controlEvent{Kind: controlEventResponse, Response: controlResponse{Lines: []string{"two"}}})
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

func TestProcessControlChannelReadLoopHandlesHandshakeAndEOF(t *testing.T) {
	channel := &processControlChannel{
		stdin:  &testWriteCloser{},
		events: make(chan controlEvent, 1),
		done:   make(chan error, 1),
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
