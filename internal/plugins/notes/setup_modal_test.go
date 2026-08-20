package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tdsetup"
)

func setupModalPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{ProjectRoot: t.TempDir(), WorkDir: t.TempDir(), Epoch: 7, Logger: discardLogger()}
	p.store = openTestStore(t)
	p.width, p.height = 120, 34
	p.loadRequestID = 1
	_, _ = p.Update(NotesLoadedMsg{
		Err:       tdsetup.ErrNotInitialized,
		Epoch:     7,
		RequestID: 1,
		Filter:    FilterActive,
	})
	return p
}

func TestUninitializedLoadOpensSetupModalWithHonestCopy(t *testing.T) {
	p := setupModalPlugin(t)
	view := ansi.Strip(p.View(p.width, p.height))
	for _, want := range []string{
		"Set up Notes", ".todos folder", ".gitignore", "AGENTS.md", "CLAUDE.md",
		"Initialize td", "Notes preferences", "Not now",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup modal missing %q:\n%s", want, view)
		}
	}
	if !p.BlocksGlobalKeys() || p.FocusContext() != "notes-setup-modal" {
		t.Fatal("setup modal did not own plugin input")
	}
}

func TestSetupModalFirstKeyInitializes(t *testing.T) {
	p := setupModalPlugin(t)
	p.setupModal = nil // the first key arrives before View
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !p.setupInitializing {
		t.Fatal("first Enter did not start initialization")
	}
}

func TestSetupModalMouseInitializes(t *testing.T) {
	p := setupModalPlugin(t)
	_ = p.View(p.width, p.height)
	x, y := modalRegionPoint(t, p.setupMouseHandler, setupActionInitialize)
	_, cmd := p.Update(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if cmd == nil || !p.setupInitializing {
		t.Fatal("clicking Initialize td did not start the same action as Enter")
	}
}

func TestSetupModalMouseAndKeyboardSharePreferenceAction(t *testing.T) {
	p := setupModalPlugin(t)
	_ = p.View(p.width, p.height)
	x, y := modalRegionPoint(t, p.setupMouseHandler, setupActionPreferences)
	_, mouseCmd := p.Update(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if mouseCmd == nil {
		t.Fatal("mouse preference action returned no command")
	}
	if _, ok := mouseCmd().(app.OpenNotesPreferencesMsg); !ok {
		t.Fatalf("mouse action produced %T", mouseCmd())
	}

	p = setupModalPlugin(t)
	p.ensureSetupModal()
	p.setupModal.Render(p.width, p.height, p.setupMouseHandler)
	p.setupModal.SetFocus(setupActionPreferences)
	_, keyCmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if keyCmd == nil {
		t.Fatal("keyboard preference action returned no command")
	}
	if _, ok := keyCmd().(app.OpenNotesPreferencesMsg); !ok {
		t.Fatalf("keyboard action produced %T", keyCmd())
	}
}

func TestSetupResultFailureStaysActionableAndSuccessReloads(t *testing.T) {
	p := setupModalPlugin(t)
	_, _ = p.Update(tdsetup.ResultMsg{
		Origin: tdsetup.OriginNotes,
		Epoch:  7,
		Err:    errors.New("permission denied"),
	})
	if !p.showSetupModal || p.setupErr == nil {
		t.Fatal("failed setup did not remain open")
	}
	if view := ansi.Strip(p.View(p.width, p.height)); !strings.Contains(view, "permission denied") {
		t.Fatalf("failure is not actionable:\n%s", view)
	}

	_, cmd := p.Update(tdsetup.ResultMsg{Origin: tdsetup.OriginTDMonitor, Epoch: 7})
	if cmd == nil || p.showSetupModal || p.setupNeeded {
		t.Fatal("shared successful setup did not close and reload Notes")
	}
}

func TestNotesInitAndFirstSetupProbeStayAsynchronous(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(root, "td-ran")
	fakeTD := filepath.Join(bin, "td")
	if err := os.WriteFile(fakeTD, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := New()
	if err := p.Init(&plugin.Context{ProjectRoot: root, WorkDir: root, Epoch: 3, Logger: discardLogger()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Init ran td or touched readiness")
	}
	msg, ok := p.loadNotes()().(NotesLoadedMsg)
	if !ok || !errors.Is(msg.Err, tdsetup.ErrNotInitialized) {
		t.Fatalf("async readiness result = %#v", msg)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("readiness probe ran td instead of checking setup")
	}
}
