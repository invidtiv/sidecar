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

func TestApplyConfigPublishesAnImmutableResolvedSnapshot(t *testing.T) {
	t.Cleanup(func() { ApplyConfig(config.DefaultNotificationsConfig()) })
	native := true
	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode = config.DeliveryBackground
	cfg.Sources = map[string]config.NotificationSourceConfig{
		"waiting": {Native: &native, Sound: config.SoundAttention, Expiry: "33s"},
	}
	ApplyConfig(cfg)

	// Mutating the loader model after Apply cannot mutate the live policy.
	native = false
	configured := cfg.Sources["waiting"]
	configured.Expiry = "1s"
	cfg.Sources["waiting"] = configured
	snapshot := CurrentConfig()
	rule := snapshot.SourceRule(SourceWaiting)
	if snapshot.NativeMode != config.DeliveryBackground || !rule.Native || rule.Expiry != 33*time.Second {
		t.Fatalf("resolved snapshot changed through source config: mode=%q rule=%+v", snapshot.NativeMode, rule)
	}

	// Inspection returns a defensive copy too.
	rules := snapshot.SourceRules()
	mutated := rules[SourceWaiting]
	mutated.Native = false
	rules[SourceWaiting] = mutated
	if !CurrentConfig().SourceRule(SourceWaiting).Native {
		t.Fatal("SourceRules exposed the live policy map")
	}
}
