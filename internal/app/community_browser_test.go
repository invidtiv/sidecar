package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/styles"
)

func TestBuildUnifiedThemeList(t *testing.T) {
	entries := buildUnifiedThemeList()
	themeCount := len(styles.ListThemes())

	if len(entries) != themeCount {
		t.Errorf("expected %d entries, got %d", themeCount, len(entries))
	}

	for i := 0; i < themeCount; i++ {
		if !entries[i].IsBuiltIn {
			t.Errorf("entry %d should be built-in", i)
		}
	}
}

func TestFilterThemeEntries(t *testing.T) {
	entries := buildUnifiedThemeList()

	// Filter for a theme
	filtered := filterThemeEntries(entries, "dracula")
	found := false
	for _, e := range filtered {
		if e.ThemeKey == "dracula" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find dracula in filtered results")
	}

	// Empty query returns all
	all := filterThemeEntries(entries, "")
	if len(all) != len(entries) {
		t.Errorf("empty filter: expected %d, got %d", len(entries), len(all))
	}

	// No matches
	none := filterThemeEntries(entries, "zzz-nonexistent-theme-xyz")
	if len(none) != 0 {
		t.Errorf("expected 0 matches, got %d", len(none))
	}
}

func TestUnifiedThemeCursorNavigation(t *testing.T) {
	var m Model
	m.width = 80
	m.height = 40
	m.initThemeSwitcher()

	if len(m.themeSwitcherFiltered) == 0 {
		t.Fatal("expected themes to be available")
	}

	if len(m.themeSwitcherFiltered) != len(styles.ListThemes()) {
		t.Errorf("expected %d themes, got %d", len(styles.ListThemes()), len(m.themeSwitcherFiltered))
	}
}
