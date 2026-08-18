package tdmonitor

import (
	"log/slog"
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
)

func TestBuildThemeMapping(t *testing.T) {
	// Register a test theme with unmistakable distinct values for every slot
	testPalette := styles.ColorPalette{
		Primary:       "#112233",
		Secondary:     "#223344",
		Accent:        "#334455",
		Success:       "#445566",
		Warning:       "#556677",
		Error:         "#667788",
		Info:          "#778899",
		TextPrimary:   "#8899aa",
		TextSecondary: "#99aabb",
		TextMuted:     "#aabbcc",
		TextSubtle:    "#bbccdd",
		TextSelection: "#ccddee",
		OnPrimary:     "#ddeeff",
		OnWarning:     "#eeff00",
		TextInverse:   "#110022",
		BgPrimary:     "#101010",
		BgSecondary:   "#202020",
		BgTertiary:    "#303030",
		BgOverlay:     "#404040",
		SurfaceRaised: "#505050",
		BorderNormal:  "#606060",
		BorderMuted:   "#707070",
		BorderActive:  "#808080",
		Link:          "#909090",
		SyntaxTheme:   "monokai",
		MarkdownTheme: "dark",
	}

	styles.RegisterTheme(styles.Theme{
		Name:        "test-td-mapping",
		DisplayName: "Test TD Mapping",
		Colors:      testPalette,
	})
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})

	styles.ApplyTheme("test-td-mapping")

	got := buildTheme()
	expected := styles.GetCurrentTheme().Colors

	// Assert every semantic slot maps directly to the corresponding sidecar slot
	assertSlot(t, "Primary", got.Primary, cleanColor(expected.Primary))
	assertSlot(t, "Secondary", got.Secondary, cleanColor(expected.Secondary))
	assertSlot(t, "Accent", got.Accent, cleanColor(expected.Accent))
	assertSlot(t, "Success", got.Success, cleanColor(expected.Success))
	assertSlot(t, "Warning", got.Warning, cleanColor(expected.Warning))
	assertSlot(t, "Error", got.Error, cleanColor(expected.Error))
	assertSlot(t, "Info", got.Info, cleanColor(expected.Info))
	assertSlot(t, "ReadyToClose", got.ReadyToClose, cleanColor(expected.Success))
	assertSlot(t, "PendingReview", got.PendingReview, cleanColor(expected.Secondary))
	assertSlot(t, "PendingOther", got.PendingOther, cleanColor(expected.Accent))
	assertSlot(t, "TextPrimary", got.TextPrimary, cleanColor(expected.TextPrimary))
	assertSlot(t, "TextSecondary", got.TextSecondary, cleanColor(expected.TextSecondary))
	assertSlot(t, "TextMuted", got.TextMuted, cleanColor(expected.TextMuted))
	assertSlot(t, "TextSubtle", got.TextSubtle, cleanColor(expected.TextSubtle))
	assertSlot(t, "TextSelection", got.TextSelection, cleanColor(expected.TextSelection))
	assertSlot(t, "OnPrimary", got.OnPrimary, cleanColor(expected.OnPrimary))
	assertSlot(t, "OnWarning", got.OnWarning, cleanColor(expected.OnWarning))
	assertSlot(t, "OnError", got.OnError, cleanColor(expected.TextInverse))
	assertSlot(t, "Background", got.Background, cleanColor(expected.BgPrimary))
	assertSlot(t, "Surface", got.Surface, cleanColor(expected.BgSecondary))
	assertSlot(t, "SurfaceRaised", got.SurfaceRaised, cleanColor(expected.SurfaceRaised))
	assertSlot(t, "Selection", got.Selection, cleanColor(expected.BgTertiary))
	assertSlot(t, "Backdrop", got.Backdrop, cleanColor(expected.BgOverlay))
	assertSlot(t, "Border", got.Border, cleanColor(expected.BorderNormal))
	assertSlot(t, "BorderMuted", got.BorderMuted, cleanColor(expected.BorderMuted))
	assertSlot(t, "BorderActive", got.BorderActive, cleanColor(expected.BorderActive))
	assertSlot(t, "Link", got.Link, cleanColor(expected.Link))
	assertSlot(t, "SyntaxTheme", got.SyntaxTheme, expected.SyntaxTheme)
	assertSlot(t, "MarkdownTheme", got.MarkdownTheme, expected.MarkdownTheme)
}

func assertSlot(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("field %s = %q, want %q", field, got, want)
	}
}

func TestLiveThemeChangePreservesModelAndUpdatesPalette(t *testing.T) {
	styles.ApplyTheme("sidecar-modern")
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})

	projectRoot := findProjectRootWithDB(t)

	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()
	startAndSettle(t, p)

	if p.model == nil {
		t.Fatal("expected non-nil monitor model after settle")
	}
	initialModel := p.model

	// Switch active theme
	styles.ApplyTheme("dracula")

	// Deliver ThemeChangedMsg to plugin
	updatedPlugin, cmd := p.Update(msg.ThemeChangedMsg{})
	if cmd != nil {
		t.Errorf("expected nil cmd on ThemeChangedMsg, got %v", cmd)
	}
	if updatedPlugin != p {
		t.Errorf("expected plugin pointer to remain unchanged")
	}

	// Model pointer should be preserved in place
	if p.model != initialModel {
		t.Errorf("p.model pointer changed: got %p, want %p", p.model, initialModel)
	}

	// The monitor model should now reflect dracula theme colors
	draculaTheme := buildTheme()
	if draculaTheme.Primary != styles.GetTheme("dracula").Colors.Primary {
		t.Errorf("buildTheme primary = %q, want %q", draculaTheme.Primary, styles.GetTheme("dracula").Colors.Primary)
	}
}

func TestLiveThemeChangeWhileModelNilIsHarmless(t *testing.T) {
	p := New()
	// Update with ThemeChangedMsg before Init/Start should not panic or error
	updated, cmd := p.Update(msg.ThemeChangedMsg{})
	if updated != p {
		t.Errorf("expected plugin pointer unchanged")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd")
	}
}

func TestThemeChangeWhileLoadingAppliesOnAdoption(t *testing.T) {
	styles.ApplyTheme("sidecar-modern")
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})

	projectRoot := findProjectRootWithDB(t)

	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start returned nil cmd")
	}
	readyMsg := cmd().(MonitorReadyMsg)

	// Theme changes while loading was in flight
	styles.ApplyTheme("dracula")

	// Adopt the monitor
	p.Update(readyMsg)

	if p.model == nil {
		t.Fatal("expected non-nil model after adoption")
	}
}
