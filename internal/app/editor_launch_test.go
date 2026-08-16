package app

import (
	"errors"
	"os/exec"
	"strconv"
	"testing"
)

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("shell exited 0, wanted %d", code)
	}
	return err
}

// The profile-loading shell failing to reach the editor is Sidecar's problem to
// paper over; an editor that ran and exited badly is the user's own.
func TestShellLaunchFailedDistinguishesShellFromEditor(t *testing.T) {
	if !shellLaunchFailed(exitErr(t, 127)) {
		t.Fatal("127 (command not found) was not treated as a launch failure")
	}
	if !shellLaunchFailed(exitErr(t, 126)) {
		t.Fatal("126 (could not execute) was not treated as a launch failure")
	}
	if !shellLaunchFailed(&exec.Error{Name: "zsh", Err: errors.New("not found")}) {
		t.Fatal("a shell that could not be started was not treated as a launch failure")
	}
	if shellLaunchFailed(exitErr(t, 1)) {
		t.Fatal("an editor exiting 1 was mistaken for a failed launch")
	}
	if shellLaunchFailed(nil) {
		t.Fatal("a clean exit was treated as a failed launch")
	}
}

func TestEditorFallbackOnlyRetriesOnce(t *testing.T) {
	fallback := []string{"vim", "/tmp/notes.md"}

	if cmd := editorFallbackCmd(EditorReturnedMsg{Err: exitErr(t, 127), Fallback: fallback}); cmd == nil {
		t.Fatal("a failed shell launch did not fall back to the direct exec")
	}
	// The retry itself carries no fallback, so a second failure is reported
	// rather than looping.
	if cmd := editorFallbackCmd(EditorReturnedMsg{Err: exitErr(t, 127)}); cmd != nil {
		t.Fatal("the fallback launch tried to fall back again")
	}
	if cmd := editorFallbackCmd(EditorReturnedMsg{Err: exitErr(t, 1), Fallback: fallback}); cmd != nil {
		t.Fatal("an editor's own nonzero exit triggered a relaunch")
	}
	if cmd := editorFallbackCmd(EditorReturnedMsg{Fallback: fallback}); cmd != nil {
		t.Fatal("a clean editor exit triggered a relaunch")
	}
}
