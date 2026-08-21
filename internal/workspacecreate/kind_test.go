package workspacecreate

import (
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

func TestKindButtonStylesHighlightSelectedWithoutFocus(t *testing.T) {
	shell, tree := kindButtonStyles(KindShell, false)
	assertStyle(t, "shell selected", shell, styles.ButtonFocused)
	assertStyle(t, "worktree idle", tree, styles.Button)

	shell, tree = kindButtonStyles(KindWorktree, false)
	assertStyle(t, "worktree selected", tree, styles.ButtonFocused)
	assertStyle(t, "shell idle", shell, styles.Button)

	shell, tree = kindButtonStyles(KindShell, true)
	assertStyle(t, "shell selected while hovered", shell, styles.ButtonFocused)
	assertStyle(t, "worktree hover", tree, styles.ButtonHover)
}

func TestKindToggleKeepsShellSelectedWhenNameFocused(t *testing.T) {
	f := Open(testOpts(KindShell))
	renderForm(t, f)
	if f.Modal().FocusedID() != FieldName {
		t.Fatalf("focus = %q, want %s", f.Modal().FocusedID(), FieldName)
	}
	if f.Kind() != KindShell {
		t.Fatalf("kind = %v, want shell", f.Kind())
	}
	shell, _ := kindButtonStyles(f.Kind(), false)
	assertStyle(t, "open-on-name still highlights shell", shell, styles.ButtonFocused)
}

func assertStyle(t *testing.T, name string, got, want lipgloss.Style) {
	t.Helper()
	if fmt.Sprint(got.GetBackground()) != fmt.Sprint(want.GetBackground()) || got.GetBold() != want.GetBold() {
		t.Fatalf("%s: background/bold = %v/%v, want %v/%v",
			name, got.GetBackground(), got.GetBold(), want.GetBackground(), want.GetBold())
	}
}
