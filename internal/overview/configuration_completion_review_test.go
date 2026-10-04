package overview

import (
	"errors"
	"testing"

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
			originalContext := m.ctx
			if refresh == "set projects" {
				if cmd := m.SetProjects(reordered); cmd != nil {
					t.Error("reordering projects restarted collection")
				}
				select {
				case <-originalContext.Done():
					t.Error("reordering projects canceled the in-flight collection")
				default:
				}
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

func TestSameConfiguredProjectsComparesMembership(t *testing.T) {
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
			if got := sameConfiguredProjects(tc.paths, tc.projects); got != tc.want {
				t.Fatalf("sameConfiguredProjects = %v, want %v", got, tc.want)
			}
		})
	}
}
