package main

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/state"
)

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
