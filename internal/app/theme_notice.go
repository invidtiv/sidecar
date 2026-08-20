package app

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/theme"
)

// defaultThemeNoticeCmd announces, once ever, that the fresh-install default
// theme changed — but only to the users the change actually restyled.
//
// Everything it does is inside the returned tea.Cmd: a stat of the state
// directory and, at most once in the installation's life, a state.json write.
// Neither belongs on the startup path (AGENTS.md, "Startup Latency"), and
// neither has to happen before the first frame — the toast reads the same one
// tick late.
//
// The decision itself is theme.ShouldAnnounceDefaultChange. This function only
// gathers the three facts it needs.
func defaultThemeNoticeCmd(cfg *config.Config) tea.Cmd {
	if cfg == nil {
		return nil
	}
	global := cfg.UI.Theme
	return func() tea.Msg {
		if !theme.ShouldAnnounceDefaultChange(global, hasPriorInstallState(), state.GetSeenDefaultThemeNotice()) {
			return nil
		}
		// Recorded before the toast is shown rather than after, so a crash or a
		// quit while it is on screen still spends the one showing. A notice
		// that can repeat is worse than one that can be missed.
		_ = state.SetSeenDefaultThemeNotice(true)
		// A one-time cosmetic notice, not an event worth keeping in the
		// centre (audit row 27).
		return FlashMsg{Text: theme.DefaultThemeNotice}
	}
}

// hasPriorInstallState reports whether this machine has run Sidecar before.
//
// config.StateDir() is the right witness: it is created the first time Sidecar
// writes anything about a project (shells, worktree state), so it exists for an
// upgrading user and does not for a fresh install. config.json is not a witness
// — it is absent for exactly the long-time user this notice is for — and the
// preferences state.json is written eagerly enough that by the time the check
// runs it may already exist.
func hasPriorInstallState() bool {
	dir := config.StateDir()
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
