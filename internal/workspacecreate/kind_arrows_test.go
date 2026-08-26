package workspacecreate

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
)

// Every row of the kind list reaches the same right edge. The rows are drawn
// with a filled background, so a row sized to its own text leaves the list
// with an edge whose shape is an accident of the longest description.
func TestKindListRowsFillTheContentColumn(t *testing.T) {
	rows := kindRowsForOpts(rowOpts{allowTerminalSplit: true, showNotes: true})
	const contentWidth = 64
	for _, line := range strings.Split(renderKindList(rows, 0, false, false, nil, contentWidth), "\n") {
		if got := ansi.StringWidth(line); got != contentWidth {
			t.Fatalf("row width = %d, want the full content column %d: %q", got, contentWidth, ansi.Strip(line))
		}
	}
}

// A disabled row is muted text on the list's own fill, not bare text: the
// list is one block, and a row that dropped the fill would read as a hole in
// it rather than as a choice that is unavailable.
func TestDisabledKindRowKeepsTheListFill(t *testing.T) {
	if got, want := kindRowStyle(true, false, false).GetBackground(), styles.Button.GetBackground(); got != want {
		t.Fatalf("disabled row background = %v, want the list's own %v", got, want)
	}
	if got, want := kindRowStyle(true, false, false).GetForeground(), styles.TextMuted; got != want {
		t.Fatalf("disabled row foreground = %v, want muted %v", got, want)
	}

	const reason = "Two terminals are already on screen — close one first"
	rows := kindRowsForOpts(rowOpts{allowTerminalSplit: true, showNotes: true})
	disabled := func(k Kind) string {
		if k == KindTerminalSplit {
			return reason
		}
		return ""
	}
	const contentWidth = 64
	for _, line := range strings.Split(renderKindList(rows, 0, false, false, disabled, contentWidth), "\n") {
		if got := ansi.StringWidth(line); got != contentWidth {
			t.Fatalf("row width = %d, want %d even with a disabled row: %q", got, contentWidth, ansi.Strip(line))
		}
	}
}

// The kind step is steerable with arrows alone: open, arrow down to the row,
// Enter. Up/down reach the list from the Name field the modal opens on, with
// no Shift+Tab first.
func TestArrowsSteerKindListFromNameField(t *testing.T) {
	f := Open(switcherOpts(KindShell))
	renderForm(t, f)
	if f.Modal().FocusedID() != FieldName {
		t.Fatalf("focus = %q, want the modal to open on %s", f.Modal().FocusedID(), FieldName)
	}

	if action, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}); action != "" {
		t.Fatalf("down produced action %q, want it consumed by the list", action)
	}
	if f.Kind() != KindWorktree {
		t.Fatalf("kind after one down = %v, want worktree", f.Kind())
	}
	renderForm(t, f)
	if got := f.Modal().FocusedID(); got != FieldName {
		t.Fatalf("focus after steering = %q, want it left on %s", got, FieldName)
	}

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if f.Kind() != KindShell {
		t.Fatalf("kind after up = %v, want shell again", f.Kind())
	}

	// The ends hold rather than wrap: a jump from the first row to the last
	// reads as a lost keypress.
	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if f.Kind() != KindShell {
		t.Fatalf("up at the top wrapped to %v, want shell held", f.Kind())
	}
}

// A field that gives up/down a meaning of its own keeps them. The Agent combo
// moves its dropdown with the arrows, so the kind list must not take them.
func TestArrowsStayWithTheAgentCombo(t *testing.T) {
	f := Open(switcherOpts(KindShell))
	m := f.Build(70)
	m.Render(100, 40, mouse.NewHandler())
	m.SetFocus(FieldAgent)

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if f.Kind() != KindShell {
		t.Fatalf("kind = %v, want shell: the combo owns up/down", f.Kind())
	}
}

// The picker step's own list owns up/down; there is no kind list on screen to
// steer, and stealing the keys there would strand the cursor on row one.
func TestArrowsStayWithThePickerList(t *testing.T) {
	f := Open(switcherOpts(KindFile))
	f.SetFileCandidates([]string{"a.go", "b.go", "c.go"})
	f.AdvanceToTarget()
	renderForm(t, f)

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if f.picker.cursor != 1 {
		t.Fatalf("picker cursor = %d, want 1: the picker list owns up/down", f.picker.cursor)
	}
	if f.Kind() != KindFile {
		t.Fatalf("kind = %v, want file: the kind list is not on screen", f.Kind())
	}
}

// The hint line says the gesture — it is the one thing on this modal nothing
// else announces — and drops Tab, which a user already expects of a modal,
// before it would wrap a narrow box onto a second line.
func TestKindStepHintAnnouncesArrows(t *testing.T) {
	f := Open(switcherOpts(KindShell))
	view := ansi.Strip(f.Build(70).Render(100, 40, mouse.NewHandler()))
	if !strings.Contains(view, "↑↓ type · Tab to switch · Enter to confirm") {
		t.Fatalf("hint does not announce the arrows:\n%s", view)
	}
	for _, width := range []int{52, 60, 70, 90} {
		hint := kindStepHint(width, "Enter to confirm · Esc to cancel")
		if got := ansi.StringWidth(hint); got > width-6 {
			t.Fatalf("hint at width %d is %d wide and would wrap the box: %q", width, got, hint)
		}
		if !strings.Contains(hint, "↑↓") {
			t.Fatalf("hint at width %d dropped the arrows: %q", width, hint)
		}
	}
}
