package features

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

func TestConversationsPlugin_DefaultOff(t *testing.T) {
	globalManager = nil
	if ConversationsPlugin.Default {
		t.Error("conversations_plugin must ship disabled by default")
	}
	if IsEnabled(ConversationsPlugin.Name) {
		t.Error("conversations_plugin should be disabled without config")
	}
	if !IsKnownFeature(ConversationsPlugin.Name) {
		t.Error("conversations_plugin should be a registered feature")
	}
}

func TestConversationsPlugin_ConfigEnables(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[ConversationsPlugin.Name] = true
	Init(cfg)
	t.Cleanup(func() { globalManager = nil })

	if !IsEnabled(ConversationsPlugin.Name) {
		t.Error("conversations_plugin should be enabled by config")
	}

	SetOverride(ConversationsPlugin.Name, false)
	if IsEnabled(ConversationsPlugin.Name) {
		t.Error("CLI override should win over config")
	}
}

func TestConversationsPlugin_ListedInAll(t *testing.T) {
	found := false
	for _, f := range ListAll() {
		if f.Name == ConversationsPlugin.Name {
			found = true
		}
	}
	if !found {
		t.Error("conversations_plugin missing from ListAll")
	}
}
