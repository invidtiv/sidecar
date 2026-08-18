package notes

import (
	"io"
	"log/slog"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestFuzzyScoreIsRuneSafe(t *testing.T) {
	if FuzzyMatchNote("日", Note{Title: "日本語"}) == 0 {
		t.Fatal("query 日 should match 日本語")
	}
	if FuzzyMatchNote("café", Note{Content: "notes about café later"}) == 0 {
		t.Fatal("query café should match content containing café")
	}
	if FuzzyMatchNote("é", Note{Title: "café"}) == 0 {
		t.Fatal("single non-ASCII rune should match")
	}
	if FuzzyMatchNote("xyz", Note{Title: "日本語"}) != 0 {
		t.Fatal("unrelated query should not match")
	}
}

func TestFuzzyScoreCamelCaseBonus(t *testing.T) {
	camel := FuzzyMatchNote("gc", Note{Title: "getConfig"})
	lower := FuzzyMatchNote("gc", Note{Title: "getconfig"})
	if camel == 0 || lower == 0 {
		t.Fatalf("both titles should match, camel=%d lower=%d", camel, lower)
	}
	if camel <= lower {
		t.Fatalf("camelCase title should score higher: camel=%d lower=%d", camel, lower)
	}
}

func TestSearchBackspaceDeletesOneRune(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.editorTextarea = textarea.New()
	p.searchMode = true
	p.searchQuery = "café"
	p.notes = []Note{{ID: "n1", Title: "café", Content: "body"}}
	p.updateFilteredNotes()

	_, _ = p.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.searchQuery != "caf" {
		t.Fatalf("searchQuery = %q, want %q after rune-safe backspace", p.searchQuery, "caf")
	}
	if len(p.filteredNotes) != 1 {
		t.Fatalf("filtered %d notes after backspace, want the remaining café match", len(p.filteredNotes))
	}

	_, _ = p.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	_, _ = p.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	_, _ = p.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.searchQuery != "" {
		t.Fatalf("searchQuery = %q, want empty after deleting remaining runes", p.searchQuery)
	}
}
