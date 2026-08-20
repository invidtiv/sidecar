package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
)

func pendingModel(t *testing.T, plugins ...plugin.Plugin) *Model {
	t.Helper()
	registry := plugin.NewRegistry(nil)
	for _, p := range plugins {
		if err := registry.Register(p); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	m := activationModel(t.TempDir())
	m.registry = registry
	return m
}

func fileTarget() uirequest.Target {
	return uirequest.Target{Kind: uirequest.TargetKindFile, Value: "internal/app/model.go", Line: 3}
}

// TestPendingTargetAppliesOnceAndClears is the slot's core promise: what was
// parked before a switch is re-emitted after it, exactly once.
func TestPendingTargetAppliesOnceAndClears(t *testing.T) {
	m := pendingModel(t)
	m.setPendingActivation(pendingActivation{target: &ActivateTargetMsg{Target: fileTarget(), Project: "elsewhere"}})

	cmds := m.applyPendingActivation()
	if len(cmds) != 1 {
		t.Fatalf("expected one landing command, got %d", len(cmds))
	}
	landing, ok := cmds[0]().(ActivateTargetMsg)
	if !ok {
		t.Fatalf("expected an ActivateTargetMsg, got %T", cmds[0]())
	}
	if landing.Project != "" {
		t.Fatalf("landing kept its project qualifier: %q", landing.Project)
	}
	if landing.Target.Value != "internal/app/model.go" || landing.Target.Line != 3 {
		t.Fatalf("landing target = %+v", landing.Target)
	}
	if m.pendingActivation != nil {
		t.Fatal("slot survived its own application")
	}
	if cmds := m.applyPendingActivation(); cmds != nil {
		t.Fatal("an applied hand-off landed a second time")
	}
}

// TestPendingTargetNewestWins: one slot, not a queue.
func TestPendingTargetNewestWins(t *testing.T) {
	m := pendingModel(t)
	first := ActivateTargetMsg{Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "old.go"}}
	second := ActivateTargetMsg{Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "new.go"}}
	m.setPendingActivation(pendingActivation{target: &first})
	m.setPendingActivation(pendingActivation{target: &second})

	cmds := m.applyPendingActivation()
	if len(cmds) != 1 {
		t.Fatalf("expected one landing, got %d", len(cmds))
	}
	landing := cmds[0]().(ActivateTargetMsg)
	if landing.Target.Value != "new.go" {
		t.Fatalf("older jump won: %q", landing.Target.Value)
	}
}

// TestPendingTargetClearedByNavigation: a user who moved on is not waiting for
// a jump they no longer asked for.
func TestPendingTargetClearedByNavigation(t *testing.T) {
	target := ActivateTargetMsg{Target: fileTarget()}
	m := pendingModel(t, &navigationPlugin{id: "somewhere"})
	m.setPendingActivation(pendingActivation{target: &target})

	m.FocusPluginByID("somewhere")
	if m.pendingActivation != nil {
		t.Fatal("navigating to a plugin left the parked jump in place")
	}
	if cmds := m.applyPendingActivation(); cmds != nil {
		t.Fatal("a cleared hand-off still landed")
	}
}

// TestPendingSelectionAppliesThroughTheSlot proves the workspace
// pending-selection pair is a client of the slot, not a parallel mechanism.
func TestPendingSelectionAppliesThroughTheSlot(t *testing.T) {
	workspace := &navigationPlugin{id: workspacePluginID}
	m := pendingModel(t, workspace)
	selection := plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionShell, Key: "sidecar-main"}
	m.setPendingActivation(pendingActivation{selection: &selection})

	m.applyPendingActivation()
	if workspace.pending == nil || workspace.pending.Key != "sidecar-main" {
		t.Fatalf("selection not delivered: %+v", workspace.pending)
	}
	if m.pendingActivation != nil {
		t.Fatal("slot survived its own application")
	}
}

