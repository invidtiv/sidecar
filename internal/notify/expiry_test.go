package notify

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func TestExpiryForFallsBackToTheRegistryDefault(t *testing.T) {
	t.Cleanup(func() { SetSourceExpiries(nil) })
	SetSourceExpiries(nil)

	if got := ExpiryFor(SourceAgent); got != 12*time.Second {
		t.Fatalf("agent default expiry = %s, want 12s", got)
	}
	if got := ExpiryFor(SourceWaiting); got != 0 {
		t.Fatalf("waiting default expiry = %s, want sticky (0)", got)
	}
}

func TestConfiguredExpiryReachesNormalize(t *testing.T) {
	t.Cleanup(func() { SetSourceExpiries(nil) })
	ApplyConfig(config.NotificationsConfig{Sources: map[string]config.NotificationSourceConfig{
		"agent":   {Expiry: "45s"},
		"session": {Expiry: "sticky"},
	}})

	now := time.Now().UTC()
	agent := Normalize(Notification{Source: SourceAgent, Title: "hi"}, now)
	if agent.ExpiresAt == nil || agent.ExpiresAt.Sub(agent.CreatedAt) != 45*time.Second {
		t.Fatalf("agent expiry not taken from config: %+v", agent.ExpiresAt)
	}

	session := Normalize(Notification{Source: SourceSession, Title: "hi"}, now)
	if !session.Sticky || session.ExpiresAt != nil {
		t.Fatalf("configured sticky session should have no countdown: %+v", session)
	}

	// A source the config says nothing about keeps its default.
	if got := ExpiryFor(SourceTD); got != 10*time.Second {
		t.Fatalf("td expiry = %s, want the 10s default", got)
	}
}

func TestUnreadableExpiryIsIgnoredRatherThanFatal(t *testing.T) {
	t.Cleanup(func() { SetSourceExpiries(nil) })
	ApplyConfig(config.NotificationsConfig{Sources: map[string]config.NotificationSourceConfig{
		"agent": {Expiry: "twelve seconds"},
	}})
	if got := ExpiryFor(SourceAgent); got != 12*time.Second {
		t.Fatalf("a bad duration must leave the default in place, got %s", got)
	}
}
