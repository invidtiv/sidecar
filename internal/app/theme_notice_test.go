package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/state"
)

// isolateNoticeState points both the "has this machine run Sidecar before?"
// witness and the preferences file at temporary directories, and reports the
// state-dir path so a case can decide whether it exists.
func isolateNoticeState(t *testing.T, priorInstall bool) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "sidecar")
	if priorInstall {
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.InitWithDir(t.TempDir()) })
}

// The notice is a status flash, not a stored notification: it is a one-time
// cosmetic heads-up, not something to find again in the centre.
func noticeToast(t *testing.T, cfg *config.Config) (FlashMsg, bool) {
	t.Helper()
	cmd := defaultThemeNoticeCmd(cfg)
	if cmd == nil {
		return FlashMsg{}, false
	}
	flash, ok := cmd().(FlashMsg)
	return flash, ok
}

// A long-time user with no recorded theme is the one being restyled, so they
// are the one who gets told.
func TestDefaultThemeNoticeFiresForARestyledUser(t *testing.T) {
	isolateNoticeState(t, true)
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{}

	toast, ok := noticeToast(t, cfg)
	if !ok {
		t.Fatal("no toast for a user with no recorded theme choice")
	}
	if toast.Text == "" {
		t.Error("the notice carries no message")
	}

	// Once, ever — and the flag is in state.json, never in config.json.
	if !state.GetSeenDefaultThemeNotice() {
		t.Fatal("showing the notice did not record that it had been shown")
	}
	if _, again := noticeToast(t, cfg); again {
		t.Error("the notice fired a second time")
	}
}

// A user who chose a theme is not being restyled and must see nothing.
func TestDefaultThemeNoticeStaysQuietForAnExplicitChoice(t *testing.T) {
	for _, chosen := range []config.ThemeConfig{
		{Name: "nord"},
		{Name: "default"},
		{Community: "catppuccin"},
	} {
		isolateNoticeState(t, true)
		cfg := config.Default()
		cfg.UI.Theme = chosen

		if _, ok := noticeToast(t, cfg); ok {
			t.Errorf("notice fired for a user who chose %+v", chosen)
		}
		if state.GetSeenDefaultThemeNotice() {
			t.Errorf("a user who saw nothing had the notice marked seen (%+v)", chosen)
		}
	}
}

// A genuinely fresh install has no previous look to contrast against. The
// witness is the state directory: absent here, present in the case above.
func TestDefaultThemeNoticeStaysQuietOnAFreshInstall(t *testing.T) {
	isolateNoticeState(t, false)
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{}

	if _, ok := noticeToast(t, cfg); ok {
		t.Fatal("notice fired on a fresh install")
	}
	if state.GetSeenDefaultThemeNotice() {
		t.Error("a fresh install spent the one-time notice without showing it")
	}
}
