package workspace

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestRenameShellPersistenceFailureRetainsModalAndMemory(t *testing.T) {
	shell := &ShellSession{TmuxName: "sidecar-sh-sidecar-1", Name: "old context"}
	input := textinput.New()
	input.SetValue("new context")
	p := &Plugin{
		viewMode:           ViewModeRenameShell,
		renameShellSession: shell,
		renameShellInput:   input,
		shells:             []*ShellSession{shell},
		shellManifest: &ShellManifest{
			Version: manifestVersion,
			Shells:  []ShellDefinition{{TmuxName: shell.TmuxName, DisplayName: shell.Name}},
			path:    filepath.Join(t.TempDir(), "missing", "shells.json"),
		},
	}
	cmd := p.executeRenameShell()
	if cmd == nil {
		t.Fatal("valid rename returned no persistence command")
	}
	msg, ok := cmd().(RenameShellDoneMsg)
	if !ok || msg.Err == nil {
		t.Fatalf("rename result = %#v, want persistence error", msg)
	}
	p.update(msg)
	if p.viewMode != ViewModeRenameShell || p.renameShellSession != shell {
		t.Fatal("persistence failure dismissed the rename modal")
	}
	if shell.Name != "old context" {
		t.Fatalf("persistence failure changed in-memory name to %q", shell.Name)
	}
	if !strings.Contains(p.renameShellError, "lock shell manifest") {
		t.Fatalf("modal error = %q", p.renameShellError)
	}
}

func TestRenameShellModalUsesSharedValidation(t *testing.T) {
	// textinput itself filters control characters and invalid UTF-8 before the
	// modal sees them; empty and byte-length validation still cross this seam.
	for _, name := range []string{"", strings.Repeat("a", 51), strings.Repeat("é", 26)} {
		t.Run(name, func(t *testing.T) {
			input := textinput.New()
			input.SetValue(name)
			p := &Plugin{renameShellSession: &ShellSession{TmuxName: "sidecar-sh-one"}, renameShellInput: input}
			if cmd := p.executeRenameShell(); cmd != nil {
				t.Fatal("invalid modal name scheduled persistence")
			}
			_, sharedErr := shellstate.NormalizeName(name)
			if sharedErr == nil || p.renameShellError != sharedErr.Error() {
				t.Fatalf("modal error = %q, shared error = %v", p.renameShellError, sharedErr)
			}
		})
	}
}

// Two shells may carry the same display name, so only the delete confirmation —
// the irreversible one — spends a line on the session that identifies them.
func TestOnlyTheDeleteConfirmationNamesTheSession(t *testing.T) {
	shell := &ShellSession{TmuxName: "sidecar-sh-sidecar-2", Name: "backend"}
	p := &Plugin{deleteConfirmShell: shell, renameShellSession: shell}

	deleteInfo := ansi.Strip(p.deleteShellInfoSection().Render(50, "", "").Content)
	if !strings.Contains(deleteInfo, "backend") || !strings.Contains(deleteInfo, shell.TmuxName) {
		t.Fatalf("delete confirmation = %q, want the display name and the session that disambiguates it", deleteInfo)
	}
	renameInfo := ansi.Strip(p.renameShellInfoSection().Render(50, "", "").Content)
	if strings.Contains(renameInfo, shell.TmuxName) {
		t.Fatalf("rename modal = %q, must not show the tmux session name", renameInfo)
	}
}

// Acknowledgement requires dwell so that arrowing through the list does not
// silently clear completions the user never actually read.
func TestDwellSatisfied(t *testing.T) {
	now := time.Now()
	p := &Plugin{}
	if !p.dwellSatisfied(now) {
		t.Fatalf("a selection that never changed should already count as read")
	}
	p.selectionSince = now
	if p.dwellSatisfied(now.Add(AckDwell / 2)) {
		t.Fatalf("passing selection should not acknowledge")
	}
	if !p.dwellSatisfied(now.Add(AckDwell + time.Millisecond)) {
		t.Fatalf("held selection should acknowledge")
	}
}
