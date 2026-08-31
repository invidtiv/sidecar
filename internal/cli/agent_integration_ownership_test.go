package cli

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentintegration"
)

// A surface must be able to say what Sidecar owns without knowing which
// provider it is talking about. These pin the two sentences that were wrong
// while ownership was one boolean with two meanings.

// TestAUserOwnedFileIsNeverCalledSidecarOwned is the sentence that mattered
// most. Codex's ~/.codex/config.toml and Claude's ~/.claude/settings.json are
// full of the user's own configuration; Sidecar owns one entry in each. Telling
// a user Sidecar owns that file is both false and alarming, and it is the
// claim the uninstall verb would be read against.
func TestAUserOwnedFileIsNeverCalledSidecarOwned(t *testing.T) {
	entry := agentintegration.FileState{
		Path: "/home/u/.codex/config.toml", Exists: true, Kind: "file",
		Owned: true, Ownership: agentintegration.OwnsEntry, Mode: "0644",
	}
	got := describeFileState(entry)
	if strings.Contains(got, "sidecar-owned") {
		t.Fatalf("a file the user owns was described as Sidecar's: %q", got)
	}
	if !strings.Contains(got, "Sidecar's entry") {
		t.Fatalf("the description does not say what Sidecar actually has here: %q", got)
	}

	file := entry
	file.Ownership = agentintegration.OwnsFile
	file.Version = "1"
	if got := describeFileState(file); !strings.Contains(got, "sidecar-owned version 1") {
		t.Fatalf("a genuinely Sidecar-owned file lost its description: %q", got)
	}
}

// TestAnOwnedThingWithNoVersionDoesNotRenderADanglingVersion pins the concrete
// defect the overload produced in shipped output: Codex set config.toml's
// Owned without ever setting a Version, so `status codex` printed
// "sidecar-owned version " with nothing after it, in both the status list and
// every before/after line of every plan.
func TestAnOwnedThingWithNoVersionDoesNotRenderADanglingVersion(t *testing.T) {
	noVersion := agentintegration.FileState{
		Path: "/home/u/.codex/config.toml", Exists: true, Kind: "file",
		Owned: true, Ownership: agentintegration.OwnsEntry, Mode: "0644",
	}
	for name, got := range map[string]string{
		"describeFileState": describeFileState(noVersion),
		"describeOwnership": describeOwnership(noVersion),
	} {
		if strings.Contains(got, "version ") {
			t.Fatalf("%s rendered a version that does not exist: %q", name, got)
		}
		if strings.HasSuffix(strings.TrimSpace(got), "version") {
			t.Fatalf("%s left a dangling version word: %q", name, got)
		}
	}
}

// TestADirectoryIsNotDescribedAsAForeignFile closes the other misreading.
// "not Sidecar's" is the phrase reserved for a foreign file sitting at
// Sidecar's own path, which is a refusal; a plugin directory Sidecar creates
// and removes is neither foreign nor a refusal, and reading that phrase against
// it suggested damage where there was none.
func TestADirectoryIsNotDescribedAsAForeignFile(t *testing.T) {
	dir := agentintegration.FileState{
		Path: "/home/u/.config/opencode/plugin", Exists: true, Kind: "dir", Mode: "0755",
	}
	got := describeFileState(dir)
	if strings.Contains(got, "not Sidecar's") {
		t.Fatalf("an ordinary directory was described as a foreign file: %q", got)
	}
	if !strings.Contains(got, "directory") {
		t.Fatalf("a directory is not described as one: %q", got)
	}
}
