package overview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverview_UIRequestPendingView(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{
			"ws-1": {
				ID:       "ws-1",
				TmuxName: "sidecar-sh-sidecar-1",
			},
		},
	}

	req := uirequest.Request{
		ID:        "req-ov-1",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin: uirequest.Origin{
			TmuxSession: "sidecar-sh-sidecar-1",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "README.md",
		},
	}

	// When not selected, queues and acks StatusQueued
	cmd := m.handleUIRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd for unselected workspace, got %v", cmd)
	}

	badge, hasBadge := m.pendingViewBadge("sidecar-sh-sidecar-1")
	if !hasBadge || badge == "" {
		t.Errorf("expected pending view badge on ws-1, got %q, %v", badge, hasBadge)
	}

	acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 || acks[0].Status != uirequest.StatusQueued {
		t.Fatalf("expected 1 queued ack, got %+v", acks)
	}
}
