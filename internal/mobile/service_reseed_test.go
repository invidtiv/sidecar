package mobile

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

type recordingSubscription struct{ requests chan struct{} }

func (r *recordingSubscription) RequestSnapshot() {
	select {
	case r.requests <- struct{}{}:
	default:
	}
}
func (r *recordingSubscription) Close() {}

// A capture failure resets the attachment. The protocol promises a
// replacement full frame after every reset, and on an idle pane nothing else
// will cause one, so the service must ask for it.
func TestCaptureFailureRequestsAReplacementFrame(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	sub := &recordingSubscription{requests: make(chan struct{}, 4)}
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1,
		firstOutputForReset: 1, outputSequence: 1, subscription: sub}
	a.captureFailed(errors.New("tmux control client closed"))
	responses := decodeResponses(t, &output)
	if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[0].Reason != "capture_failed" ||
		responses[0].ResetGeneration != 2 || responses[1].Type != mobileproto.ResponseError {
		t.Fatalf("responses = %#v", responses)
	}
	select {
	case <-sub.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("capture_failed reset was never followed by a snapshot request")
	}
	// Repeated failures back off instead of spinning.
	a.mu.Lock()
	delay := a.reseedDelay
	a.mu.Unlock()
	if delay != 2*reseedInitialDelay {
		t.Fatalf("reseed delay after one failure = %v", delay)
	}
	for i := 0; i < 10; i++ {
		a.captureFailed(errors.New("still failing"))
	}
	a.mu.Lock()
	delay = a.reseedDelay
	a.mu.Unlock()
	if delay != reseedMaxDelay {
		t.Fatalf("reseed delay did not cap: %v", delay)
	}
}

type failingRelease struct{ released int }

func (f *failingRelease) Resize(int, int) error         { return nil }
func (f *failingRelease) Heartbeat() error              { return nil }
func (f *failingRelease) SendLiteral([]byte) error      { return nil }
func (f *failingRelease) ExpirePresence() (bool, error) { return false, nil }
func (f *failingRelease) Release() error {
	f.released++
	return errors.New("conditional clear failed")
}

// Every refused mutation leaves operation_sequence untouched; a release whose
// lease clear fails is refused, so it must too. Control is revoked regardless.
func TestFailedReleaseDoesNotConsumeTheOperationSequence(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	geometry := &failingRelease{}
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), control: true, geometry: geometry,
		operationSequence: 4, resetGeneration: 2, outputSequence: 9, firstOutputForReset: 3}
	s.attachments[a.handle] = a
	s.release(context.Background(), mobileproto.Request{AttachmentHandle: a.handle, RequestID: "release", OperationSequence: 5,
		LastResetGeneration: 2, LastOutputSequence: 9})
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseError || responses[0].Error.Code != mobileproto.ErrorBackend {
		t.Fatalf("responses = %#v", responses)
	}
	if geometry.released != 1 || a.control || a.geometry != nil {
		t.Fatalf("release did not revoke control: released %d control %v", geometry.released, a.control)
	}
	if a.operationSequence != 4 {
		t.Fatalf("failed release consumed operation_sequence: %d", a.operationSequence)
	}
}

type closingSubscription struct {
	recordingSubscription
	closed chan struct{}
	once   sync.Once
}

func (c *closingSubscription) Close() { c.once.Do(func() { close(c.closed) }) }

// A capture source that dies because its pane or session is gone can never be
// reseeded. The attachment must end with the target refusal instead of
// resetting and respawning a capture every few seconds forever.
func TestCaptureFailureOnAGoneTargetEndsTheAttachment(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.resolve = func(context.Context, string) (ResolvedTarget, error) {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: `managed shell "mobile" is no longer registered`}
	}
	sub := &closingSubscription{recordingSubscription: recordingSubscription{requests: make(chan struct{}, 4)}, closed: make(chan struct{})}
	a := newAttachment(s, "attachment", "client", 1, testTarget())
	a.subscription = sub
	s.attachments[a.handle] = a
	close(a.ready)
	a.offerFailure(errors.New("tmux control client closed"))
	select {
	case <-sub.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the capture subscription for a gone target was never closed")
	}
	<-a.stopped
	select {
	case <-sub.requests:
		t.Fatal("a gone target was asked for another capture")
	case <-time.After(2 * reseedInitialDelay):
	}
	s.mu.Lock()
	_, still := s.attachments[a.handle]
	s.mu.Unlock()
	if still {
		t.Fatal("the ended attachment is still registered")
	}
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseError || responses[0].RequestID != "" ||
		responses[0].Error.Code != mobileproto.ErrorIdentityChanged || responses[0].Error.Retry || responses[0].AttachmentHandle != "" {
		t.Fatalf("responses = %#v", responses)
	}
}

