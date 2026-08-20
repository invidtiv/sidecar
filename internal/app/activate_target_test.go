package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
)

func activationModel(workDir string) *Model {
	ui := NewUIState()
	ui.WorkDir = workDir
	ui.ProjectRoot = workDir
	return &Model{ui: ui}
}

// collect flattens a command (including a tea.Batch) into its messages.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, collect(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestActivateFileTargetFocusesFileBrowser(t *testing.T) {
	m := activationModel(t.TempDir())
	msgs := collect(m.activateTarget(ActivateTargetMsg{
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "internal/app/model.go", Line: 7},
	}))
	var focused bool
	var navigated NavigateToFileMsg
	for _, got := range msgs {
		switch typed := got.(type) {
		case FocusPluginByIDMsg:
			if typed.PluginID != "file-browser" {
				t.Fatalf("focused %q", typed.PluginID)
			}
			focused = true
		case NavigateToFileMsg:
			navigated = typed
		}
	}
	if !focused {
		t.Fatal("expected the file browser to be focused")
	}
	if navigated.Path != "internal/app/model.go" || navigated.Line != 7 {
		t.Fatalf("unexpected navigation %+v", navigated)
	}
}

func TestActivateMalformedTargetIsRefusedOutLoud(t *testing.T) {
	m := activationModel(t.TempDir())
	msgs := collect(m.activateTarget(ActivateTargetMsg{
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "/etc/passwd"},
	}))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if _, ok := msgs[0].(notify.PostMsg); !ok {
		t.Fatalf("expected a notification, got %T", msgs[0])
	}
}

func TestActivateOtherProjectIsRefusedForNow(t *testing.T) {
	m := activationModel(t.TempDir())
	msgs := collect(m.activateTarget(ActivateTargetMsg{
		Target:  uirequest.Target{Kind: uirequest.TargetKindFile, Value: "main.go"},
		Project: "some-other-project",
	}))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if _, ok := msgs[0].(notify.PostMsg); !ok {
		t.Fatalf("expected a notification, got %T", msgs[0])
	}
}

func TestTargetProjectIsCurrentAcceptsPathAndBaseName(t *testing.T) {
	dir := t.TempDir()
	m := activationModel(dir)
	for _, project := range []string{"", dir, baseName(dir)} {
		if !m.targetProjectIsCurrent(project) {
			t.Fatalf("expected %q to be the current project", project)
		}
	}
	if m.targetProjectIsCurrent("nowhere") {
		t.Fatal("expected an unrelated project to be rejected")
	}
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
