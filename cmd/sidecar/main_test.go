package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/state"
)

func TestTerminalPerformanceEnabled(t *testing.T) {
	for _, value := range []string{"1", "true", "YES", " on "} {
		t.Run(fmt.Sprintf("enabled_%q", value), func(t *testing.T) {
			if !terminalPerformanceEnabled(value) {
				t.Fatalf("terminalPerformanceEnabled(%q) = false", value)
			}
		})
	}
	for _, value := range []string{"", "0", "false", "sometimes"} {
		t.Run(fmt.Sprintf("disabled_%q", value), func(t *testing.T) {
			if terminalPerformanceEnabled(value) {
				t.Fatalf("terminalPerformanceEnabled(%q) = true", value)
			}
		})
	}
}

// The first line of --version is parsed by scripts/dev-install.sh (prefix match)
// and scripts/verify-release-archives.sh (exact match on the first line), so it
// has to stay one line of exactly "sidecar version <v>".
func TestVersionLinesFirstLineIsStable(t *testing.T) {
	for _, version := range []string{"v1.9.0", "devel+main.577c47fc", "devel"} {
		t.Run(version, func(t *testing.T) {
			lines := versionLines(version, buildDetails{
				commit:  "a1b2c3d",
				dirty:   true,
				date:    "2026-08-27T18:04:11Z",
				profile: "release",
			})
			if want := "sidecar version " + version; lines[0] != want {
				t.Fatalf("first line = %q, want %q", lines[0], want)
			}
			for _, line := range lines[1:] {
				if !strings.HasPrefix(line, "  ") {
					t.Fatalf("detail line %q must be indented so version probes skip it", line)
				}
			}
		})
	}
}

func TestVersionLinesOmitsUnknownDetail(t *testing.T) {
	lines := versionLines("v1.9.0", buildDetails{profile: "development"})
	got := strings.Join(lines, "\n")
	want := "sidecar version v1.9.0\n  profile: development"
	if got != want {
		t.Fatalf("versionLines() = %q, want %q", got, want)
	}
}

func TestVersionLinesMarksDirtyCommit(t *testing.T) {
	clean := versionLines("v1.9.0", buildDetails{commit: "a1b2c3d", profile: "release"})
	if want := "  commit:  a1b2c3d"; clean[1] != want {
		t.Fatalf("clean commit line = %q, want %q", clean[1], want)
	}
	dirty := versionLines("v1.9.0", buildDetails{commit: "a1b2c3d", dirty: true, profile: "development"})
	if want := "  commit:  a1b2c3d (dirty)"; dirty[1] != want {
		t.Fatalf("dirty commit line = %q, want %q", dirty[1], want)
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit("577c47fc1234567890"); got != "577c47f" {
		t.Fatalf("shortCommit() = %q, want %q", got, "577c47f")
	}
	if got := shortCommit("577c"); got != "577c" {
		t.Fatalf("shortCommit() = %q, want %q", got, "577c")
	}
}

func TestInitialPluginRestoresPerWorktreeAcrossRestart(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "config")
	if err := state.InitWithDir(stateDir); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "repo")
	a := filepath.Join(root, "worktrees", "a")
	b := filepath.Join(root, "worktrees", "b")
	if err := state.SetActivePlugin(root, "td-monitor"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetActivePlugin(a, "file-browser"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetActivePlugin(b, "workspace"); err != nil {
		t.Fatal(err)
	}

	if got := initialPluginForWorkDir(a, root); got != "file-browser" {
		t.Fatalf("A startup plugin = %q, want file-browser", got)
	}
	if got := initialPluginForWorkDir(b, root); got != "workspace" {
		t.Fatalf("B startup plugin = %q, want workspace", got)
	}
	if err := state.InitWithDir(stateDir); err != nil {
		t.Fatal(err)
	}
	if got := initialPluginForWorkDir(a, root); got != "file-browser" {
		t.Fatalf("A restart plugin = %q, want file-browser", got)
	}
	if got := initialPluginForWorkDir(b, root); got != "workspace" {
		t.Fatalf("B restart plugin = %q, want workspace", got)
	}
}