// TestActivateUnresolvableProjectDeclinesOutLoud: never a silent drop.
func TestActivateUnresolvableProjectDeclinesOutLoud(t *testing.T) {
	m := pendingModel(t)
	m.cfg = config.Default()
	msgs := collect(m.activateTarget(ActivateTargetMsg{Target: fileTarget(), Project: "no-such-project"}))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if _, ok := msgs[0].(notify.PostMsg); !ok {
		t.Fatalf("expected a notification, got %T", msgs[0])
	}
	if m.pendingActivation != nil {
		t.Fatal("a declined jump was parked anyway")
	}
}

// TestActivateMalformedCrossProjectTargetRefusesBeforeSwitching: a bad target
// must not tear down the project the user is in on its way to failing.
func TestActivateMalformedCrossProjectTargetRefusesBeforeSwitching(t *testing.T) {
	m := pendingModel(t)
	m.cfg = config.Default()
	m.cfg.Projects.List = []config.ProjectConfig{{Name: "other", Path: t.TempDir()}}
	before := m.ui.WorkDir
	msgs := collect(m.activateTarget(ActivateTargetMsg{
		Target:  uirequest.Target{Kind: uirequest.TargetKindFile, Value: ""},
		Project: "other",
	}))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if _, ok := msgs[0].(notify.PostMsg); !ok {
		t.Fatalf("expected a notification, got %T", msgs[0])
	}
	if m.ui.WorkDir != before || m.pendingActivation != nil {
		t.Fatal("a malformed target still started a project switch")
	}
}

// TestActivateTargetForAbsentPluginDeclinesOutLoud covers the rebuilt-registry
// edge: the project we landed in has no such plugin.
func TestActivateTargetForAbsentPluginDeclinesOutLoud(t *testing.T) {
	m := pendingModel(t, &navigationPlugin{id: "git-status"})
	msgs := collect(m.activateTarget(ActivateTargetMsg{Target: fileTarget()}))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if _, ok := msgs[0].(notify.PostMsg); !ok {
		t.Fatalf("expected a notification, got %T", msgs[0])
	}
}

// TestActivateTargetNamingTheCurrentProjectByPathLandsNow: a qualifier that
// resolves to where the user already is must not park anything.
func TestActivateTargetNamingTheCurrentProjectLandsImmediately(t *testing.T) {
	m := pendingModel(t, &navigationPlugin{id: "file-browser"})
	m.cfg = config.Default()
	m.cfg.Projects.List = []config.ProjectConfig{{Name: "here", Path: m.ui.WorkDir}}
	msgs := collect(m.activateTarget(ActivateTargetMsg{Target: fileTarget(), Project: "here"}))
	var navigated bool
	for _, got := range msgs {
		if _, ok := got.(NavigateToFileMsg); ok {
			navigated = true
		}
	}
	if !navigated {
		t.Fatalf("expected the jump to land, got %#v", msgs)
	}
	if m.pendingActivation != nil {
		t.Fatal("a same-project jump was parked")
	}
}

func TestResolveProjectPathDistinguishesPathsFromNames(t *testing.T) {
	dir := t.TempDir()
	m := pendingModel(t)
	m.cfg = config.Default()
	m.cfg.Projects.List = []config.ProjectConfig{{Name: "widgets", Path: dir}}

	if path, exact, ok := m.resolveProjectPath("widgets"); !ok || path != dir || exact {
		t.Fatalf("by name = %q exact=%v ok=%v", path, exact, ok)
	}
	if path, exact, ok := m.resolveProjectPath(dir); !ok || path != dir || !exact {
		t.Fatalf("by path = %q exact=%v ok=%v", path, exact, ok)
	}
	// An unconfigured but real checkout is still a destination.
	other := t.TempDir()
	if path, exact, ok := m.resolveProjectPath(other); !ok || path != other || !exact {
		t.Fatalf("unconfigured path = %q exact=%v ok=%v", path, exact, ok)
	}
	if _, _, ok := m.resolveProjectPath("nowhere-at-all"); ok {
		t.Fatal("a name matching nothing resolved anyway")
	}
}
