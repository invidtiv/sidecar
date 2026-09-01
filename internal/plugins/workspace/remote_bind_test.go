package workspace

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestRemoteStartListsHostShellsWithoutLocalStartup(t *testing.T) {
	p := New()
	calledLocal := false
	p.shellStartupHooks.loadManifest = func(string) (*ShellManifest, error) {
		calledLocal = true
		return &ShellManifest{}, nil
	}
	ctx := &plugin.Context{
		HostID:      "aerie",
		ProjectKey:  "/home/me/sidecar",
		WorkDir:     "/would/be/wrong",
		ProjectRoot: "/would/be/wrong",
		HostWorkspaces: func() []plugin.HostWorkspace {
			return []plugin.HostWorkspace{{
				Kind:     string(workspaceinventory.KindShell),
				Name:     "Claude pane",
				TmuxName: "sidecar-claude",
				PaneID:   "%7",
				Live:     true,
			}}
		},
	}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if cmd := p.Start(); cmd != nil {
		_ = cmd
	}
	if calledLocal {
		t.Fatal("loadShellStartup ran against the twin local path")
	}
	if len(p.shells) != 1 || p.shells[0].Name != "Claude pane" || p.shells[0].TmuxName != "sidecar-claude" {
		t.Fatalf("shells = %+v", p.shells)
	}
}

func TestRemoteCreateShellRefusesNamingHost(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{HostID: "aerie"}
	cmd := p.createShell(shellCreateOpts{CustomName: "nope"})
	if cmd == nil {
		t.Fatal("createShell returned nil")
	}
	msg, ok := cmd().(ShellCreatedMsg)
	if !ok || msg.Err == nil {
		t.Fatalf("msg = %#v, want a named refusal", msg)
	}
	if !strings.Contains(msg.Err.Error(), "aerie") {
		t.Errorf("refusal = %v, want the host named", msg.Err)
	}
}

func TestRemoteTerminalUsesControlSpawner(t *testing.T) {
	p := New()
	var spawned bool
	p.ctx = &plugin.Context{
		HostID: "aerie",
		RemoteControlSpawner: func() tty.ControlSpawner {
			spawned = true
			return func(string) *exec.Cmd { return exec.Command("false") }
		},
	}
	model := p.newWorkspaceTerminal(workspaceTerminalPrimary)
	p.applyTerminalControl(model)
	if !spawned {
		t.Fatal("RemoteControlSpawner was not consulted")
	}
	if !model.IsRemote() {
		t.Fatal("terminal was not switched to remote control")
	}
	p.ctx.HostID = ""
	p.applyTerminalControl(model)
	if model.IsRemote() {
		t.Fatal("UseLocalControl was not applied when returning to local")
	}
}

func TestHostInventoryMsgRefreshesBoundShells(t *testing.T) {
	p := New()
	shells := []plugin.HostWorkspace{{
		Kind: string(workspaceinventory.KindShell), Name: "one", TmuxName: "s1", Live: true,
	}}
	p.ctx = &plugin.Context{
		HostID:     "aerie",
		ProjectKey: "/home/me/sidecar",
		HostWorkspaces: func() []plugin.HostWorkspace {
			return shells
		},
	}
	p.applyHostInventory()
	if len(p.shells) != 1 {
		t.Fatalf("shells = %+v", p.shells)
	}
	shells = append(shells, plugin.HostWorkspace{
		Kind: string(workspaceinventory.KindShell), Name: "two", TmuxName: "s2", Live: true,
	})
	_, _ = p.handleHostInventory()
	if len(p.shells) != 2 {
		t.Fatalf("after inventory msg shells = %+v", p.shells)
	}
}
