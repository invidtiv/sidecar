package cli

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMobileOwnerWatchdogBoundsUnresponsiveRequest(t *testing.T) {
	for _, cause := range []string{"transport", "context"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := make(chan struct{})
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			done := make(chan error, 1)
			go func() {
				done <- runMobileOwnerUntilClosed(ctx, func(context.Context) error {
					close(entered)
					<-release
					return nil
				}, transport, 25*time.Millisecond)
			}()
			<-entered
			if cause == "transport" {
				close(transport)
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "shutdown exceeded") {
					t.Fatalf("watchdog result = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("owner runner remained blocked after transport loss")
			}
		})
	}
}

func TestMobileOwnerWatchdogLetsFinalRequestFinish(t *testing.T) {
	transport := make(chan struct{})
	close(transport)
	err := runMobileOwnerUntilClosed(context.Background(), func(context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	}, transport, time.Second)
	if err != nil {
		t.Fatalf("normal EOF failed: %v", err)
	}
}

func TestMobileOwnerParentLossCancelsEvenWithOpenTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var parent atomic.Int64
	parent.Store(42)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		monitorMobileOwnerParent(ctx, 42, func() int { return int(parent.Load()) }, time.Millisecond, cancel)
	}()
	select {
	case <-ctx.Done():
		t.Fatal("unchanged parent cancelled a healthy owner")
	case <-time.After(10 * time.Millisecond):
	}
	parent.Store(1)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner failed to observe parent loss")
	}
	<-monitorDone
}

func TestMobileOwnerAlreadyOrphanedCancelsAtStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitorMobileOwnerParent(ctx, 1, func() int { return 1 }, time.Second, cancel)
	if ctx.Err() == nil {
		t.Fatal("already orphaned owner remained active")
	}
}
