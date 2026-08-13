package workspace

import (
	"testing"
	"time"
)

func mergeTestShell(name, display string) *ShellSession {
	return &ShellSession{
		Name:      display,
		TmuxName:  name,
		CreatedAt: time.Unix(100, 0),
	}
}

func shellNames(shells []*ShellSession) []string {
	names := make([]string, 0, len(shells))
	for _, shell := range shells {
		names = append(names, shell.TmuxName)
	}
	return names
}

func restoredNames(defs []ShellDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.TmuxName)
	}
	return names
}

func assertNames(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// TestMergeKeepsLocallyLiveShellAbsentFromManifest is the td-8d18de regression:
// another instance rewrote the shared manifest down to its own session, and the
// two shells it never knew about are still alive on this instance's tmux server.
func TestMergeKeepsLocallyLiveShellAbsentFromManifest(t *testing.T) {
	existing := []*ShellSession{
		mergeTestShell("sidecar-sh-sidecar-1", "Shell 1"),
		mergeTestShell("sidecar-sh-sidecar-2", "Shell 2"),
		mergeTestShell("sidecar-sh-sidecar-3", "Shell 3"),
	}
	result := mergeShellState(shellMergeInput{
		Existing: existing,
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Shell 1", CreatedAt: time.Unix(100, 0)},
		},
		Running: map[string]bool{
			"sidecar-sh-sidecar-1": true,
			"sidecar-sh-sidecar-2": true,
			"sidecar-sh-sidecar-3": true,
		},
		WorkDir:   "/tmp/x/sidecar",
		Namespace: "host:/tmp/tmux-501/default",
	})

	assertNames(t, "shells", shellNames(result.Shells), []string{
		"sidecar-sh-sidecar-1", "sidecar-sh-sidecar-2", "sidecar-sh-sidecar-3",
	})
	assertNames(t, "restored", restoredNames(result.Restored), []string{
		"sidecar-sh-sidecar-2", "sidecar-sh-sidecar-3",
	})
	if len(result.Dropped) != 0 {
		t.Fatalf("dropped = %v, want none", result.Dropped)
	}
	for _, shell := range result.Shells {
		if shell.IsOrphaned {
			t.Fatalf("shell %s marked orphaned while running", shell.TmuxName)
		}
	}
}

func TestMergeDropsShellNeitherInManifestNorLive(t *testing.T) {
	result := mergeShellState(shellMergeInput{
		Existing: []*ShellSession{
			mergeTestShell("sidecar-sh-sidecar-1", "Shell 1"),
			mergeTestShell("sidecar-sh-sidecar-2", "Shell 2"),
		},
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Shell 1"},
		},
		Running: map[string]bool{"sidecar-sh-sidecar-1": true},
		WorkDir: "/tmp/x/sidecar",
	})

	assertNames(t, "shells", shellNames(result.Shells), []string{"sidecar-sh-sidecar-1"})
	assertNames(t, "dropped", result.Dropped, []string{"sidecar-sh-sidecar-2"})
	if len(result.Restored) != 0 {
		t.Fatalf("restored = %v, want none", restoredNames(result.Restored))
	}
}

func TestMergeAdoptsDiscoveredSession(t *testing.T) {
	result := mergeShellState(shellMergeInput{
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Shell 1"},
		},
		Running: map[string]bool{
			"sidecar-sh-sidecar-1": true,
			"sidecar-sh-sidecar-3": true,
			"sidecar-sh-sidecar-2": true,
		},
		PaneID:    func(string) string { return "%7" },
		WorkDir:   "/tmp/x/sidecar",
		Namespace: "host:/tmp/tmux-501/default",
		Now:       func() time.Time { return time.Unix(500, 0) },
	})

	// Adoptions are sorted so the sidebar order does not depend on map order.
	assertNames(t, "shells", shellNames(result.Shells), []string{
		"sidecar-sh-sidecar-1", "sidecar-sh-sidecar-2", "sidecar-sh-sidecar-3",
	})
	assertNames(t, "restored", restoredNames(result.Restored), []string{
		"sidecar-sh-sidecar-2", "sidecar-sh-sidecar-3",
	})
	adopted := result.Shells[1]
	if adopted.Name != "Shell 2" {
		t.Fatalf("adopted display name = %q, want %q", adopted.Name, "Shell 2")
	}
	if adopted.Agent == nil || adopted.Agent.TmuxPane != "%7" {
		t.Fatalf("adopted shell did not get a live agent with its pane: %+v", adopted.Agent)
	}
	if !result.Restored[0].CreatedAt.Equal(time.Unix(500, 0)) {
		t.Fatalf("adopted CreatedAt = %v, want injected clock", result.Restored[0].CreatedAt)
	}
	if result.Restored[0].WorkDir != "/tmp/x/sidecar" {
		t.Fatalf("adopted WorkDir = %q, want the current workDir", result.Restored[0].WorkDir)
	}
	if adopted.WorkDir != "/tmp/x/sidecar" {
		t.Fatalf("adopted session WorkDir = %q, want the current workDir", adopted.WorkDir)
	}
}