// A revalidation that fails for a reason that says nothing about the target
// keeps the transient path: reset, retryable error, backoff, reseed.
func TestCaptureFailureWithAnInconclusiveCheckStillReseeds(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.resolve = func(context.Context, string) (ResolvedTarget, error) {
		return ResolvedTarget{}, errors.New("state file temporarily unreadable")
	}
	sub := &recordingSubscription{requests: make(chan struct{}, 4)}
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1,
		firstOutputForReset: 1, outputSequence: 1, subscription: sub}
	if a.captureFailed(errors.New("tmux control client closed")) {
		t.Fatal("an inconclusive check ended the attachment")
	}
	responses := decodeResponses(t, &output)
	if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[1].Error == nil || !responses[1].Error.Retry {
		t.Fatalf("responses = %#v", responses)
	}
	select {
	case <-sub.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("transient capture failure was not reseeded")
	}
}

// A failed control connection cannot establish that its target is gone.
// Recovery must use independent read-only evidence, while operations continue
// to fail closed on the owning control connection.
func TestCaptureFailureDoesNotMistakeADeadTransportForAGoneTarget(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.revalidateTarget = func(context.Context, ResolvedTarget) (ResolvedTarget, error) {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "owning control connection is unavailable"}
	}
	s.captureRevalidateTarget = func(context.Context, ResolvedTarget) (ResolvedTarget, error) {
		return testTarget().resolved, nil
	}
	sub := &recordingSubscription{requests: make(chan struct{}, 4)}
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1, subscription: sub}
	s.attachments[a.handle] = a
	if a.captureFailed(errors.New("control pipe closed")) {
		t.Fatal("a healthy target ended because its capture transport died")
	}
	responses := decodeResponses(t, &output)
	if len(responses) != 2 || responses[0].Reason != mobileproto.ResetCaptureFailed || responses[1].Error == nil || !responses[1].Error.Retry {
		t.Fatalf("transient capture failure = %#v", responses)
	}
	select {
	case <-sub.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("healthy target was not reseeded")
	}
}

type acceptingGeometry struct{}

func (acceptingGeometry) Resize(int, int) error         { return nil }
func (acceptingGeometry) Heartbeat() error              { return nil }
func (acceptingGeometry) SendLiteral([]byte) error      { return nil }
func (acceptingGeometry) ExpirePresence() (bool, error) { return false, nil }
func (acceptingGeometry) Release() error                { return nil }

