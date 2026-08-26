package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

// A configured resource provider is offered by the global browser's switcher,
// the same as by the project workspace's.
//
// Providers are app-level precisely because they serve both surfaces, and this
// one's preview already places Resource panes — `sidecar open <locator>` has
// opened one here all along. The rows went missing only because they shared a
// flag with Terminal split, which this surface genuinely cannot offer: it holds
// a single terminal producer bound to the selected row. One capability, one
// gate; a passive row must not ride along on it.
func TestGlobalSwitcherOffersConfiguredProviders(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{TerminalResources: config.TerminalResourcesConfig{
		Providers: []config.TerminalResourceProviderConfig{
			{ID: "jira-work", Enabled: true},
			{ID: "linear-eng", Enabled: true},
			{ID: "off-by-config", Enabled: false},
		},
	}}

	m.OpenPaneSwitcher()
	view := ansi.Strip(renderCreateModal(t, m))

	for _, id := range []string{"jira-work", "linear-eng"} {
		if !strings.Contains(view, id) {
			t.Fatalf("global switcher does not offer provider %q:\n%s", id, view)
		}
	}
	if strings.Contains(view, "off-by-config") {
		t.Fatal("global switcher offered a disabled provider")
	}
	if !strings.Contains(view, "Terminal split") {
		t.Fatalf("global switcher did not offer the enabled Terminal split:\n%s", view)
	}
}

func TestGlobalSwitcherHidesTerminalSplitWithWorkspaceFlag(t *testing.T) {
	features.Init(&config.Config{})
	t.Cleanup(func() { features.Init(&config.Config{}) })
	features.SetOverride(features.WorkspaceTerminalPanel.Name, false)
	m := catalogModel(t)
	m.OpenPaneSwitcher()
	if view := ansi.Strip(renderCreateModal(t, m)); strings.Contains(view, "Terminal split") {
		t.Fatalf("global switcher offered disabled Terminal split:\n%s", view)
	}
}

// Picking a provider row resolves its locator against that instance, so the
// modal ends exactly where `sidecar open` begins on this surface too.
func TestGlobalSwitcherResolvesProviderLocator(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{TerminalResources: config.TerminalResourcesConfig{
		Providers: []config.TerminalResourceProviderConfig{{ID: "jira-work", Enabled: true}},
	}}

	m.OpenPaneSwitcher()
	renderCreateModal(t, m)
	m.createForm.SetKind(workspacecreate.KindResource)
	m.createForm.AdvanceToTarget()
	m.createForm.PickerInput().SetValue("PROJ-123")

	target, err := m.createForm.TargetFor("")
	if err != nil {
		t.Fatalf("TargetFor on the global surface: %v", err)
	}
	if target.Provider != "jira-work" || target.Value != "PROJ-123" {
		t.Fatalf("target = %+v, want the locator under jira-work", target)
	}
}
