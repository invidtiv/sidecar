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

func TestListSlashStillSearchesNotes(t *testing.T) {
	p := layoutTestPlugin(t, "alpha body", "beta body")
	p.activePane = PaneList
	p.previewMode = true
	_, _ = p.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !p.searchMode || p.noteSearchMode {
		t.Fatalf("list / searchMode=%v noteSearchMode=%v", p.searchMode, p.noteSearchMode)
	}
}

func TestPreviewSlashSearchesInsideNote(t *testing.T) {
	p := layoutTestPlugin(t, "hello world\nhello again")
	p.activePane = PaneEditor
	p.previewMode = true
	p.markdownView = false
	p.ensureViewSurface()

	_, _ = p.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if p.searchMode || !p.noteSearchMode {
		t.Fatalf("preview / searchMode=%v noteSearchMode=%v", p.searchMode, p.noteSearchMode)
	}

	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if p.noteSearchQuery != "he" || len(p.noteSearchMatches) != 2 {
		t.Fatalf("query=%q matches=%d, want he / 2", p.noteSearchQuery, len(p.noteSearchMatches))
	}

	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !p.noteSearchCommitted {
		t.Fatal("enter did not commit in-note search")
	}
	first := p.noteSearchCursor
	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if p.noteSearchCursor == first {
		t.Fatal("n did not advance match")
	}
	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: 'N', Text: "N"})
	if p.noteSearchCursor != first {
		t.Fatalf("N cursor = %d, want %d", p.noteSearchCursor, first)
	}

	highlighted := p.highlightNoteSearchLine(p.noteSearchMatches[0].Line, p.viewSurface.Lines[p.noteSearchMatches[0].Line])
	if highlighted == p.viewSurface.Lines[p.noteSearchMatches[0].Line] {
		t.Fatal("current match was not highlighted")
	}

	_, _ = p.handleNoteSearchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.noteSearchMode || p.noteSearchQuery != "" {
		t.Fatalf("esc left search active: mode=%v query=%q", p.noteSearchMode, p.noteSearchQuery)
	}
}