// A resize is acknowledged, and before the attachment captures its expected
// geometry another lease holder resizes the pane. Every capture from then on
// shows the foreign size. The attachment must stop waiting for a geometry that
// will never come: revoke control with a geometry_changed reset and publish
// the frame at the size the pane really has, instead of requesting snapshots
// forever while the client sits on a stale screen.
func TestForeignResizeWhileAwaitingExpectedGeometryEndsTheWait(t *testing.T) {
	for _, tc := range []struct {
		name          string
		columns, rows int
	}{
		{name: "a third size", columns: 6, rows: 3},
		{name: "back to the size before the claim", columns: 4, rows: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := testService(&output)
			sub := &recordingSubscription{requests: make(chan struct{}, 64)}
			a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), control: true, geometry: acceptingGeometry{},
				operationSequence: 1, resetGeneration: 1, outputSequence: 1, firstOutputForReset: 1,
				latest: testSnapshot("A   \n    ", false, 4, 2), snapshots: make(chan queuedSnapshot, 1), subscription: sub}
			s.attachments[a.handle] = a
			s.resize(context.Background(), mobileproto.Request{RequestID: "resize", AttachmentHandle: a.handle, OperationSequence: 2,
				LastResetGeneration: 1, LastOutputSequence: 1, Columns: 5, Rows: 3})
			ack := decodeResponses(t, &output)
			if len(ack) != 2 || ack[0].Type != mobileproto.ResponseResized || ack[1].Reason != mobileproto.ResetResize {
				t.Fatalf("resize responses = %#v", ack)
			}
			// The foreign resize lands first, so every capture the attachment
			// asks for shows it.
			lines := make([]string, tc.rows)
			for index := range lines {
				lines[index] = strings.Repeat("F", tc.columns)
			}
			foreign := testSnapshot(strings.Join(lines, "\n"), false, tc.columns, tc.rows)
			var responses []mobileproto.Response
			for capture := 0; capture < 8; capture++ {
				a.offerSnapshot(foreign)
				a.publishQueued(<-a.snapshots)
				responses = append(responses, decodeResponses(t, &output)...)
				if len(responses) > 0 && responses[len(responses)-1].Type == mobileproto.ResponseFrame {
					break
				}
			}
			if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[0].Reason != mobileproto.ResetGeometryChanged ||
				responses[1].Type != mobileproto.ResponseFrame || responses[1].ResetGeneration != responses[0].ResetGeneration ||
				responses[1].Geometry == nil || responses[1].Geometry.Columns != tc.columns || responses[1].Geometry.Rows != tc.rows {
				t.Fatalf("after a foreign resize the attachment emitted %#v (snapshot requests: %d)", responses, len(sub.requests))
			}
			if a.control || a.geometry != nil || a.expectedColumns != 0 || a.expectedRows != 0 || a.firstOutputForReset == 0 {
				t.Fatalf("wait did not end cleanly: control=%t expected=%dx%d first=%d", a.control, a.expectedColumns, a.expectedRows, a.firstOutputForReset)
			}
		})
	}
}

// The wait for an expected geometry exists for a capture that was already in
// flight when tmux acknowledged the resize. Ending the wait on a foreign
// resize must not end it on that capture: it is dropped, control is kept, and
// the frame at the requested size follows in the resize's reset generation.
func TestInFlightPreResizeCaptureStillWaitsForTheExpectedGeometry(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	sub := &recordingSubscription{requests: make(chan struct{}, 64)}
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), control: true, geometry: acceptingGeometry{},
		operationSequence: 1, resetGeneration: 1, outputSequence: 1, firstOutputForReset: 1,
		latest: testSnapshot("A   \n    ", false, 4, 2), snapshots: make(chan queuedSnapshot, 1), subscription: sub}
	s.attachments[a.handle] = a
	s.resize(context.Background(), mobileproto.Request{RequestID: "resize", AttachmentHandle: a.handle, OperationSequence: 2,
		LastResetGeneration: 1, LastOutputSequence: 1, Columns: 5, Rows: 3})
	_ = decodeResponses(t, &output)
	a.offerSnapshot(testSnapshot("B   \n    ", false, 4, 2)) // captured before tmux applied the resize
	a.publishQueued(<-a.snapshots)
	if responses := decodeResponses(t, &output); len(responses) != 0 {
		t.Fatalf("in-flight pre-resize capture published %#v", responses)
	}
	a.offerSnapshot(testSnapshot("CCCCC\nCCCCC\nCCCCC", false, 5, 3))
	a.publishQueued(<-a.snapshots)
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseFrame || responses[0].ResetGeneration != 2 ||
		responses[0].Geometry == nil || responses[0].Geometry.Columns != 5 || responses[0].Geometry.Rows != 3 {
		t.Fatalf("responses = %#v", responses)
	}
	if !a.control || a.expectedColumns != 0 {
		t.Fatalf("expected resize lost control or stayed pending: control=%t expected=%d", a.control, a.expectedColumns)
	}
}
