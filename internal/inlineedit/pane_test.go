package inlineedit

import "testing"

func TestHandleConfirmKeyDecides(t *testing.T) {
	s := &Session{ShowExitConfirm: true}

	if outcome, handled := s.HandleConfirmKey("j"); !handled || outcome != OutcomePending {
		t.Fatalf("j: got (%v,%v), want (pending,true)", outcome, handled)
	}
	if s.ConfirmSelection != 1 {
		t.Fatalf("selection = %d, want 1", s.ConfirmSelection)
	}
	outcome, handled := s.HandleConfirmKey("enter")
	if !handled || outcome != OutcomeDiscard {
		t.Fatalf("enter on option 1: got (%v,%v), want (discard,true)", outcome, handled)
	}
	if s.ShowExitConfirm {
		t.Fatal("confirmation still up after a decision")
	}

	s = &Session{ShowExitConfirm: true, PendingClickRegion: "leaf"}
	if outcome, handled := s.HandleConfirmKey("esc"); !handled || outcome != OutcomeCancel {
		t.Fatalf("esc: got (%v,%v), want (cancel,true)", outcome, handled)
	}
	if s.PendingClickRegion != "" {
		t.Fatal("cancel kept the pending click")
	}

	// A key the dialog does not own is still reported as consumed by the
	// caller's standards: nothing behind a modal may act on it.
	if outcome, handled := s.HandleConfirmKey("x"); handled || outcome != OutcomePending {
		t.Fatalf("closed dialog: got (%v,%v), want (pending,false)", outcome, handled)
	}
}

func TestRouteWithoutSessionIsInert(t *testing.T) {
	var s *Session
	if cmd, alive := s.Route(nil); cmd != nil || alive {
		t.Fatal("nil session routed a message")
	}
	s = &Session{}
	if _, alive := s.Route(nil); alive {
		t.Fatal("inactive session reported alive")
	}
}
