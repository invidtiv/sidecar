package mobilehub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
)

type fakeRouteRegistry struct {
	mu          sync.Mutex
	valid       bool
	validations int
	mode        string
	args        []string
}

type blockingWriteCloser struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

func (w *blockingWriteCloser) Write(p []byte) (int, error) {
	w.enterOnce.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func (w *blockingWriteCloser) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}

func (w *blockingWriteCloser) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (f *fakeRouteRegistry) ValidateMobileRoute(hosts.MobileRouteAuthority) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validations++
	if !f.valid {
		return hosts.ErrMobileRouteChanged
	}
	return nil
}

func (f *fakeRouteRegistry) MobileSidecarCommand(ctx context.Context, _ hosts.MobileRouteAuthority) (*exec.Cmd, error) {
	if err := f.ValidateMobileRoute(hosts.MobileRouteAuthority{}); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.args = []string{"mobile", "serve", "--stdio"}
	mode := f.mode
	f.mu.Unlock()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMobileOwnerHelperProcess", "--")
	cmd.Env = append(os.Environ(), "SIDECAR_MOBILE_OWNER_HELPER=1", "SIDECAR_MOBILE_OWNER_MODE="+mode)
	return cmd, nil
}

func TestOwnerStreamDoesNotGenerateRequests(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if line, err := stream.ReadLine(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected autonomous owner line=%q err=%v", line, err)
	}
}