func TestInferDefinitionWorkDirUsesExplicitThenPattern(t *testing.T) {
	def := ShellDefinition{TmuxName: "sidecar-sh-feature-1", WorkDir: "/repos/feature"}
	if got := inferDefinitionWorkDir(def, []string{"/repos/main", "/repos/feature"}, "/repos/main"); got != "/repos/feature" {
		t.Fatalf("explicit WorkDir = %q", got)
	}
	def.WorkDir = ""
	if got := inferDefinitionWorkDir(def, []string{"/repos/main", "/repos/feature"}, "/repos/main"); got != "/repos/feature" {
		t.Fatalf("inferred WorkDir = %q, want /repos/feature", got)
	}
	if got := inferDefinitionWorkDir(ShellDefinition{TmuxName: "sidecar-sh-main-1"}, nil, "/repos/main"); got != "/repos/main" {
		t.Fatalf("empty-as-current = %q", got)
	}
}

func TestGroupManifestShellsByWorkDirSeparatesNestFromShells(t *testing.T) {
	groups := groupManifestShellsByWorkDir([]ShellDefinition{
		{TmuxName: "sidecar-sh-main-1", DisplayName: "Mine", WorkDir: "/repos/main"},
		{TmuxName: "sidecar-sh-feature-1", DisplayName: "Sibling", WorkDir: "/repos/feature"},
	}, []string{"/repos/main", "/repos/feature"}, "/repos/main", nil)

	if len(groups["/repos/feature"]) != 1 || groups["/repos/feature"][0].TmuxName != "sidecar-sh-feature-1" {
		t.Fatalf("feature group = %+v", groups["/repos/feature"])
	}
	if len(groups["/repos/main"]) != 1 || groups["/repos/main"][0].TmuxName != "sidecar-sh-main-1" {
		t.Fatalf("main group = %+v", groups["/repos/main"])
	}
}

func TestMergeStampsNamespaceOnRestored(t *testing.T) {
	const ns = "host:/private/tmp/tmux-501/default"
	result := mergeShellState(shellMergeInput{
		Existing:  []*ShellSession{mergeTestShell("sidecar-sh-sidecar-2", "Shell 2")},
		Manifest:  nil,
		Running:   map[string]bool{"sidecar-sh-sidecar-2": true, "sidecar-sh-sidecar-9": true},
		WorkDir:   "/tmp/x/sidecar",
		Namespace: ns,
	})

	if len(result.Restored) != 2 {
		t.Fatalf("restored = %v, want 2 entries", restoredNames(result.Restored))
	}
	for _, def := range result.Restored {
		if def.Namespace != ns {
			t.Fatalf("restored %s namespace = %q, want %q", def.TmuxName, def.Namespace, ns)
		}
	}
}

func TestMergePreservesManifestOrder(t *testing.T) {
	result := mergeShellState(shellMergeInput{
		Existing: []*ShellSession{
			mergeTestShell("sidecar-sh-sidecar-1", "Shell 1"),
			mergeTestShell("sidecar-sh-sidecar-2", "Shell 2"),
		},
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-3", DisplayName: "Shell 3"},
			{TmuxName: "sidecar-sh-sidecar-2", DisplayName: "Renamed"},
		},
		Running: map[string]bool{
			"sidecar-sh-sidecar-1": true,
			"sidecar-sh-sidecar-2": true,
		},
		WorkDir: "/tmp/x/sidecar",
	})

	// Manifest order first, then the locally live survivor.
	assertNames(t, "shells", shellNames(result.Shells), []string{
		"sidecar-sh-sidecar-3", "sidecar-sh-sidecar-2", "sidecar-sh-sidecar-1",
	})
	if result.Shells[0].IsOrphaned != true {
		t.Fatalf("manifest-only, non-running shell should be orphaned")
	}
	if result.Shells[1].Name != "Renamed" {
		t.Fatalf("existing shell did not pick up the manifest display name: %q", result.Shells[1].Name)
	}
}

