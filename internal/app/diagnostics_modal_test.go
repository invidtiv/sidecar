package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/version"
)

type dummyDiagPlugin struct {
	id   string
	name string
}

func (p dummyDiagPlugin) ID() string                 { return p.id }
func (p dummyDiagPlugin) Name() string               { return p.name }
func (p dummyDiagPlugin) Icon() string               { return "" }
func (p dummyDiagPlugin) Init(*plugin.Context) error { return nil }
func (p dummyDiagPlugin) Start() tea.Cmd             { return nil }
func (p dummyDiagPlugin) Stop()                      {}
func (p dummyDiagPlugin) View(int, int) string       { return "" }
func (p dummyDiagPlugin) IsFocused() bool            { return false }
func (p dummyDiagPlugin) SetFocused(bool)            {}
func (p dummyDiagPlugin) Commands() []plugin.Command { return nil }
func (p dummyDiagPlugin) FocusContext() string       { return "" }
func (p dummyDiagPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	return p, nil
}

func TestDiagnosticsModalRenderingAndInteractions(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	km := keymap.NewRegistry()
	keymap.RegisterDefaults(km)
	pCtx := &plugin.Context{WorkDir: "/test/project", Keymap: km}
	reg := plugin.NewRegistry(pCtx)
	_ = reg.Register(dummyDiagPlugin{id: "td", name: "td"})
	_ = reg.Register(dummyDiagPlugin{id: "git-status", name: "Git"})
	_ = reg.Register(dummyDiagPlugin{id: "file-browser", name: "Files"})
	_ = reg.Register(dummyDiagPlugin{id: "workspace", name: "Workspaces"})
	_ = reg.Register(dummyDiagPlugin{id: "notes", name: "Notes"})

	cfg := &config.Config{}
	m := New(reg, km, cfg, "dev", "/test/project", "/test/project", "")
	m.width = 100
	m.height = 40
	m.showDiagnostics = true
	rendered := m.renderDiagnosticsModal("")

	// 1. Plain word "Sidecar" above logo is removed (modal title is empty)
	if strings.Contains(rendered, "Sidecar\n\n") {
		t.Errorf("rendered diagnostics modal contains plain title 'Sidecar'")
	}

	// 2. Logo graphic is present
	if !strings.Contains(rendered, "_____") {
		t.Errorf("rendered diagnostics modal missing logo graphic")
	}

	// 3. Plugins listed with checkboxes in table, without ": active", issue counts, or "agenda view"
	if strings.Contains(rendered, ": active") {
		t.Errorf("rendered diagnostics modal should not contain ': active'")
	}
	if strings.Contains(rendered, "agenda view") {
		t.Errorf("rendered diagnostics modal should not contain 'agenda view'")
	}
	if !strings.Contains(rendered, "✓") || !strings.Contains(rendered, "td") || !strings.Contains(rendered, "Git") {
		t.Errorf("rendered diagnostics modal missing plugin entries")
	}

	// 4. "Press ! or esc to close" is removed
	if strings.Contains(rendered, "Press ! or esc to close") {
		t.Errorf("rendered diagnostics modal should not contain 'Press ! or esc to close'")
	}

	// The old "Press u to view details and update" instruction line is gone;
	// the [u] Update chip supersedes it.
	if strings.Contains(rendered, "view details") {
		t.Errorf(`rendered diagnostics modal should not contain the "Press u to view details" instruction`)
	}

	// 5. Close button is present
	if !strings.Contains(rendered, "Close") {
		t.Errorf("rendered diagnostics modal missing 'Close' button")
	}

	// Refresh time lives here, not in the application footer.
	if !strings.Contains(rendered, "Refresh:") {
		t.Errorf("diagnostics modal missing last-refresh time")
	}

	// 6. Test Mouse Click on Close button
	regions := m.diagnosticsMouseHandler.HitMap.Regions()
	var closeRegion *mouse.Region
	for _, r := range regions {
		if r.ID == "close" {
			closeRegion = &r
			break
		}
	}
	if closeRegion == nil {
		t.Fatal("close button region not found in diagnostics mouse handler")
	}

	clickMsg := tea.MouseClickMsg{
		X:      closeRegion.Rect.X + 1,
		Y:      closeRegion.Rect.Y,
		Button: tea.MouseLeft,
	}
	updated, _ := m.Update(clickMsg)
	model := updated.(*Model)
	if model.showDiagnostics {
		t.Errorf("expected showDiagnostics to be false after clicking Close button")
	}

	// 7. Test Tab + Enter closes modal
	m.showDiagnostics = true
	m.clearDiagnosticsModal()
	_ = m.renderDiagnosticsModal("")

	// Send Tab key to focus Close button
	tabUpdated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = tabUpdated.(*Model)
	// Send Enter to trigger Close button action
	enterUpdated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = enterUpdated.(*Model)
	if model.showDiagnostics {
		t.Errorf("expected showDiagnostics to be false after Tab + Enter on Close button")
	}
}

// Bare Enter is a primary action, never a silent one: with an update
// available it opens the updater exactly like pressing u; with nothing to
// update it puts the modal away.
func TestDiagnosticsBareEnterFollowsAvailability(t *testing.T) {
	setup := func() *Model {
		km := keymap.NewRegistry()
		keymap.RegisterDefaults(km)
		cfg := &config.Config{}
		m := New(plugin.NewRegistry(&plugin.Context{WorkDir: "/t", Keymap: km}), km, cfg, "dev", "/t", "/t", "")
		m.width = 100
		m.height = 40
		m.showDiagnostics = true
		return &m
	}

	withUpdate := setup()
	withUpdate.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}
	_ = withUpdate.renderDiagnosticsModal("")
	updated, _ := withUpdate.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model := updated.(*Model)
	if model.updateModalState != UpdateModalPreview {
		t.Errorf("bare Enter with an update available must open the updater, got phase %v", model.updateModalState)
	}
	if model.showDiagnostics {
		t.Error("opening the updater must put the diagnostics modal away")
	}

	nothing := setup()
	nothing.needsRestart = true // restart pending, nothing left to update
	_ = nothing.renderDiagnosticsModal("")
	updated, _ = nothing.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.(*Model).showDiagnostics {
		t.Error("bare Enter with only Close offered must close the modal")
	}
}
