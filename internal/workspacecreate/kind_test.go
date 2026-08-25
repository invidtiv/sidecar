package workspacecreate

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

func TestKindRowStylesHighlightSelectedWithoutFocus(t *testing.T) {
	assertStyle(t, "shell selected", kindRowStyle(KindShell, KindShell, false, false), styles.ButtonFocused)
	assertStyle(t, "worktree idle", kindRowStyle(KindWorktree, KindShell, false, false), styles.Button)

	assertStyle(t, "worktree selected", kindRowStyle(KindWorktree, KindWorktree, false, false), styles.ButtonFocused)
	assertStyle(t, "shell idle", kindRowStyle(KindShell, KindWorktree, false, false), styles.Button)

	assertStyle(t, "shell selected while hovered", kindRowStyle(KindShell, KindShell, false, true), styles.ButtonFocused)
	assertStyle(t, "worktree hover", kindRowStyle(KindWorktree, KindShell, false, true), styles.ButtonHover)
}

func TestKindControlKeepsShellSelectedWhenNameFocused(t *testing.T) {
	f := Open(testOpts(KindShell))
	renderForm(t, f)
	if f.Modal().FocusedID() != FieldName {
		t.Fatalf("focus = %q, want %s", f.Modal().FocusedID(), FieldName)
	}
	if f.Kind() != KindShell {
		t.Fatalf("kind = %v, want shell", f.Kind())
	}
	assertStyle(t, "open-on-name still highlights shell", kindRowStyle(f.Kind(), f.Kind(), false, false), styles.ButtonFocused)
}

func TestKindFrameStyleMatchesInputBorder(t *testing.T) {
	if got, want := kindFrameStyle(true, false).GetForeground(), styles.Primary; got != want {
		t.Fatalf("focused frame = %v, want Primary %v", got, want)
	}
	if got, want := kindFrameStyle(true, true).GetForeground(), styles.Primary; got != want {
		t.Fatalf("focused+hovered frame = %v, want Primary still %v", got, want)
	}
	if got, want := kindFrameStyle(false, true).GetForeground(), styles.TextMuted; got != want {
		t.Fatalf("hovered frame = %v, want TextMuted %v", got, want)
	}
	if got, want := kindFrameStyle(false, false).GetForeground(), styles.BorderNormal; got != want {
		t.Fatalf("idle frame = %v, want BorderNormal %v", got, want)
	}
}

func TestKindToggleFrameIsIdleWhenNameFocused(t *testing.T) {
	rows := kindRowsFor(false)
	idle := renderKindToggle(rows, KindShell, false, false, nil, 80)
	focused := renderKindToggle(rows, KindShell, true, false, nil, 80)
	if idle == focused {
		t.Fatal("focused and idle toggles rendered identically")
	}
	if !containsStyle(idle, kindFrameStyle(false, false), kindFrameOpen) {
		t.Fatalf("idle toggle missing BorderNormal %q:\n%s", kindFrameOpen, idle)
	}
	if !containsStyle(focused, kindFrameStyle(true, false), kindFrameOpen) {
		t.Fatalf("focused toggle missing Primary %q:\n%s", kindFrameOpen, focused)
	}
	if !containsStyle(idle, styles.ButtonFocused, " Shell ") {
		t.Fatalf("idle toggle dropped the selected Shell pill:\n%s", idle)
	}
}

func TestKindSpansAccountForFrame(t *testing.T) {
	spans := kindSpans(kindRowsFor(false))
	if spans[0][0] != len(kindFrameOpen) {
		t.Fatalf("first span starts at %d, want after %q", spans[0][0], kindFrameOpen)
	}
}

func containsStyle(haystack string, style lipgloss.Style, needle string) bool {
	return strings.Contains(haystack, style.Render(needle))
}

func assertStyle(t *testing.T, name string, got, want lipgloss.Style) {
	t.Helper()
	if fmt.Sprint(got.GetBackground()) != fmt.Sprint(want.GetBackground()) || got.GetBold() != want.GetBold() {
		t.Fatalf("%s: background/bold = %v/%v, want %v/%v",
			name, got.GetBackground(), got.GetBold(), want.GetBackground(), want.GetBold())
	}
}
