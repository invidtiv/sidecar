package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDestinationCarriesOnlyVerifiedMatchingPane(t *testing.T) {
	home, socket := setupShellCLI(t, "attachment proof")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%7")
	stateDir := filepath.Join(home, "sidecar")
	dest, err := resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "%7" {
		t.Fatalf("matching live caller: %+v %v", dest, err)
	}
	// The same session on another socket is not evidence for this destination.
	tmux := filepath.Join(filepath.SplitList(os.Getenv("PATH"))[0], "tmux")
	script := "#!/bin/sh\nprintf 'sidecar-sh-sidecar-1\\t/other/socket\\n'\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("foreign socket leaked pane: %+v %v", dest, err)
	}
	script = "#!/bin/sh\nprintf 'sidecar-sh-other-1\\t%s\\n' " + shellQuote(socket) + "\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("foreign session leaked pane: %+v %v", dest, err)
	}
	t.Setenv("TMUX_PANE", "")
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("missing caller pane: %+v %v", dest, err)
	}
}
