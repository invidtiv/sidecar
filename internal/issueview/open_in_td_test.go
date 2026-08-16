package issueview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// O belongs to the card, not to each host's key switch: the hosts supply the
// jump, and every surface that supplies one answers the key and advertises it.
func TestOpenInTDHandlerOwnsTheOKey(t *testing.T) {
	m := New(nil)
	m.SetData(sample())
	m.SetSize(80, 20)

	// Without a host capability the key is not the card's to answer.
	if handled, _ := m.HandleKey(tea.KeyPressMsg{Code: 'O', Text: "O"}); handled {
		t.Fatal("O was claimed with no host handler wired")
	}
	m.SetFocused(true)
	if strings.Contains(m.View(), " td") {
		t.Fatalf("an unwired card advertised O:\n%s", m.View())
	}

	var got string
	m.OpenInTDHandler = func(id string) tea.Cmd {
		got = id
		return func() tea.Msg { return nil }
	}

	// Focused but not active: O is about the card, not about walking the epic.
	handled, cmd := m.HandleKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
	if !handled || cmd == nil {
		t.Fatalf("focused card did not answer O: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got != "td-abc123" {
		t.Fatalf("handler received %q, want the card's own issue", got)
	}
	if !strings.Contains(m.View(), "td") {
		t.Fatalf("the ACTIONS row does not offer O:\n%s", m.View())
	}

	// Active with the cursor on a subtask: O opens what is selected.
	m.SetActive(true)
	if _, cmd := m.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}); cmd != nil {
		_ = cmd()
	}
	want := m.SelectedID()
	if want == "td-abc123" {
		t.Fatal("the cursor did not move onto a navigable row")
	}
	if handled, _ := m.HandleKey(tea.KeyPressMsg{Code: 'O', Text: "O"}); !handled {
		t.Fatal("an active card did not answer O")
	}
	if got != want {
		t.Fatalf("handler received %q, want the selected row %q", got, want)
	}
}
