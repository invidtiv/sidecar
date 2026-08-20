package msg

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/notify"
)

// A source-specific alert is a notification, so the centre can separate an
// agent's failure from generic app chrome by hue and priority.
func TestAlertCarriesItsSource(t *testing.T) {
	post, ok := Alert(notify.SourceAgent, notify.SeverityWarning, "Agent failed")().(notify.PostMsg)
	if !ok {
		t.Fatalf("Alert produced %T, want notify.PostMsg", Alert(notify.SourceAgent, notify.SeverityWarning, "x")())
	}
	if post.Notification.Source != notify.SourceAgent {
		t.Errorf("source = %q", post.Notification.Source)
	}
	if post.Notification.Severity != notify.SeverityWarning {
		t.Errorf("severity = %q", post.Notification.Severity)
	}
	if post.Notification.Title != "Agent failed" {
		t.Errorf("title = %q", post.Notification.Title)
	}
}

// A refused action speaks as `waiting` — the user has to do something — but
// leases itself so it does not sit in the centre the way a real waiting agent
// should.
func TestBlockedIsAWaitingWarningWithALease(t *testing.T) {
	post := Blocked("Git write already in progress")().(notify.PostMsg)
	if post.Notification.Source != notify.SourceWaiting {
		t.Errorf("source = %q, want waiting", post.Notification.Source)
	}
	if post.Notification.Severity != notify.SeverityWarning {
		t.Errorf("severity = %q, want warning", post.Notification.Severity)
	}
	if post.Notification.ExpiresAt == nil {
		t.Fatal("a refusal with no expiry would be sticky")
	}
	// CreatedAt is the store's to set, so the lease is measured from now.
	if got := time.Until(*post.Notification.ExpiresAt); got > blockedActionExpiry || got < blockedActionExpiry-time.Second {
		t.Errorf("lease = %v, want about %v", got, blockedActionExpiry)
	}
}

// A flash is never a notification: it must not reach the store.
func TestShowFlashIsNotANotification(t *testing.T) {
	if _, ok := ShowFlash("Copied")().(notify.PostMsg); ok {
		t.Fatal("a flash posted a notification")
	}
	if got := ShowFlash("Copied")().(FlashMsg).Text; got != "Copied" {
		t.Errorf("flash text = %q", got)
	}
}
