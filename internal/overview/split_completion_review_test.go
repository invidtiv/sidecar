package overview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/workspacecreate"
)

func TestSplitCompletionSurvivesReplacementDialog(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(map[bool]string{false: "current", true: "cached"}[cached]+"/"+map[bool]string{false: "success", true: "error"}[failed], func(t *testing.T) {
				m, _ := previewModel(t)
				m.preview.visible = true
				m.bindPreview(false)
				original := ensurePreviewTerminalSession
				ensurePreviewTerminalSession = func(string, string) (string, error) { return "%created", nil }
				t.Cleanup(func() { ensurePreviewTerminalSession = original })
				m.OpenPaneSwitcher()
				m.createForm.SetKind(workspacecreate.KindTerminalSplit)
				cmd := m.createPreviewTerminalSplit()
				if cmd == nil {
					t.Fatal("split command missing")
				}
				msg := cmd().(previewTerminalSplitCreatedMsg)
				if failed {
					msg.Err = errors.New("split failed")
				}
				leaf := m.preview.terminalPanes.Leaf(msg.LeafID)
				if cached {
					m.workspaces.SelectID("b")
					m.bindPreview(false)
				}
				m.OpenPaneSwitcher()
				m.createError = "replacement dialog"
				m.createBusy = true
				seed := &previewSplitSeed{session: "another split", typeCmd: "keep this prefill"}
				m.pendingSplitSeed = seed
				m.Update(msg)
				if !failed && leaf.PaneID != "%created" {
					t.Fatal("replacement dialog discarded a valid split completion")
				}
				if failed {
					panes := m.preview.terminalPanes
					if cached {
						panes = m.preview.paneCache[msg.WorkspaceID].terminals
					}
					if panes.Leaf(msg.LeafID) != nil {
						t.Fatal("failed split left a stranded pane")
					}
				}
				if !m.createOpen || !m.createBusy || m.createError != "replacement dialog" || m.pendingSplitSeed != seed {
					t.Fatal("split completion changed the replacement dialog")
				}
			})
		}
	}
}

func TestSplitPrefillRetainsOriginalOwnerAfterReplacementDialog(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, generation := range []uint64{0, 1} {
			t.Run(map[bool]string{false: "current", true: "cached"}[cached]+"/"+map[uint64]string{0: "before-first-dialog", 1: "after-dialog"}[generation], func(t *testing.T) {
				m, _ := previewModel(t)
				m.preview.visible = true
				m.bindPreview(false)
				original := ensurePreviewTerminalSession
				ensurePreviewTerminalSession = func(string, string) (string, error) { return "%created", nil }
				t.Cleanup(func() { ensurePreviewTerminalSession = original })
				m.OpenPaneSwitcher()
				m.createForm.SetKind(workspacecreate.KindTerminalSplit)
				m.createGeneration = generation
				cmd := m.createPreviewTerminalSplit()
				if cmd == nil {
					t.Fatal("split command missing")
				}
				msg := cmd().(previewTerminalSplitCreatedMsg)
				m.pendingSplitSeed = &previewSplitSeed{session: msg.Session, typeCmd: "original prefill"}
				if cached {
					m.workspaces.SelectID("b")
					m.bindPreview(false)
				}
				m.OpenPaneSwitcher()
				m.createError = "replacement dialog"
				dir := t.TempDir()
				marker := filepath.Join(dir, "args")
				// Failing send proves both dispatch ownership and error routing. The
				// shim launches no tmux server and touches only this test directory.
				t.Setenv("SPLIT_PROOF_ARGS", marker)
				if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SPLIT_PROOF_ARGS\"\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				run(t, m, m.Update(msg))
				args, err := os.ReadFile(marker)
				if err != nil || !strings.Contains(string(args), msg.Session+"\noriginal prefill\n") || m.pendingSplitSeed != nil {
					t.Fatalf("original split prefill never ran at its owner: %q, %v", args, err)
				}
				if !m.createOpen || m.createError != "replacement dialog" {
					t.Fatal("old prefill error changed the replacement dialog")
				}
			})
		}
	}
}

func TestRestoredSplitCompletionDoesNotCloseReplacementDialog(t *testing.T) {
	for _, generation := range []uint64{0, 1} {
		t.Run(map[uint64]string{0: "before-first-dialog", 1: "after-dialog"}[generation], func(t *testing.T) {
			m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
			m.createGeneration = generation
			ws, ok := m.SelectedWorkspace()
			if !ok {
				t.Fatal("workspace missing")
			}
			cmd := m.ensureRestoredPreviewShell(ws)
			if cmd == nil {
				t.Fatal("restore command missing")
			}
			m.OpenPaneSwitcher()
			m.createError = "replacement dialog"
			m.Update(cmd())
			if leaf.PaneID != "%peer" || !m.createOpen || m.createError != "replacement dialog" {
				t.Fatal("restore completion changed the replacement dialog")
			}
		})
	}
}
