package sessionrestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestorePlanDoesNotProbeProviderCapabilities(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "probe")
	script := "#!/bin/sh\nprintf probe > '" + marker + "'\nexit 2\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
	in.Config.ResumeAgents = ResumeAuto
	step := only(t, Build(in))
	if step.Action != ActionResumeAgent || !step.Agent.Resume {
		t.Fatalf("planning must preserve exact resume intent: %+v", step)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("planning executed provider: %v", err)
	}
}
