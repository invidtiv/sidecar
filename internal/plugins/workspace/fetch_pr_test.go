package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFetchPRFilterAcceptsSpaces(t *testing.T) {
	p := &Plugin{
		viewMode:      ViewModeFetchPR,
		width:         80,
		height:        24,
		fetchPRFilter: "fix",
	}

	p.handleFetchPRKeys(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})

	if got := p.fetchPRFilter; got != "fix " {
		t.Fatalf("fetch PR filter = %q, want %q", got, "fix ")
	}
}
