package conversations

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// twoPaneConversations is the surface in its ordinary shape: a visible sidebar
// and a message pane with a session selected to draw in it.
func twoPaneConversations() *Plugin {
	return &Plugin{
		sidebarVisible:  true,
		activePane:      PaneSidebar,
		selectedSession: "session-1",
	}
}

// With the centre open the toggle is a ring: the sidebar is not the wrap point
// going forward, the message pane is, and shift+tab wraps at the other end.
func TestConversationsRingWrapsAtTheMessagePane(t *testing.T) {
	p := twoPaneConversations()
	if p.AtFocusCycleEnd(false) {
		t.Fatal("the sidebar is not the end of the forward cycle; the messages are")
	}
	if !p.AtFocusCycleEnd(true) {
		t.Fatal("the sidebar is where a reverse cycle wraps")
	}

	p.activePane = PaneMessages
	if !p.AtFocusCycleEnd(false) {
		t.Fatal("the message pane is where a forward cycle wraps")
	}
	if p.AtFocusCycleEnd(true) {
		t.Fatal("the message pane is not the start of the ring")
	}
}

// Coming back from the centre lands on the window the toggle resumes at.
func TestConversationsFocusCycleStart(t *testing.T) {
	p := twoPaneConversations()
	p.activePane = PaneMessages
	p.FocusCycleStart(false)
	if p.activePane != PaneSidebar {
		t.Fatalf("forward handback focused %v, want the sidebar", p.activePane)
	}
	p.FocusCycleStart(true)
	if p.activePane != PaneMessages {
		t.Fatalf("reverse handback focused %v, want the message pane", p.activePane)
	}
}

// A pane that is not drawn is not a stop.
func TestConversationsRingSkipsPanesThatAreNotDrawn(t *testing.T) {
	p := twoPaneConversations()
	p.selectedSession = ""
	if !p.AtFocusCycleEnd(false) || !p.AtFocusCycleEnd(true) {
		t.Fatal("with no session selected the sidebar is the whole ring")
	}
}

// Sub-modes keep tab.
func TestConversationsSubModesKeepTab(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Plugin)
	}{
		{"search", func(p *Plugin) { p.searchMode = true }},
		{"filter", func(p *Plugin) { p.filterMode = true }},
		{"content search", func(p *Plugin) { p.contentSearchMode = true }},
		{"resume modal", func(p *Plugin) { p.showResumeModal = true }},
		{"turn detail", func(p *Plugin) { p.detailMode = true }},
		{"analytics", func(p *Plugin) { p.view = ViewAnalytics }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := twoPaneConversations()
			p.activePane = PaneMessages
			tc.apply(p)
			if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
				t.Fatalf("%s offered the centre a tab stop", tc.name)
			}
		})
	}
}

// With the centre closed the shell never asks, and the plugin's own tab handler
// is the exact toggle it always was.
func TestConversationsTabTogglesPanesUnchanged(t *testing.T) {
	p := twoPaneConversations()
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	p.updateSessions(tab)
	if p.activePane != PaneMessages {
		t.Fatalf("tab from the sidebar focused %v, want the message pane", p.activePane)
	}
	p.updateMessages(tab)
	if p.activePane != PaneSidebar {
		t.Fatalf("tab from the message pane focused %v, want the sidebar", p.activePane)
	}
}
