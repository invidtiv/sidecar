package app

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/features"
)

// A machine registered in Configuration has to be watched without a restart.
//
// The save path already calls overview.SyncHosts, which reconciles the registry
// from the configuration that was just written; this asserts it rather than
// trusting it, because "the save reloads everything" was true of the theme and
// the notification policy long before it was true of the host registry.
//
// The host is registered switched off deliberately. A disabled entry is
// reconciled exactly like any other — it gets a row and a health state — but it
// gets no client, so this test never spawns an ssh child.
func TestSavingAHostRegistersItWithoutARestart(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)

	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.SidecarRemoteHosts.Name: true}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	features.Init(cfg)

	if len(m.overview.HostConditions()) != 0 {
		t.Fatalf("the registry started with hosts: %+v", m.overview.HostConditions())
	}

	if _, err := config.AddHost(config.HostConfig{ID: "book", Target: "marcusbook", Disabled: true}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	// Exactly what Configuration emits after a write.
	m.applyConfigSaved(configui.ConfigSavedMsg{Notice: "Added host: book"})

	conditions := m.overview.HostConditions()
	if len(conditions) != 1 || conditions[0].ID != "book" {
		t.Fatalf("the saved host did not reach the registry: %+v", conditions)
	}

	// And the Configuration surface reads that same health rather than probing.
	remotes := m.configRemoteHosts()
	if len(remotes) != 1 || remotes[0].ID != "book" || remotes[0].State != string(conditions[0].Health.State) {
		t.Fatalf("Configuration's view of the registry = %+v, want the registry's own", remotes)
	}
}
