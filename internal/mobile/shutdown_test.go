package mobile

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestCloseAllBoundsConcurrentStuckAttachmentCleanup(t *testing.T) {
	s, err := New(Config{Input: bytes.NewReader(nil), Output: io.Discard, Resolver: func(context.Context, string) (ResolvedTarget, error) { return ResolvedTarget{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.out.close()
	// Simulate a backend operation holding the attachment lock indefinitely.
	a := &attachment{stop: make(chan struct{}), stopped: make(chan struct{})}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	close(a.stopped)
	s.attachments["stuck"] = a
	first, second := make(chan struct{}), make(chan struct{})
	go func() { s.closeAll(); close(first) }()
	go func() { s.closeAll(); close(second) }()
	deadline := time.After(shutdownTimeout + time.Second)
	for _, done := range []<-chan struct{}{first, second} {
		select {
		case <-done:
		case <-deadline:
			t.Fatal("closeAll blocked on attachment teardown or sync.Once")
		}
	}
}

func TestTransportDoneObservesEOFWhileRequestHandlerIsBlocked(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = input.Close() }()
	defer func() { _ = writer.Close() }()
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := New(Config{Input: input, Output: io.Discard, OwnerConfigGeneration: "config", Resolver: func(context.Context, string) (ResolvedTarget, error) {
		close(entered)
		<-release
		return ResolvedTarget{}, io.ErrClosedPipe
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	defer func() { close(release); _ = writer.Close(); <-done }()
	_, err = io.WriteString(writer, "{\"version\":0,\"type\":\"hello\",\"request_id\":\"hello\"}\n{\"version\":0,\"type\":\"resolve\",\"request_id\":\"resolve\",\"target\":\"stuck\"}\n")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resolver was not called")
	}
	_ = writer.Close()
	select {
	case <-s.TransportDone():
	case <-time.After(time.Second):
		t.Fatal("EOF was hidden by the blocked request")
	}
}

func TestTransportDoneObservesEOFBehindQueuedRequest(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = input.Close() }()
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := New(Config{Input: input, Output: io.Discard, OwnerConfigGeneration: "config", Resolver: func(context.Context, string) (ResolvedTarget, error) {
		close(entered)
		<-release
		return ResolvedTarget{}, io.ErrClosedPipe
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	defer func() { close(release); _ = writer.Close(); <-done }()
	_, _ = io.WriteString(writer, "{\"version\":0,\"type\":\"hello\",\"request_id\":\"hello\"}\n{\"version\":0,\"type\":\"resolve\",\"request_id\":\"resolve\",\"target\":\"stuck\"}\n")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resolver was not called")
	}
	_, _ = io.WriteString(writer, "{\"version\":0,\"type\":\"sessions\",\"request_id\":\"queued\"}\n")
	_ = writer.Close()
	select {
	case <-s.TransportDone():
	case <-time.After(time.Second):
		t.Fatal("queued request hid EOF behind the blocked handler")
	}
}

func TestRequestScannerBoundsPendingQueueAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		capacity          int
	}{
		{"count", "one\ntwo\n", "inbound queue overflow", 1},
		{"bytes", strings.Repeat(strings.Repeat("a", inboundQueueBytes/3+1)+"\n", 3), "inbound byte budget exceeded", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tc.input))
			scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes)
			lines := make(chan []byte, tc.capacity)
			var queued atomic.Int64
			err := scanMobileRequests(context.Background(), scanner, lines, make(chan struct{}), &queued)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scanner error = %v, want %q", err, tc.want)
			}
			if queued.Load() > inboundQueueBytes {
				t.Fatalf("queue retained %d bytes", queued.Load())
			}
		})
	}
}