func TestOwnerStreamOutputOverflowIsTerminal(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "burst"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.WriteLine([]byte(`{"version":0,"type":"status","request_id":"burst"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := stream.Wait(ctx); !errors.Is(err, ErrOwnerOutputOverflow) {
		t.Fatalf("overflow wait error = %v", err)
	}
	if err := stream.WriteLine([]byte(`{"version":0,"type":"status","request_id":"later"}`)); !errors.Is(err, ErrOwnerOutputOverflow) {
		t.Fatalf("write after overflow = %v", err)
	}
}

func (f *fakeRouteRegistry) invalidate() {
	f.mu.Lock()
	f.valid = false
	f.mu.Unlock()
}

func TestOwnerStreamValidatesActualHelloAndForwardsOnlyCallerLines(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, hello, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	if hello.APIInstance != "owner-test" || hello.Capabilities == nil || !hello.Capabilities.HistorySnapshots {
		t.Fatalf("owner hello = %+v", hello)
	}
	registry.mu.Lock()
	wantArgs := []string{"mobile", "serve", "--stdio"}
	if strings.Join(registry.args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("owner args = %q", registry.args)
	}
	registry.mu.Unlock()
	request := []byte(`{"version":0,"type":"status","request_id":"phone-1"}`)
	if err := stream.WriteLine(request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	line, err := stream.ReadLine(ctx)
	if err != nil || string(line) != string(request) {
		t.Fatalf("forwarded line=%q err=%v", line, err)
	}
	registry.mu.Lock()
	validations := registry.validations
	registry.mu.Unlock()
	if validations < 4 {
		t.Fatalf("route validations = %d, want bind/start/after-hello/write", validations)
	}
}

func TestOwnerStreamRefusesInvalidOrIncompleteHello(t *testing.T) {
	for _, mode := range []string{"malformed", "incomplete", "wrong-version"} {
		t.Run(mode, func(t *testing.T) {
			registry := &fakeRouteRegistry{valid: true, mode: mode}
			stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
			if err == nil || stream != nil {
				t.Fatalf("invalid hello accepted stream=%v err=%v", stream, err)
			}
		})
	}
}

func TestOwnerStreamRefusesRouteChangeBeforeForwarding(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	registry.invalidate()
	if err := stream.WriteLine([]byte(`{"version":0,"type":"heartbeat","request_id":"phone-1"}`)); !errors.Is(err, hosts.ErrMobileRouteChanged) {
		t.Fatalf("write after route change = %v", err)
	}
	stream.Close()
}

func TestOwnerStreamRevalidatesQueuedWriteAtMutationBoundary(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true}
	writer := &blockingWriteCloser{entered: make(chan struct{}), release: make(chan struct{})}
	stream := &OwnerStream{registry: registry, stdin: writer, cancel: func() {}}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- stream.WriteLine([]byte(`{"version":0,"type":"status","request_id":"first"}`)) }()
	<-writer.entered
	go func() { secondDone <- stream.WriteLine([]byte(`{"version":0,"type":"status","request_id":"second"}`)) }()
	registry.invalidate()
	close(writer.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("already-authorized write = %v", err)
	}
	if err := <-secondDone; !errors.Is(err, hosts.ErrMobileRouteChanged) {
		t.Fatalf("queued write after retarget = %v", err)
	}
	if !writer.isClosed() {
		t.Fatal("route failure did not close owner stdin")
	}
}

func TestOwnerStreamDiscardsQueuedResponseAfterRouteFailure(t *testing.T) {
	writer := &blockingWriteCloser{entered: make(chan struct{}), release: make(chan struct{})}
	stream := &OwnerStream{stdin: writer, lines: make(chan []byte, 1), cancel: func() {}}
	stream.lines <- []byte(`{"version":0,"type":"accepted","request_id":"stale"}`)
	stream.fail(hosts.ErrMobileRouteChanged)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if line, err := stream.ReadLine(ctx); !errors.Is(err, hosts.ErrMobileRouteChanged) || line != nil {
		t.Fatalf("queued response after route failure line=%q err=%v", line, err)
	}
	if !writer.isClosed() {
		t.Fatal("route failure did not close owner stdin")
	}
}

func TestOwnerStreamCancelsIdleProcessAfterRouteChange(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	registry.invalidate()
	ctx, cancel := context.WithTimeout(context.Background(), 2*ownerRoutePoll)
	defer cancel()
	if err := stream.Wait(ctx); !errors.Is(err, hosts.ErrMobileRouteChanged) {
		t.Fatalf("idle route change wait error = %v", err)
	}
}

func TestOwnerStreamCloseIsNormalTermination(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stream.Wait(ctx); err != nil {
		t.Fatalf("closed owner wait error = %v", err)
	}
}

func TestOwnerStreamBoundsForwardedLines(t *testing.T) {
	registry := &fakeRouteRegistry{valid: true, mode: "echo"}
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	for _, line := range [][]byte{nil, []byte("one\ntwo"), make([]byte, mobileproto.MaxLineBytes+1)} {
		if err := stream.WriteLine(line); err == nil {
			t.Fatalf("unbounded line of %d bytes accepted", len(line))
		}
	}
}

func TestMobileOwnerHelperProcess(t *testing.T) {
	if os.Getenv("SIDECAR_MOBILE_OWNER_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		os.Exit(3)
	}
	var request mobileproto.Request
	if json.Unmarshal([]byte(line), &request) != nil || request.Type != mobileproto.RequestHello {
		os.Exit(4)
	}
	mode := os.Getenv("SIDECAR_MOBILE_OWNER_MODE")
	if mode == "malformed" {
		fmt.Println("not-json")
		os.Exit(0)
	}
	hello := mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHello, RequestID: request.RequestID, APIInstance: "owner-test"}
	capabilities := mobileproto.DefaultCapabilities()
	hello.Capabilities = &capabilities
	if mode == "incomplete" {
		hello.Capabilities.HistorySnapshots = false
	}
	if mode == "wrong-version" {
		hello.Version++
	}
	if json.NewEncoder(os.Stdout).Encode(hello) != nil {
		os.Exit(5)
	}
	if mode != "echo" && mode != "burst" {
		os.Exit(0)
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if mode == "burst" {
			for i := 0; i < mobileproto.OutboundQueueDepth+2; i++ {
				fmt.Printf("{\"line\":%d}\n", i)
			}
			continue
		}
		fmt.Println(scanner.Text())
	}
	os.Exit(0)
}