// TestMergeBuildsAgentForRevivedShell pins the "self-heal means usable" half of
// td-8d18de item 5: a shell that comes back to life must get an Agent, or it
// renders as live while enterInteractiveMode, recreateOrphanedShell and the
// terminal controller all refuse it — forever, since `r` re-runs this same merge.
func TestMergeBuildsAgentForRevivedShell(t *testing.T) {
	orphan := mergeTestShell("sidecar-sh-sidecar-1", "Shell 1")
	orphan.IsOrphaned = true

	result := mergeShellState(shellMergeInput{
		Existing: []*ShellSession{orphan},
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Shell 1"},
		},
		Running: map[string]bool{"sidecar-sh-sidecar-1": true},
		PaneID:  func(string) string { return "%7" },
		WorkDir: "/tmp/x/sidecar",
	})

	if orphan.IsOrphaned {
		t.Fatal("revived shell is still marked orphaned")
	}
	if orphan.Agent == nil {
		t.Fatal("revived shell has no Agent: it renders live but can never be opened")
	}
	if orphan.Agent.TmuxPane != "%7" {
		t.Errorf("revived agent pane = %q, want %%7", orphan.Agent.TmuxPane)
	}
	if len(result.Revived) != 1 || result.Revived[0] != orphan {
		t.Fatalf("Revived = %v, want the revived shell so the caller can poll it", result.Revived)
	}
}

// TestMergeHidesSiblingWorktreeEntries separates persistence from display: all
// worktrees of a repo share one shells.json, and a definition this working
// directory could never have produced must stay in the file but off the screen.
func TestMergeHidesSiblingWorktreeEntries(t *testing.T) {
	result := mergeShellState(shellMergeInput{
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Mine"},
			{TmuxName: "sidecar-sh-sidecar-agent-status-1", DisplayName: "Sibling's"},
		},
		WorkDir: "/tmp/x/sidecar",
	})

	assertNames(t, "shells", shellNames(result.Shells), []string{"sidecar-sh-sidecar-1"})
	if len(result.Dropped) != 0 {
		t.Fatalf("Dropped = %v, want nothing removed from the shared file", result.Dropped)
	}
}

// TestMergeHidesLeakedExistingSibling is the td-4819be defense: a sibling that
// leaked into Existing (ShellCreatedMsg used to append it) must not remain as
// an orphaned top-Shells row. It stays in the manifest.
func TestMergeHidesLeakedExistingSibling(t *testing.T) {
	leaked := mergeTestShell("sidecar-sh-sidecar-agent-status-1", "Sibling's")
	leaked.WorkDir = "/tmp/x/sidecar-agent-status"
	leaked.IsOrphaned = true
	result := mergeShellState(shellMergeInput{
		Existing: []*ShellSession{
			mergeTestShell("sidecar-sh-sidecar-1", "Mine"),
			leaked,
		},
		Manifest: []ShellDefinition{
			{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "Mine", WorkDir: "/tmp/x/sidecar"},
			{TmuxName: "sidecar-sh-sidecar-agent-status-1", DisplayName: "Sibling's", WorkDir: "/tmp/x/sidecar-agent-status"},
		},
		WorkDir: "/tmp/x/sidecar",
	})

	assertNames(t, "shells", shellNames(result.Shells), []string{"sidecar-sh-sidecar-1"})
	if len(result.Dropped) != 0 {
		t.Fatalf("Dropped = %v, want the sibling left in the shared file", result.Dropped)
	}
	if leaked.WorkDir != "/tmp/x/sidecar-agent-status" {
		t.Fatalf("merge rewrote sibling WorkDir to %q", leaked.WorkDir)
	}
}
