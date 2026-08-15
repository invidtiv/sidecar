package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestUIRequests_PendingViewLifecycle(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	p := &Plugin{
		shells: []*ShellSession{
			{TmuxName: "sidecar-sh-sidecar-1", Name: "Shell 1"},
			{TmuxName: "sidecar-sh-sidecar-2", Name: "Shell 2"},
		},
		selectedShellIdx: 0,
		shellSelected:    true,
	}

	// Request for unselected shell: should queue and ack StatusQueued
	req := uirequest.Request{
		ID:        "req-1",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin: uirequest.Origin{
			TmuxSession: "sidecar-sh-sidecar-2",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "README.md",
			Line:  10,
		},
	}

	cmd := p.handleUIRequest(req)
	if cmd != nil {
		t.Errorf("handleUIRequest on unselected shell returned non-nil cmd: %v", cmd)
	}

	badge, hasBadge := p.pendingViewBadge("sidecar-sh-sidecar-2")
	if !hasBadge || badge == "" {
		t.Errorf("expected pending view badge on shell 2, got %q, %v", badge, hasBadge)
	}

	// Read acks
	t.Logf("config.StateDir() = %s, stateHome = %s", config.StateDir(), filepath.Join(stateHome, "sidecar"))
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	if acks[0].Status != uirequest.StatusQueued {
		t.Errorf("expected status %s, got %s", uirequest.StatusQueued, acks[0].Status)
	}

	// Foreign shell: ignore silently
	foreignReq := uirequest.Request{
		ID:     "req-foreign",
		Action: uirequest.ActionOpen,
		Origin: uirequest.Origin{
			TmuxSession: "other-session",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "foo.go",
		},
	}
	foreignCmd := p.handleUIRequest(foreignReq)
	if foreignCmd != nil {
		t.Errorf("expected nil cmd for foreign shell, got %v", foreignCmd)
	}
	foreignAcks, _ := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), foreignReq.ID, foreignReq.Action)
	if len(foreignAcks) > 0 {
		t.Errorf("expected 0 acks for foreign shell, got %d", len(foreignAcks))
	}
}

// An open that puts nothing on screen must be acknowledged as declined. The
// agent's exit code is the only thing telling it whether the user can see the
// file, so a pane that never opened may never be reported as opened.
func TestUIRequests_SelectedShellDeclinesWhenNothingOpens(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	workDir := t.TempDir()
	p := &Plugin{
		ctx: &plugin.Context{WorkDir: workDir},
		shells: []*ShellSession{
			{TmuxName: "sidecar-sh-sidecar-1", Name: "Shell 1", WorkDir: workDir},
		},
		selectedShellIdx: 0,
		shellSelected:    true,
	}

	req := uirequest.Request{
		ID:        "req-decline",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin:    uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target:    uirequest.Target{Kind: uirequest.TargetKindFile, Value: "README.md"},
	}

	// No pane tree: the open cannot land anywhere.
	p.handleUIRequest(req)

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	if acks[0].Status != uirequest.StatusDeclined {
		t.Errorf("expected status %s, got %s", uirequest.StatusDeclined, acks[0].Status)
	}
}

// Both pane hosts live in one process, so their acks must not share a file
// name — otherwise one host's answer silently overwrites the other's.
func TestUIRequests_InstanceIDIsPerHost(t *testing.T) {
	if hostInstanceID() == uirequest.InstanceID("overview") {
		t.Errorf("workspace and overview hosts share instance id %q", hostInstanceID())
	}
}

func TestUIRequests_ExpiredPendingView(t *testing.T) {
	p := &Plugin{
		pendingViews: map[string]*pendingView{
			"sh-1": {
				Target:    uirequest.Target{Kind: uirequest.TargetKindFile, Value: "a.txt"},
				CreatedAt: time.Now().Add(-10 * time.Minute),
				TTLMs:     1000,
			},
		},
	}

	if _, has := p.pendingViewBadge("sh-1"); has {
		t.Errorf("expected expired pending view to have no badge")
	}

	cmd := p.consumePendingView("sh-1")
	if cmd != nil {
		t.Errorf("expected nil cmd from expired pending view, got %v", cmd)
	}
}
