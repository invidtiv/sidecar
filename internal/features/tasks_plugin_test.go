package features

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

func TestTasksPlugin_DefaultOff(t *testing.T) {
	globalManager = nil
	if TasksPlugin.Default {
		t.Error("tasks_plugin must ship disabled by default")
	}
	if IsEnabled(TasksPlugin.Name) {
		t.Error("tasks_plugin should be disabled without config")
	}
	if !IsKnownFeature(TasksPlugin.Name) {
		t.Error("tasks_plugin should be a registered feature")
	}
}

func TestTasksPlugin_ConfigEnables(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[TasksPlugin.Name] = true
	Init(cfg)
	t.Cleanup(func() { globalManager = nil })

	if !IsEnabled(TasksPlugin.Name) {
		t.Error("tasks_plugin should be enabled by config")
	}

	SetOverride(TasksPlugin.Name, false)
	if IsEnabled(TasksPlugin.Name) {
		t.Error("CLI override should win over config")
	}
}

func TestTasksPlugin_ListedInAll(t *testing.T) {
	found := false
	for _, f := range ListAll() {
		if f.Name == TasksPlugin.Name {
			found = true
		}
	}
	if !found {
		t.Error("tasks_plugin missing from ListAll")
	}
}
