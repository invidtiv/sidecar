package configui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

func TestTerminalPageRendersDefaults(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"Interaction", "Exit interactive mode", "Ctrl+\\",
		"Attach to tmux", "Disabled",
		"Opens the full tmux client instead of Sidecar's embedded terminal.",
		"Leave this off unless you rely on tmux's own interface and shortcuts.",
		"Copy selection", "Alt+C", "Paste", "Alt+V", "Copy on select",
		"Capture", "Preview limit", "2 MB",
		"Capture limits are advanced controls; Sidecar uses a safe default.",
		"apply to the next one you open",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Terminal is missing %q:\n%s", want, view)
		}
	}
}

// Without the full-attach feature the chord is cleared for every terminal host,
// so the control must not pretend to be editable.
func TestTerminalAttachForceDisabled(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.InteractiveAttachKey = "ctrl+]"
	})
	m.Open(PageTerminal)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Disabled") {
		t.Fatalf("a configured attach chord was shown as live:\n%s", view)
	}
	if !strings.Contains(view, "Full tmux attach under Feature Flags") {
		t.Fatalf("Terminal did not say why attach is unavailable:\n%s", view)
	}
	for _, c := range m.controls {
		if c.id == regionAttachKey && c.cursor {
			t.Fatal("the disabled attach control is still a cursor stop")
		}
	}

	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.TmuxFullAttach.Name: true}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	view = ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Ctrl+]") {
		t.Fatalf("with the feature on, the attach chord is not shown:\n%s", view)
	}
}

func TestTerminalKeyEditValidatesAndPersists(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	m.View(160, 45)
	m.detailFocus = true

	m.editKey(regionExitKey, "ctrl+\\", func(ws *config.WorkspacePluginConfig, value string) {
		ws.InteractiveExitKey = value
	})
	if m.FocusContext() != ContextConfigEdit {
		t.Fatalf("editing a chord reported context %q", m.FocusContext())
	}
	m.editor.input.SetValue("q")
	if _, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("a bare key was accepted")
	}
	if !m.editing() {
		t.Fatal("a refused value closed the editor")
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "needs a modifier") {
		t.Fatalf("the refusal was not explained:\n%s", view)
	}
	if loadSaved(t).Plugins.Workspace.InteractiveExitKey != "" {
		t.Fatal("a refused value was written anyway")
	}

	m.editor.input.SetValue("ctrl+x")
	_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("a valid chord was not saved")
	}
	reload(t, m, cmd())
	if got := loadSaved(t).Plugins.Workspace.InteractiveExitKey; got != "ctrl+x" {
		t.Fatalf("exit chord saved as %q", got)
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Ctrl+X") {
		t.Fatalf("the saved chord is not on screen:\n%s", view)
	}
}

func TestCopyOnSelectPersists(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	activate(t, m, regionCopyOnSelect)
	if !loadSaved(t).Selection.CopyOnSelect {
		t.Fatal("copy on select did not persist")
	}
}

// A config written before copy-on-select applied to every surface holds it under
// the terminal's own key. The control answers for that key too, and turning the
// setting off has to retire it, or the next load turns it back on.
func TestCopyOnSelectTurnsOffTheKeyItUsedToLiveUnder(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, func(cfg *config.Config) { cfg.Plugins.Workspace.CopyOnSelect = true })
	m.Open(PageTerminal)
	if !copyOnSelect(m.Config()) {
		t.Fatal("the terminal's own copy-on-select key did not reach the control")
	}
	activate(t, m, regionCopyOnSelect)
	saved := loadSaved(t)
	if saved.Selection.CopyOnSelect || saved.Plugins.Workspace.CopyOnSelect {
		t.Fatalf("copy on select survived being turned off: selection=%v terminal=%v",
			saved.Selection.CopyOnSelect, saved.Plugins.Workspace.CopyOnSelect)
	}
}

func TestCaptureLimitStepsAndClamps(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.TmuxCaptureMaxBytes = CaptureLimitMax
	})
	m.Open(PageTerminal)
	choose(t, m, regionCaptureLimit, strconv.Itoa(CaptureLimitChoices[0]))
	if got := loadSaved(t).Plugins.Workspace.TmuxCaptureMaxBytes; got != CaptureLimitChoices[0] {
		t.Fatalf("choosing the smallest value stored %d", got)
	}

	cases := map[int]int{
		0:                    CaptureLimitDefault,
		-1:                   CaptureLimitDefault,
		1:                    CaptureLimitMin,
		CaptureLimitMax * 10: CaptureLimitMax,
		4 * 1024 * 1024:      4 * 1024 * 1024,
	}
	for in, want := range cases {
		if got := ClampCaptureLimit(in); got != want {
			t.Fatalf("ClampCaptureLimit(%d) = %d, want %d", in, got, want)
		}
	}
	if got := FormatCaptureLimit(CaptureLimitMin); got != "256 KB" {
		t.Fatalf("FormatCaptureLimit(min) = %q", got)
	}
	if got := FormatCaptureLimit(2 * 1024 * 1024); got != "2 MB" {
		t.Fatalf("FormatCaptureLimit(2MB) = %q", got)
	}
}

func TestValidateInteractiveKey(t *testing.T) {
	valid := []string{"ctrl+\\", "ctrl+]", "alt+c", "alt+v", "ctrl+shift+p", "super+c", "ctrl+enter", "ctrl+f5", "ctrl++"}
	for _, key := range valid {
		if err := ValidateInteractiveKey(key); err != nil {
			t.Fatalf("ValidateInteractiveKey(%q) = %v, want nil", key, err)
		}
	}
	invalid := []string{"", "q", "Ctrl+C", "meta+c", "ctrl+", "ctrl+ctrl+c", "ctrl+nope"}
	for _, key := range invalid {
		if err := ValidateInteractiveKey(key); err == nil {
			t.Fatalf("ValidateInteractiveKey(%q) accepted an unusable chord", key)
		}
	}
	if got := FormatKeyLabel("ctrl+\\"); got != "Ctrl+\\" {
		t.Fatalf("FormatKeyLabel = %q", got)
	}
	if got := FormatKeyLabel("alt+c"); got != "Alt+C" {
		t.Fatalf("FormatKeyLabel = %q", got)
	}
}
