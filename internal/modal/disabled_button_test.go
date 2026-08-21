package modal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A disabled button is inert in every direction a button can be reached:
// it registers no focusable (so Tab passes over it and a click finds no
// target) and it answers no action to Enter.
func TestDisabledButtonIsUnfocusableAndInert(t *testing.T) {
	section := Buttons(
		Btn(" Create ", "create", BtnDisabled()),
		Btn(" Cancel ", "cancel"),
	)
	rendered := section.Render(40, "create", "create")
	for _, f := range rendered.Focusables {
		if f.ID == "create" {
			t.Fatal("a disabled button registered a focusable")
		}
	}
	if len(rendered.Focusables) != 1 || rendered.Focusables[0].ID != "cancel" {
		t.Fatalf("focusables = %+v, want only the enabled button", rendered.Focusables)
	}
	if rendered.Content == "" {
		t.Fatal("a disabled button is still drawn, muted")
	}
}

// The enabled button next to it keeps working, so disabling one is not
// disabling the row.
func TestEnabledButtonBesideADisabledOneStillActs(t *testing.T) {
	m := New("t", WithWidth(40)).AddSection(Buttons(
		Btn(" Create ", "create", BtnDisabled()),
		Btn(" Cancel ", "cancel"),
	))
	m.SetFocus("cancel")
	action, _ := m.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != "cancel" {
		t.Fatalf("action = %q, want cancel", action)
	}
	// Tab never reaches the disabled button, and neither does a click: it
	// registers no focusable at all. (Enter's modal-wide primary-action
	// fallback is a separate path; hosts refuse there.)
	for i := 0; i < 6; i++ {
		m.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		if id := m.FocusedID(); id == "create" {
			t.Fatal("Tab focused a disabled button")
		}
	}
}
