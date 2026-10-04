package overview

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestLateMutationCompletionsLeaveReplacementConfigurationAlone(t *testing.T) {
	late := errors.New("old project failure")
	constructors := []func(completionScope) tea.Msg{
		func(s completionScope) tea.Msg { return globalShellCreatedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return globalWorktreePlannedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return globalWorktreeCreatedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return globalWorkspaceLaunchedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return globalWorktreeDeletedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return globalShellDeletedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg {
			return projectMutationRefreshMsg{completionScope: s, Project: Project{Path: "/old"}, Err: late}
		},
		func(s completionScope) tea.Msg {
			return projectMutationRefreshMsg{completionScope: s, Project: Project{Path: "/old"}, Result: workspaceinventory.ProjectResult{ProjectKey: "/old"}}
		},
		func(s completionScope) tea.Msg { return previewTerminalSplitCreatedMsg{completionScope: s, Err: late} },
		func(s completionScope) tea.Msg { return previewSplitSeedFailedMsg{completionScope: s, Err: late} },
	}
	for _, constructor := range constructors {
		m := New(workspaceinventory.Collector{})
		m.configurationGeneration = 1
		msg := constructor(m.completionScope())
		m.configurationGeneration = 2
		m.createOpen, m.createBusy, m.deleteOpen, m.deleteBusy = true, true, true, true
		m.createError, m.deleteError = "new create", "new delete"
		if cmd := m.Update(msg); cmd != nil || !m.createOpen || !m.createBusy || !m.deleteOpen || !m.deleteBusy || m.createError != "new create" || m.deleteError != "new delete" || len(m.results) != 0 {
			t.Fatalf("%T altered the replacement configuration or modal", msg)
		}
	}
}

func TestLateCreateCompletionCannotCloseNewDialog(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.createGeneration = 1
	msg := globalShellCreatedMsg{completionScope: m.createCompletionScope(), Tmux: "old"}
	m.createGeneration = 2
	m.createOpen, m.createBusy = true, true
	if cmd := m.Update(msg); cmd != nil || !m.createOpen || !m.createBusy {
		t.Fatal("previous create completion closed the new dialog")
	}
}

func TestCompletionConfigurationFenceSurvivesRefreshPoll(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.configurationGeneration = 1
	scope := m.completionScope()
	m.generation++
	if !m.completionCurrent(scope) {
		t.Fatal("a normal inventory poll invalidated an owned completion")
	}
	m.configurationGeneration++
	if m.completionCurrent(scope) {
		t.Fatal("a changed project set retained the old completion")
	}
}
