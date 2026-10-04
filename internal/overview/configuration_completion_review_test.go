package overview

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestConfigurationChangeClosesInvalidatedBusyDialogs(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	defer m.Stop()
	projects := []Project{{Path: t.TempDir()}}
	m.SetProjects(projects)
	scope := m.completionScope()
	m.createOpen, m.createBusy = true, true
	m.renameOpen, m.renameBusy = true, true
	m.deleteOpen, m.deleteBusy = true, true
	m.start(projects, "poll")
	if !m.createOpen || !m.createBusy || !m.renameOpen || !m.renameBusy || !m.deleteOpen || !m.deleteBusy {
		t.Fatal("ordinary refresh retired a valid operation dialog")
	}
	m.SetProjects(append(projects, Project{Path: t.TempDir()}))
	m.Update(globalShellCreatedMsg{completionScope: scope, Err: errors.New("old failure")})
	if m.createOpen || m.createBusy || m.renameOpen || m.renameBusy || m.deleteOpen || m.deleteBusy {
		t.Fatal("configuration change stranded an invalidated operation dialog")
	}
}

func TestConfigurationReorderPreservesOperations(t *testing.T) {
	for _, refresh := range []string{"set projects", "refresh", "poll"} {
		t.Run(refresh, func(t *testing.T) {
			m := New(workspaceinventory.Collector{})
			defer m.Stop()
			projects := []Project{{Path: t.TempDir()}, {Path: t.TempDir()}}
			m.SetProjects(projects)
			scope := m.createCompletionScope()
			m.createOpen, m.createBusy = true, true
			m.renameOpen, m.renameBusy = true, true
			m.deleteOpen, m.deleteBusy = true, true
			seed := &previewSplitSeed{session: "original", run: "echo ready"}
			m.pendingSplitSeed = seed
			reordered := []Project{projects[1], projects[0]}
			if refresh == "set projects" {
				m.SetProjects(reordered)
			} else {
				m.start(reordered, refresh)
			}
			if !m.completionCurrent(scope) {
				t.Error("reordering projects invalidated the operation completion")
			}
			if !m.createOpen || !m.createBusy || !m.renameOpen || !m.renameBusy || !m.deleteOpen || !m.deleteBusy || m.pendingSplitSeed != seed {
				t.Error("reordering projects retired a valid dialog or split seed")
			}
			failure := errors.New("original operation failed")
			m.Update(globalShellCreatedMsg{completionScope: scope, Project: projects[0], Err: failure})
			if m.createBusy || m.createError != failure.Error() {
				t.Errorf("original completion was not delivered: busy=%v error=%q", m.createBusy, m.createError)
			}
		})
	}
}

func TestSameConfiguredProjectMembership(t *testing.T) {
	for _, tc := range []struct {
		name     string
		paths    []string
		projects []Project
		want     bool
	}{
		{name: "empty", want: true},
		{name: "reordered", paths: []string{"/one", "/two"}, projects: []Project{{Path: "/two"}, {Path: "/one"}}, want: true},
		{name: "added", paths: []string{"/one"}, projects: []Project{{Path: "/one"}, {Path: "/two"}}},
		{name: "removed", paths: []string{"/one", "/two"}, projects: []Project{{Path: "/one"}}},
		{name: "replaced", paths: []string{"/one", "/two"}, projects: []Project{{Path: "/one"}, {Path: "/three"}}},
		{name: "duplicate is not replacement", paths: []string{"/one", "/two"}, projects: []Project{{Path: "/one"}, {Path: "/one"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameConfiguredProjectMembership(tc.paths, tc.projects); got != tc.want {
				t.Fatalf("sameConfiguredProjectMembership = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigurationReorderRefreshesPresentationOrder(t *testing.T) {
	m := New(workspaceinventory.Collector{Runner: &stageRunner{}})
	defer m.Stop()
	projects := []Project{{Name: "one", Path: workspaceinventory.CanonicalPath(t.TempDir())}, {Name: "two", Path: workspaceinventory.CanonicalPath(t.TempDir())}}
	m.SetProjects(projects)
	reordered := []Project{projects[1], projects[0]}
	cmd := m.SetProjects(reordered)
	if cmd == nil {
		t.Fatal("reordering did not schedule collection with the new presentation order")
	}
	queue := []tea.Cmd{cmd}
	for steps := 0; m.loading && len(queue) > 0 && steps < 100; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
		} else {
			queue = append(queue, m.Update(msg))
		}
	}
	if m.loading || len(m.projects) != 2 || m.projects[0].Path != reordered[0].Path || m.projects[1].Path != reordered[1].Path {
		t.Fatalf("reordered inventory did not adopt presentation order: loading=%v projects=%+v", m.loading, m.projects)
	}
}
