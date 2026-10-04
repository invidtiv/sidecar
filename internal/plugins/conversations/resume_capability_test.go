package conversations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestResumeRenderingAndActionsDoNotProbeOnUpdate(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "help-probes")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" >> '" + marker + "'\nprintf '%s' '--no-daemon'\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := New()
	p.ctx = &plugin.Context{WorkDir: dir}
	p.sessions = []adapter.Session{{ID: "session-id", AdapterID: "codex", AdapterName: "Codex"}}
	p.selectedSession = "session-id"
	for i := 0; i < 3; i++ {
		if header := p.renderMainPane(100, 20); !strings.Contains(header, "codex resume session-id") {
			t.Fatalf("render omitted resume preview: %q", header)
		}
	}
	copyCommand := p.yankResumeCommand()
	if copyCommand == nil {
		t.Fatal("resume command unavailable")
	}
	p.openResumeModal()
	if !p.showResumeModal {
		t.Fatal("pure eligibility check refused the provider")
	}
	resumeCommand := p.executeResume()
	if resumeCommand == nil {
		t.Fatal("resume action unavailable")
	}
	_ = resumeCommand() // target resolution belongs to the destination workspace
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("render or constructing actions probed the provider: %v", err)
	}
	clip.ResetRecent()
	t.Cleanup(clip.ResetRecent)
	_ = copyCommand() // resolve off Update; do not execute the clipboard command tree
	if copied, ok := clip.LastCopied(); !ok || copied != "codex --no-daemon resume session-id" {
		t.Fatalf("copied command did not resolve installed isolation: %q, %v", copied, ok)
	}
	probes, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(probes)) != dir {
		t.Fatalf("copy probe did not use captured destination: %q, %v", probes, err)
	}
}

func TestCopiedResumeUsesSelectedWorktreeInsteadOfMainProject(t *testing.T) {
	dir, worktree := t.TempDir(), t.TempDir()
	marker := filepath.Join(dir, "help-probes")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" > '" + marker + "'\nif [ \"$PWD\" = '" + worktree + "' ]; then printf '%s' '--no-daemon'; else printf '%s' 'older usage'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := New()
	p.ctx = &plugin.Context{WorkDir: dir}
	p.sessions = []adapter.Session{{ID: "session-id", AdapterID: "codex", WorktreePath: worktree}}
	p.selectedSession = "session-id"
	clip.ResetRecent()
	t.Cleanup(clip.ResetRecent)
	_ = p.yankResumeCommand()()
	if copied, ok := clip.LastCopied(); !ok || copied != "codex --no-daemon resume session-id" {
		t.Fatalf("selected worktree's provider was not resolved: %q, %v", copied, ok)
	}
	probes, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(probes)) != worktree {
		t.Fatalf("probe used main project instead of selected worktree: %q, %v", probes, err)
	}
}
