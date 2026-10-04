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
