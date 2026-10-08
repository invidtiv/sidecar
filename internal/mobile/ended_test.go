package mobile

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestExitedCaptureIsFinalWithoutResetOrError(t *testing.T) {
	for _, negotiated := range []bool{false, true} {
		var output bytes.Buffer
		s := testService(&output)
		s.clientCaps.TerminalEnded = negotiated
		status := 7
		s.endObserver = func(context.Context, ResolvedTarget) (*TerminalEnd, error) {
			return &TerminalEnd{ExitStatus: &status}, nil
		}
		sub := &recordingSubscription{requests: make(chan struct{}, 1)}
		a := newAttachment(s, "attachment", "client", 2, testTarget())
		a.subscription = sub
		close(a.ready)
		s.attachments[a.handle] = a
		if !a.captureFailed(errors.New("tmux control exit notification")) {
			t.Fatal("exited attachment reseeded")
		}
		<-a.stopped
		responses := decodeResponses(t, &output)
		if len(responses) != 1 {
			t.Fatalf("responses=%+v", responses)
		}
		if negotiated {
			r := responses[0]
			if r.Type != mobileproto.ResponseEnded || r.Reason != mobileproto.EndExited || r.AttachmentHandle != a.handle || r.AttachmentGeneration != 2 || r.ExitStatus == nil || *r.ExitStatus != 7 || r.Error != nil || r.ResetGeneration != 0 {
				t.Fatalf("end=%+v", r)
			}
		} else if responses[0].Type != mobileproto.ResponseError {
			t.Fatalf("legacy=%+v", responses)
		}
		if _, live := s.attachment(a.handle); live {
			t.Fatal("attachment still live")
		}
		select {
		case <-sub.requests:
			t.Fatal("end requested capture")
		default:
		}
	}
}

type exitingResizeGeometry struct{ acceptingGeometry }

func (exitingResizeGeometry) Resize(int, int) error { return errors.New("can't find pane") }

func TestResizeRaceProducesEndBeforeGeometryError(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.clientCaps.TerminalEnded = true
	s.endObserver = func(context.Context, ResolvedTarget) (*TerminalEnd, error) { return &TerminalEnd{}, nil }
	a := newAttachment(s, "attachment", "client", 1, testTarget())
	a.geometry = exitingResizeGeometry{}
	a.control = true
	a.resetGeneration, a.outputSequence, a.firstOutputForReset = 1, 1, 1
	s.attachments[a.handle] = a
	close(a.ready)
	s.resize(context.Background(), mobileproto.Request{RequestID: "racing-resize", AttachmentHandle: a.handle, OperationSequence: 1, LastResetGeneration: 1, LastOutputSequence: 1, Columns: 80, Rows: 24})
	<-a.stopped
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseEnded {
		t.Fatalf("responses=%+v", responses)
	}
}
