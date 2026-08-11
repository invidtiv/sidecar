package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
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
