package mobile

import (
	"bytes"
	"context"
	"errors"
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
