package features

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

func TestPluginContentPanesDefaultOnAndOverrides(t *testing.T) {
	globalManager = nil
	if !PluginContentPanes.Default || !DefaultEnabled(PluginContentPanes.Name) || !IsEnabled(PluginContentPanes.Name) {
		t.Fatal("plugin_content_panes must ship enabled by default")
	}
	if !IsKnownFeature(PluginContentPanes.Name) {
		t.Fatal("plugin_content_panes is not registered")
	}

	cfg := config.Default()
	cfg.Features.Flags[PluginContentPanes.Name] = false
	Init(cfg)
	t.Cleanup(func() { globalManager = nil })
	if IsEnabled(PluginContentPanes.Name) {
		t.Fatal("explicit config false did not disable default-on content panes")
	}
	SetOverride(PluginContentPanes.Name, true)
	if !IsEnabled(PluginContentPanes.Name) {
		t.Fatal("CLI override did not win over explicit config false")
	}
}

func TestPluginContentPanesListedInAll(t *testing.T) {
	for _, feature := range ListAll() {
		if feature.Name == PluginContentPanes.Name {
			return
		}
	}
	t.Fatal("plugin_content_panes missing from ListAll")
}
