package mobile

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

func encoderBudgetFrame(handle string, sequence uint64, payloadBytes int) mobileproto.Response {
	return mobileproto.Response{Version: 0, Type: mobileproto.ResponseFrame, AttachmentHandle: handle, AttachmentGeneration: 1, ResetGeneration: 1, OutputSequence: sequence, Coalesced: true, RenderVTBase64: strings.Repeat("x", payloadBytes)}
}

func encoderWireBytes(t *testing.T, r mobileproto.Response) int {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return len(data) + 1
}

func TestEncoderByteBudgetRejectsBelowQueueDepth(t *testing.T) {
	for _, coalesced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "negotiated"}[coalesced], func(t *testing.T) {
			e := &safeEncoder{wake: make(chan struct{}, 1)}
			frame := encoderBudgetFrame("a", 1, 7<<20)
			frame.Coalesced = coalesced
			for _, handle := range []string{"a", "b"} {
				frame.AttachmentHandle = handle
				if err := e.write(frame); err != nil {
					t.Fatal(err)
				}
			}
			frame.AttachmentHandle = "c"
			if err := e.write(frame); err == nil || !strings.Contains(err.Error(), "byte budget") {
				t.Fatalf("accepted third 7 MiB frame below count limit: %v", err)
			}
			if len(e.pending) != 2 || e.outputBytes > encoderOutputBytes {
				t.Fatalf("overflow mutated queue: count=%d bytes=%d", len(e.pending), e.outputBytes)
			}
		})
	}
}

func TestEncoderReplacementAccountsForGrowthAndShrink(t *testing.T) {
	e := &safeEncoder{wake: make(chan struct{}, 1)}
	a, b, c := encoderBudgetFrame("a", 1, 1<<20), encoderBudgetFrame("b", 1, 7<<20), encoderBudgetFrame("c", 1, 7<<20)
	for _, frame := range []mobileproto.Response{a, b, c} {
		if err := e.write(frame); err != nil {
			t.Fatal(err)
		}
	}
	original := e.outputBytes
	growth := encoderBudgetFrame("a", 2, 3<<20)
	if err := e.write(growth); err == nil {
		t.Fatal("coalesced replacement exceeded budget")
	}
	if e.outputBytes != original || e.pending[0].OutputSequence != 1 {
		t.Fatal("rejected replacement discarded last valid frame")
	}
	small := encoderBudgetFrame("b", 2, 1<<20)
	if err := e.write(small); err != nil {
		t.Fatal(err)
	}
	want := encoderWireBytes(t, a) + encoderWireBytes(t, small) + encoderWireBytes(t, c)
	if e.outputBytes != want {
		t.Fatalf("shrink bytes=%d want=%d", e.outputBytes, want)
	}
	if err := e.write(growth); err != nil {
		t.Fatal(err)
	}
	want = encoderWireBytes(t, growth) + encoderWireBytes(t, small) + encoderWireBytes(t, c)
	if len(e.pending) != 3 || e.outputBytes != want || e.pending[0].OutputSequence != 2 {
		t.Fatalf("growth count=%d bytes=%d want=%d", len(e.pending), e.outputBytes, want)
	}
}

type encoderGateWriter struct {
	entered chan int
	release chan struct{}
	stop    chan struct{}
	once    sync.Once
	short   bool
}

func (w *encoderGateWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- len(p):
	case <-w.stop:
		return 0, io.ErrClosedPipe
	}
	select {
	case <-w.release:
	case <-w.stop:
		return 0, io.ErrClosedPipe
	}
	if w.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func (w *encoderGateWriter) close() { w.once.Do(func() { close(w.stop) }) }
func (w *encoderGateWriter) next(t *testing.T) int {
	t.Helper()
	select {
	case n := <-w.entered:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("encoder write did not start")
		return 0
	}
}
func (w *encoderGateWriter) unblock(t *testing.T) {
	t.Helper()
	select {
	case w.release <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("encoder write did not unblock")
	}
}

func TestEncoderByteBudgetIncludesInflightAndReleasesAfterWrite(t *testing.T) {
	w := &encoderGateWriter{entered: make(chan int), release: make(chan struct{}), stop: make(chan struct{})}
	e := newSafeEncoder(w)
	t.Cleanup(func() { w.close(); e.close() })
	first, second, third := encoderBudgetFrame("a", 1, 7<<20), encoderBudgetFrame("b", 1, 7<<20), encoderBudgetFrame("c", 1, 7<<20)
	if err := e.write(first); err != nil {
		t.Fatal(err)
	}
	firstBytes := w.next(t)
	if err := e.write(second); err != nil {
		t.Fatal(err)
	}
	if err := e.write(third); err == nil {
		t.Fatal("in-flight 7 MiB frame was not charged")
	}
	e.mu.Lock()
	charged := e.outputBytes
	pending := len(e.pending)
	e.mu.Unlock()
	if charged != firstBytes+encoderWireBytes(t, second) || pending != 1 {
		t.Fatalf("charged=%d pending=%d", charged, pending)
	}
	w.unblock(t)
	secondBytes := w.next(t)
	e.mu.Lock()
	charged = e.outputBytes
	e.mu.Unlock()
	if charged != secondBytes {
		t.Fatalf("completed frame still charged: %d want %d", charged, secondBytes)
	}
	if err := e.write(third); err != nil {
		t.Fatalf("released budget unavailable: %v", err)
	}
	w.unblock(t)
	w.next(t)
	w.unblock(t)
	e.close()
	e.mu.Lock()
	charged = e.outputBytes
	e.mu.Unlock()
	if charged != 0 {
		t.Fatalf("drained encoder retains %d bytes", charged)
	}
}

func TestEncoderShortWriteReleasesQueuedBudgetAndStops(t *testing.T) {
	w := &encoderGateWriter{entered: make(chan int), release: make(chan struct{}), stop: make(chan struct{}), short: true}
	e := newSafeEncoder(w)
	t.Cleanup(func() { w.close(); e.close() })
	failed := make(chan error, 1)
	e.mu.Lock()
	e.onError = func(err error) { failed <- err }
	e.mu.Unlock()
	if err := e.write(encoderBudgetFrame("a", 1, 1<<20)); err != nil {
		t.Fatal(err)
	}
	w.next(t)
	if err := e.write(encoderBudgetFrame("b", 1, 1<<20)); err != nil {
		t.Fatal(err)
	}
	w.unblock(t)
	select {
	case err := <-failed:
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("write error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("short write was not reported")
	}
	e.mu.Lock()
	charged, pending := e.outputBytes, len(e.pending)
	e.mu.Unlock()
	if charged != 0 || pending != 0 {
		t.Fatalf("failed queue retained bytes=%d count=%d", charged, pending)
	}
	if err := e.write(mobileproto.Response{Type: "status"}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("failed encoder accepted another response: %v", err)
	}
}
