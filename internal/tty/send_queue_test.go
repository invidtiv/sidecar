package tty

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// td-8fcd2e: keystrokes must reach tmux in the order they were enqueued, even
// though each one is awaited from its own Bubble Tea command goroutine.
func TestSendOrderedPreservesCallOrder(t *testing.T) {
	const n = 50
	target := fmt.Sprintf("test-order-%d", time.Now().UnixNano())

	var mu sync.Mutex
	var got []int

	waits := make([]<-chan error, 0, n)
	for i := range n {
		// Enqueue in the "Update loop": synchronous, single-threaded.
		waits = append(waits, SendOrdered(target, func() error {
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
			return nil
		}))
	}

	// Await from concurrent goroutines, as tea.Cmds do.
	var wg sync.WaitGroup
	for _, done := range waits {
		wg.Add(1)
		go func(ch <-chan error) {
			defer wg.Done()
			<-ch
		}(done)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("ran %d jobs, want %d", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("job %d ran at position %d; delivery order = %v", v, i, got)
		}
	}
}

func TestSendOrderedReportsError(t *testing.T) {
	target := fmt.Sprintf("test-err-%d", time.Now().UnixNano())
	want := errors.New("can't find pane")

	if got := <-SendOrdered(target, func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSendOrderedEmptyTargetDoesNotBlock(t *testing.T) {
	select {
	case err := <-SendOrdered("", func() error { return errors.New("should not run") }):
		if err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SendOrdered with an empty target never resolved")
	}
}

// A queue goroutine deregisters itself once idle so long-lived processes do not
// accumulate one per dead tmux session.
func TestSendOrderedQueueIsRegisteredPerTarget(t *testing.T) {
	target := fmt.Sprintf("test-reg-%d", time.Now().UnixNano())
	<-SendOrdered(target, func() error { return nil })

	sendQueuesMu.Lock()
	_, ok := sendQueues[target]
	sendQueuesMu.Unlock()
	if !ok {
		t.Fatal("expected a live queue for the target right after a send")
	}
}
