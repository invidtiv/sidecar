package tty

import (
	"sync"
	"time"
)

// Keystroke delivery ordering (td-8fcd2e).
//
// Every keypress in interactive mode returns its own tea.Cmd, and Bubble Tea
// runs each Cmd in a fresh goroutine. Two keys typed in quick succession
// therefore race into `tmux send-keys` and can arrive transposed — typing
// "whoami" lands as "whoaim".
//
// The fix is to fix the order at the point where the key is handled rather than
// where its Cmd happens to be scheduled. Update runs single-threaded, so
// enqueueing there establishes a total order; the returned Cmd only waits for
// its own job to complete so session-dead errors still surface as messages.
const (
	sendQueueDepth       = 512
	sendQueueIdleTimeout = 30 * time.Second
	sendQueueBackoff     = time.Millisecond
)

type sendJob struct {
	run  func() error
	done chan error
}

type sendQueue struct {
	jobs chan sendJob
}

var (
	sendQueuesMu sync.Mutex
	sendQueues   = make(map[string]*sendQueue)

	pendingMu   sync.Mutex
	pendingIdle = sync.NewCond(&pendingMu)
	pendingJobs int
)

// SendOrdered queues work for a tmux target and returns a channel that receives
// its result. Jobs for the same target run one at a time, in call order.
//
// Call this from the Update loop, not from inside a tea.Cmd — the ordering
// guarantee is only as good as the ordering of the calls.
func SendOrdered(target string, run func() error) <-chan error {
	done := make(chan error, 1)
	if target == "" || run == nil {
		done <- nil
		return done
	}

	job := sendJob{run: run, done: done}
	for {
		sendQueuesMu.Lock()
		q, ok := sendQueues[target]
		if !ok {
			q = &sendQueue{jobs: make(chan sendJob, sendQueueDepth)}
			sendQueues[target] = q
			go q.run(target)
		}
		select {
		case q.jobs <- job:
			pendingMu.Lock()
			pendingJobs++
			pendingMu.Unlock()
			sendQueuesMu.Unlock()
			return done
		default:
		}
		sendQueuesMu.Unlock()
		// Backlogged past the buffer. Yield instead of blocking with the registry
		// lock held, then retry — dropping the job would lose a keystroke and
		// sending it inline would break the order we are trying to preserve.
		time.Sleep(sendQueueBackoff)
	}
}

// SendKeysOrdered queues a keystroke batch for a tmux target. The batch is
// delivered atomically with respect to other queued work for the same target.
func SendKeysOrdered(target string, keys ...KeySpec) <-chan error {
	return SendOrdered(target, func() error {
		return SendKeys(target, keys...)
	})
}

// WaitForPendingSends blocks until every queued send has run.
//
// Enqueueing happens where the key is handled, so the tmux call escapes the
// caller's goroutine and can land arbitrarily later — after a test has restored
// PATH, for instance, or while a different test owns the fake tmux. Tests that
// assert on what was spawned use this to draw a hard line around themselves.
func WaitForPendingSends() {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	for pendingJobs > 0 {
		pendingIdle.Wait()
	}
}

func (q *sendQueue) run(target string) {
	idle := time.NewTimer(sendQueueIdleTimeout)
	defer idle.Stop()

	for {
		select {
		case job := <-q.jobs:
			job.done <- job.run()
			pendingMu.Lock()
			pendingJobs--
			if pendingJobs == 0 {
				pendingIdle.Broadcast()
			}
			pendingMu.Unlock()
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(sendQueueIdleTimeout)

		case <-idle.C:
			sendQueuesMu.Lock()
			if len(q.jobs) > 0 {
				// Raced with an enqueue; keep draining.
				sendQueuesMu.Unlock()
				idle.Reset(sendQueueIdleTimeout)
				continue
			}
			if sendQueues[target] == q {
				delete(sendQueues, target)
			}
			sendQueuesMu.Unlock()
			return
		}
	}
}
